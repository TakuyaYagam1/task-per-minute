//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	admin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	progression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

type playoffPublicationClock struct{ now time.Time }

func (clock playoffPublicationClock) Now() time.Time {
	if !clock.now.IsZero() {
		return clock.now.UTC().Truncate(time.Microsecond)
	}
	return time.Now().UTC().Truncate(time.Microsecond)
}

func TestPlayoffStagePublicationFromSwissReceipt(t *testing.T) {
	ctx := context.Background()
	fixture := prepareFinalSwissSettlement(ctx, t)
	settleSwissReceiptSeries(ctx, t, fixture, 1)
	closeSwissReceiptWave(ctx, t, fixture)
	_, sourceRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	command := progression.Command{CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: sourceRevision, Action: progression.ActionStartPlayoffs}
	view, err := publishSwissPlayoffs(ctx, fixture, command)
	require.NoError(t, err)
	require.Equal(t, domain.TournamentStatePlayoffs, view.State)
	var top4, bracket, semifinals, outbox int
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM projection_artifacts AS artifact JOIN projection_revisions AS revision ON revision.id = artifact.produced_by_revision_id WHERE artifact.tournament_id = $1 AND artifact.artifact_kind = 'top_four' AND revision.revision_number = $3),
		(SELECT count(*) FROM projection_artifacts AS artifact JOIN projection_revisions AS revision ON revision.id = artifact.produced_by_revision_id WHERE artifact.tournament_id = $1 AND artifact.artifact_kind = 'bracket' AND revision.revision_number = $3),
		(SELECT count(*) FROM tournament_stage_playoff_semifinals WHERE command_id = $2),
		(SELECT count(*) FROM outbox_events WHERE tournament_id = $1 AND projection_revision = $3)`, fixture.tournamentID, command.CommandID, sourceRevision+1).
		Scan(&top4, &bracket, &semifinals, &outbox))
	require.Equal(t, 1, top4)
	require.Equal(t, 1, bracket)
	require.Equal(t, 2, semifinals)
	require.Equal(t, 1, outbox)
	assertPlayoffPublicationEvent(ctx, t, fixture, command)
}

func assertPlayoffPublicationEvent(ctx context.Context, t *testing.T, fixture tournamentAdminSwissProofFixture, command progression.Command) {
	t.Helper()
	var topic, audience string
	var terminal bool
	var payload []byte
	var revisionID, top4ID, bracketID, receiptID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT event.topic, event.audience, event.terminal, event.payload,
		source.projection_revision_id, source.top4_artifact_id, source.bracket_artifact_id, source.swiss_receipt_projection_revision_id
		FROM outbox_events AS event JOIN outbox_stage_projection_sources AS source ON source.outbox_event_id = event.id
		WHERE source.stage_command_id = $1`, command.CommandID).
		Scan(&topic, &audience, &terminal, &payload, &revisionID, &top4ID, &bracketID, &receiptID))
	require.Equal(t, "tournament.playoffs.published", topic)
	require.Equal(t, "all", audience)
	require.True(t, terminal)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(payload, &fields))
	require.Len(t, fields, 8)
	require.Equal(t, "playoffs-publication-v1", fields["schema"])
	require.Equal(t, command.CommandID.String(), fields["stage_command_id"])
	require.Equal(t, revisionID.String(), fields["projection_revision_id"])
	require.Equal(t, float64(command.ExpectedProjectionRevision+1), fields["projection_revision"])
	require.Equal(t, top4ID.String(), fields["top4_artifact_id"])
	require.Equal(t, bracketID.String(), fields["bracket_artifact_id"])
	for _, key := range []string{"top4_digest", "bracket_digest"} {
		require.Regexp(t, "^[0-9a-f]{64}$", fields[key])
	}
	var receiptCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT count(*) FROM final_swiss_projection_receipts
		WHERE projection_revision_id = $1 AND tournament_id = $2 AND roster_id = $3`, receiptID, fixture.tournamentID, fixture.rosterID).Scan(&receiptCount))
	require.Equal(t, 1, receiptCount)
}

func preparePlayoffPublication(ctx context.Context, t *testing.T) (tournamentAdminSwissProofFixture, progression.Command) {
	t.Helper()
	fixture := prepareFinalSwissSettlement(ctx, t)
	settleSwissReceiptSeries(ctx, t, fixture, 1)
	closeSwissReceiptWave(ctx, t, fixture)
	_, revision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	return fixture, progression.Command{CommandID: uuid.New(), TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ActorID: uuid.New(), ExpectedProjectionRevision: revision, Action: progression.ActionStartPlayoffs}
}

func TestPlayoffTop4AndBracketPublicationRollback(t *testing.T) {
	ctx := context.Background()
	fixture, command := preparePlayoffPublication(ctx, t)
	before := playoffPublicationCounts(ctx, t, fixture.tournamentID)
	beforeID, beforeRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	abort := errors.New("stop after stage publication")
	err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
		_, err := publishSwissPlayoffs(txCtx, fixture, command)
		if err != nil {
			return err
		}
		return abort
	})
	require.ErrorIs(t, err, abort)
	require.Equal(t, before, playoffPublicationCounts(ctx, t, fixture.tournamentID))
	afterID, afterRevision := currentPublishedProjection(ctx, t, fixture.tournamentID, fixture.rosterID)
	require.Equal(t, beforeID, afterID)
	require.Equal(t, beforeRevision, afterRevision)
	_, err = publishSwissPlayoffs(ctx, fixture, command)
	require.NoError(t, err)
	assertPlayoffPublicationEvent(ctx, t, fixture, command)
}

func TestPlayoffTop4AndBracketConcurrentPublication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, command := preparePlayoffPublication(ctx, t)
	before := playoffPublicationCounts(ctx, t, fixture.tournamentID)
	commands := [2]progression.Command{command, command}
	commands[1].CommandID = uuid.New()
	errorsByWriter := [2]error{}
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(2)
	done.Add(2)
	for index := range commands {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			_, errorsByWriter[index] = publishSwissPlayoffs(ctx, fixture, commands[index])
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	winner := -1
	for index, err := range errorsByWriter {
		if err == nil {
			require.Equal(t, -1, winner, "only one writer may publish the shared Top4/bracket revision")
			winner = index
		} else {
			require.ErrorIs(t, err, domain.ErrConflict)
		}
	}
	require.NotEqual(t, -1, winner)
	after := playoffPublicationCounts(ctx, t, fixture.tournamentID)
	require.Equal(t, before[0]+1, after[0], "projection")
	require.Equal(t, before[2]+2, after[2], "Top4 and bracket artifacts")
	require.Equal(t, before[3]+1, after[3], "stage proof")
	require.Equal(t, before[4]+1, after[4], "outbox event")
	require.Equal(t, before[5]+1, after[5], "normalized source")
	assertPlayoffPublicationEvent(ctx, t, fixture, commands[winner])
}

func playoffPublicationCounts(ctx context.Context, t *testing.T, tournamentID uuid.UUID) [6]int64 {
	t.Helper()
	var counts [6]int64
	require.NoError(t, sharedPool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM projection_revisions WHERE tournament_id = $1),
		(SELECT count(*) FROM projection_dependencies WHERE tournament_id = $1),
		(SELECT count(*) FROM projection_artifacts WHERE tournament_id = $1 AND artifact_kind IN ('top_four', 'bracket')),
		(SELECT count(*) FROM tournament_stage_progressions WHERE tournament_id = $1),
		(SELECT count(*) FROM outbox_events WHERE tournament_id = $1),
		(SELECT count(*) FROM outbox_stage_projection_sources WHERE tournament_id = $1)`, tournamentID).
		Scan(&counts[0], &counts[1], &counts[2], &counts[3], &counts[4], &counts[5]))
	return counts
}

func TestPlayoffStageOutboxRejectsInvalidAndMultipleSources(t *testing.T) {
	ctx := context.Background()
	fixture, command := preparePlayoffPublication(ctx, t)
	_, err := publishSwissPlayoffs(ctx, fixture, command)
	require.NoError(t, err)
	before := playoffPublicationCounts(ctx, t, fixture.tournamentID)
	t.Run("wrong artifact kind", func(t *testing.T) {
		_, err := sharedPool.Exec(ctx, `INSERT INTO outbox_stage_projection_sources
			SELECT outbox_event_id, tournament_id, roster_id, stage_command_id, projection_revision_id,
				projection_revision, projection_ordinal, swiss_receipt_projection_revision_id,
				top4_artifact_id, 'standings', bracket_artifact_id, bracket_kind, created_at
			FROM outbox_stage_projection_sources WHERE stage_command_id = $1`, command.CommandID)
		var databaseError *pgconn.PgError
		require.ErrorAs(t, err, &databaseError)
		require.Equal(t, "23514", databaseError.Code)
		require.Equal(t, "outbox_stage_projection_sources_identity_check", databaseError.ConstraintName)
	})
	t.Run("late second source", func(t *testing.T) {
		// Closing Swiss produced a fresh Wave revision with no disclosure event.
		// It satisfies the Wave source's local FKs and current-head checks, but
		// must not become a second namespace for the committed playoff event.
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, `INSERT INTO outbox_wave_sources
			(outbox_event_id, tournament_id, roster_id, wave_id, wave_revision_id, wave_revision,
			 projection_revision_id, projection_revision, projection_ordinal, created_at)
			SELECT source.outbox_event_id, source.tournament_id, source.roster_id, wave.id, wave.revision_id, wave.revision,
				source.projection_revision_id, source.projection_revision, source.projection_ordinal, source.created_at
			FROM outbox_stage_projection_sources AS source JOIN waves AS wave ON wave.id = $2
			WHERE source.stage_command_id = $1`, command.CommandID, fixture.waveID)
		require.NoError(t, err, "all namespace-local checks should pass before deferred exclusivity")
		err = tx.Commit(ctx)
		var databaseError *pgconn.PgError
		require.ErrorAs(t, err, &databaseError)
		require.Equal(t, "23514", databaseError.Code)
		require.Contains(t, databaseError.Message, "exactly one normalized source binding")
	})
	require.Equal(t, before, playoffPublicationCounts(ctx, t, fixture.tournamentID))
}

// This is the application-owned lifecycle transaction: the production
// progression workflow owns stage planning/persistence, and the lifecycle
// repository seals its command before the deferred constraints are checked.
func publishSwissPlayoffs(ctx context.Context, fixture tournamentAdminSwissProofFixture, command progression.Command) (inbound.TournamentView, error) {
	return publishSwissPlayoffsWithClock(ctx, fixture, command, playoffPublicationClock{})
}

func publishSwissPlayoffsWithClock(
	ctx context.Context,
	fixture tournamentAdminSwissProofFixture,
	command progression.Command,
	clock playoffPublicationClock,
) (inbound.TournamentView, error) {
	var result inbound.TournamentView
	err := fixture.tx.Do(ctx, func(txCtx context.Context) error {
		var reservedAt time.Time
		if err := fixture.tx.Conn(txCtx).QueryRow(txCtx,
			`SELECT locked_at FROM rosters WHERE id = $1`, fixture.rosterID,
		).Scan(&reservedAt); err != nil {
			return err
		}
		if _, err := fixture.tx.Querier(txCtx).ReserveCheckedInTournamentParticipants(
			txCtx,
			sqlc.ReserveCheckedInTournamentParticipantsParams{
				RosterID:   fixture.rosterID,
				AcquiredAt: pgtype.Timestamptz{Time: reservedAt, Valid: true},
			},
		); err != nil {
			return err
		}
		lifecycle := postgres.NewTournamentAdminLifecyclePostgres(fixture.tx)
		authority, err := lifecycle.LockLifecycleAuthority(txCtx, fixture.tournamentID)
		if err != nil {
			return err
		}
		if authority.ProjectionRevision != command.ExpectedProjectionRevision {
			return domain.ErrConflict
		}
		repository := observedPlayoffPublication{postgres.NewTournamentProgressionPostgres(fixture.tx)}
		workflow := progression.NewWorkflow(progression.ProgressionDependencies{
			Repository: repository, TerminalEvidence: repository, Transitioner: repository, Publisher: repository, ProgressionClock: clock,
		})
		receipt, err := workflow.Advance(txCtx, command, progression.Authority{
			Tournament: authority.Tournament, ProjectionRevisionID: authority.ProjectionRevisionID, ProjectionRevision: authority.ProjectionRevision,
		})
		if err != nil {
			return err
		}
		result = receipt.Result
		return lifecycle.SaveLifecycleCommand(txCtx, admin.LifecycleCommandRecord{
			CommandScope: admin.CommandScope{Operator: admin.OperatorIdentity{ActorID: command.ActorID}, TournamentID: command.TournamentID, CommandID: command.CommandID},
			Action:       admin.TournamentActionStartPlayoffs, SourceProjectionRevisionID: authority.ProjectionRevisionID,
			SourceProjectionRevision: authority.ProjectionRevision, SourceTournamentRevision: authority.Tournament.Revision,
			SourceTournamentState: authority.Tournament.State, Result: result, ExecutedAt: result.UpdatedAt,
		})
	})
	return result, err
}

type observedPlayoffPublication struct {
	*postgres.TournamentProgressionPostgres
}

func (r observedPlayoffPublication) LoadSwissEvidence(ctx context.Context, authority progression.Authority) (progression.SwissEvidence, error) {
	evidence, err := r.TournamentProgressionPostgres.LoadSwissEvidence(ctx, authority)
	if err != nil {
		return evidence, fmt.Errorf("load Swiss evidence: %w", err)
	}
	return evidence, nil
}

func (r observedPlayoffPublication) PublishPlayoffStage(ctx context.Context, plan progression.Plan) (progression.PlayoffPublication, error) {
	publication, err := r.TournamentProgressionPostgres.PublishPlayoffStage(ctx, plan)
	if err != nil {
		return publication, fmt.Errorf("publish playoff stage: %w", err)
	}
	return publication, nil
}

func (r observedPlayoffPublication) PersistStageProgression(ctx context.Context, plan progression.Plan, publication *progression.PlayoffPublication) (progression.PersistenceReceipt, error) {
	receipt, err := r.TournamentProgressionPostgres.PersistStageProgression(ctx, plan, publication)
	if err != nil {
		return receipt, fmt.Errorf("persist progression: %w", err)
	}
	return receipt, nil
}

func (r observedPlayoffPublication) TransitionStage(ctx context.Context, command progression.TransitionCommand) (inbound.TournamentView, bool, error) {
	view, changed, err := r.TournamentProgressionPostgres.TransitionStage(ctx, command)
	if err != nil {
		return view, changed, fmt.Errorf("transition stage: %w", err)
	}
	return view, changed, nil
}
