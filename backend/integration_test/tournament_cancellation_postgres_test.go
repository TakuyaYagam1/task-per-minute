//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	cancellationrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/cancellation"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
)

func TestTournamentCancellationPostgresUsesLockedProjectionTargetAndReplaysExactly(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createDraftMigrationFixture(ctx, t)
	input := cancellationRepositoryInput(ctx, t, fixture, uuid.New())
	var expectedProjectionID uuid.UUID
	var expectedProjectionRevision int64
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id, revision_number
		FROM projection_revisions
		WHERE tournament_id = $1
			AND roster_id = $2
			AND state = 'published'
		FOR KEY SHARE`, fixture.tournamentID, fixture.rosterID,
	).Scan(&expectedProjectionID, &expectedProjectionRevision))

	repositories := []*cancellationrepo.TournamentCancellationPostgres{
		cancellationrepo.NewTournamentCancellationPostgres(postgres.NewTxManager(sharedPool)),
		cancellationrepo.NewTournamentCancellationPostgres(postgres.NewTxManager(sharedPool)),
	}
	type outcome struct {
		record  *tournamentcancellation.TournamentCancellationRecord
		changed bool
		err     error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, len(repositories))
	var workers sync.WaitGroup
	for _, repository := range repositories {
		workers.Add(1)
		go func(repository *cancellationrepo.TournamentCancellationPostgres) {
			defer workers.Done()
			<-start
			record, changed, err := repository.CancelTournament(ctx, input)
			outcomes <- outcome{record: record, changed: changed, err: err}
		}(repository)
	}
	close(start)
	workers.Wait()
	close(outcomes)

	changedCount := 0
	replayedCount := 0
	for outcome := range outcomes {
		require.NoError(t, outcome.err)
		require.NotNil(t, outcome.record)
		require.Equal(t, input.CommandID, outcome.record.CommandID)
		if outcome.changed {
			changedCount++
		} else {
			replayedCount++
		}
	}
	require.Equal(t, 1, changedCount)
	require.Equal(t, 1, replayedCount)

	var (
		eventProjectionID        uuid.UUID
		eventProjectionRevision  int64
		eventProjectionOrdinal   int16
		sourceProjectionID       uuid.UUID
		sourceProjectionRevision int64
		sourceProjectionOrdinal  int16
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT event.projection_revision_id,
			event.projection_revision,
			event.projection_ordinal,
			source.projection_revision_id,
			source.projection_revision,
			source.projection_ordinal
		FROM outbox_events AS event
		INNER JOIN outbox_tournament_cancellation_sources AS source
			ON source.outbox_event_id = event.id
			AND source.tournament_id = event.tournament_id
			AND source.roster_id = event.roster_id
		WHERE source.cancellation_command_id = $1`, input.CommandID,
	).Scan(
		&eventProjectionID,
		&eventProjectionRevision,
		&eventProjectionOrdinal,
		&sourceProjectionID,
		&sourceProjectionRevision,
		&sourceProjectionOrdinal,
	))
	require.Equal(t, expectedProjectionID, eventProjectionID)
	require.Equal(t, expectedProjectionRevision, eventProjectionRevision)
	require.GreaterOrEqual(t, eventProjectionOrdinal, int16(1))
	require.Equal(t, eventProjectionID, sourceProjectionID)
	require.Equal(t, eventProjectionRevision, sourceProjectionRevision)
	require.Equal(t, eventProjectionOrdinal, sourceProjectionOrdinal)
}

func TestTournamentCancellationPostgresRejectsCrossScopeCommandReuse(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	first := createDraftMigrationFixture(ctx, t)
	second := createDraftMigrationFixture(ctx, t)
	commandID := uuid.New()
	inputs := []tournamentcancellation.TournamentCancellationInput{
		cancellationRepositoryInput(ctx, t, first, commandID),
		cancellationRepositoryInput(ctx, t, second, commandID),
	}
	type outcome struct {
		input   tournamentcancellation.TournamentCancellationInput
		changed bool
		err     error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, len(inputs))
	var workers sync.WaitGroup
	for _, input := range inputs {
		workers.Add(1)
		go func(input tournamentcancellation.TournamentCancellationInput) {
			defer workers.Done()
			<-start
			_, changed, err := cancellationrepo.NewTournamentCancellationPostgres(
				postgres.NewTxManager(sharedPool),
			).CancelTournament(ctx, input)
			outcomes <- outcome{input: input, changed: changed, err: err}
		}(input)
	}
	close(start)
	workers.Wait()
	close(outcomes)

	changedCount := 0
	conflictedCount := 0
	var conflicted tournamentcancellation.TournamentCancellationInput
	for outcome := range outcomes {
		switch {
		case outcome.err == nil && outcome.changed:
			changedCount++
		case errors.Is(outcome.err, domain.ErrConflict):
			conflictedCount++
			conflicted = outcome.input
		default:
			require.NoError(t, outcome.err)
		}
	}
	require.Equal(t, 1, changedCount)
	require.Equal(t, 1, conflictedCount)

	var (
		state    string
		revision int64
		ledger   int
		outbox   int
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT state, revision
		FROM tournaments
		WHERE id = $1`, conflicted.TournamentID,
	).Scan(&state, &revision))
	require.Equal(t, string(domain.TournamentStateDraft), state)
	require.Equal(t, conflicted.ExpectedRevision, revision)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM tournament_cancellations
		WHERE tournament_id = $1`, conflicted.TournamentID,
	).Scan(&ledger))
	require.Zero(t, ledger)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM outbox_events
		WHERE tournament_id = $1`, conflicted.TournamentID,
	).Scan(&outbox))
	require.Zero(t, outbox)
}

func cancellationRepositoryInput(
	ctx context.Context,
	tb testing.TB,
	fixture draftMigrationFixture,
	commandID uuid.UUID,
) tournamentcancellation.TournamentCancellationInput {
	tb.Helper()

	var (
		revision int64
		state    string
	)
	require.NoError(tb, sharedPool.QueryRow(ctx, `
		SELECT revision, state
		FROM tournaments
		WHERE id = $1`, fixture.tournamentID,
	).Scan(&revision, &state))
	return tournamentcancellation.TournamentCancellationInput{
		TournamentID:     fixture.tournamentID,
		ExpectedRevision: revision,
		ExpectedState:    domain.TournamentState(state),
		CommandID:        commandID,
		ActorID:          uuid.New(),
		Reason:           "operator cancellation request",
		CancelledAt:      time.Now().UTC().Truncate(time.Microsecond),
	}
}
