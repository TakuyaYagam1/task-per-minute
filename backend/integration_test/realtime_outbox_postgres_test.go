//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	eventdelivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
)

func TestRealtimeOutboxPostgresPreservesProjectionOrderAcrossRetryAndRestart(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createDraftMigrationFixture(ctx, t)
	var (
		waveID         uuid.UUID
		waveRevisionID uuid.UUID
		waveRevision   int64
	)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT id
		FROM waves
		WHERE tournament_id = $1
			AND roster_id = $2`, fixture.tournamentID, fixture.rosterID,
	).Scan(&waveID))
	waveRepository := postgres.NewWavePostgres(postgres.NewTxManager(sharedPool))
	wave, err := waveRepository.Get(ctx, fixture.tournamentID, waveID)
	require.NoError(t, err)
	openedAt := fixture.createdAt.Add(time.Second)
	windowID := uuid.New()
	wave, changed, err := waveRepository.OpenReadyWindow(
		ctx,
		fixture.tournamentID,
		waveID,
		wave.Revision,
		postgres.ReadyWindowInput{
			ID: windowID, RevisionID: domain.ReadyWindowRevisionID(uuid.New()),
			OpenedAt: openedAt, Deadline: openedAt.Add(domain.ReadyWindowDuration),
		},
	)
	require.NoError(t, err)
	require.True(t, changed)
	for index, participantID := range fixture.participantIDs {
		wave, changed, err = waveRepository.MarkReady(
			ctx,
			fixture.tournamentID,
			waveID,
			windowID,
			participantID,
			wave.Revision,
			wave.ReadinessRevisions[participantID],
			openedAt.Add(time.Duration(index+1)*time.Second),
		)
		require.NoError(t, err)
		require.True(t, changed)
	}
	wave, changed, err = waveRepository.Start(
		ctx,
		fixture.tournamentID,
		waveID,
		windowID,
		wave.Revision,
		openedAt.Add(3*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WaveStateActive, wave.Wave.State)
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT revision_id, revision
		FROM waves
		WHERE id = $1`, waveID,
	).Scan(&waveRevisionID, &waveRevision))

	principalID := uuid.New()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	cancellationInput := cancellationRepositoryInput(ctx, t, fixture, uuid.New())
	type publishOutcome struct {
		changed bool
		err     error
	}
	start := make(chan struct{})
	published := make(chan publishOutcome, 2)
	var publishers sync.WaitGroup
	publishers.Add(2)
	go func() {
		defer publishers.Done()
		<-start
		_, publishErr := sqlc.New(sharedPool).CreateWaveDisclosureOutboxEvent(
			ctx,
			sqlc.CreateWaveDisclosureOutboxEventParams{
				TournamentID:           fixture.tournamentID,
				RosterID:               fixture.rosterID,
				Audience:               string(eventdelivery.AudienceOperator),
				PrincipalID:            principalID,
				Payload:                []byte(`{"wave":"disclosed"}`),
				IdempotencyKey:         uuid.New(),
				WaveID:                 waveID,
				ExpectedWaveRevisionID: waveRevisionID,
				ExpectedWaveRevision:   waveRevision,
				CreatedAt: pgtype.Timestamptz{
					Time: createdAt, Valid: true,
				},
				ID: uuid.New(),
			},
		)
		published <- publishOutcome{changed: publishErr == nil, err: publishErr}
	}()
	go func() {
		defer publishers.Done()
		<-start
		_, changed, publishErr := postgres.NewTournamentCancellationPostgres(
			postgres.NewTxManager(sharedPool),
		).CancelTournament(ctx, cancellationInput)
		published <- publishOutcome{changed: changed, err: publishErr}
	}()
	close(start)
	publishers.Wait()
	close(published)

	for outcome := range published {
		require.NoError(t, outcome.err)
		require.True(t, outcome.changed)
	}

	repository := postgres.NewRealtimeOutboxPostgres(postgres.NewTxManager(sharedPool))
	replayed, err := repository.ListAfter(t.Context(), eventdelivery.ReplayRequest{
		TournamentID: fixture.tournamentID,
		Limit:        10,
		Audience:     eventdelivery.AudienceOperator,
		PrincipalID:  principalID,
	})
	require.NoError(t, err)
	require.Len(t, replayed, 2)
	require.Equal(t, []int64{1, 2}, []int64{replayed[0].Sequence, replayed[1].Sequence})
	require.Equal(t, []int32{1, 2}, []int32{replayed[0].ProjectionOrdinal, replayed[1].ProjectionOrdinal})
	require.Equal(t, replayed[0].ProjectionRevisionID, replayed[1].ProjectionRevisionID)
	require.Equal(t, replayed[0].ProjectionRevision, replayed[1].ProjectionRevision)
	require.NotEqual(t, replayed[0].Terminal, replayed[1].Terminal)

	failedAt := createdAt.Add(time.Second)
	failedWorker, err := eventdelivery.NewWorker(
		repository,
		outboxSink(func(eventdelivery.Event) error { return errors.New("sink unavailable") }),
		outboxWorkerConfig(failedAt),
	)
	require.NoError(t, err)
	result, err := failedWorker.Process(t.Context())
	require.ErrorIs(t, err, eventdelivery.ErrDeliveryFailed)
	require.Equal(t, eventdelivery.ProcessResult{Claimed: 1, Retried: 1}, result)

	restartedAt := failedAt.Add(2 * time.Millisecond)
	delivered := make([]int64, 0, 2)
	restartedWorker, err := eventdelivery.NewWorker(
		postgres.NewRealtimeOutboxPostgres(postgres.NewTxManager(sharedPool)),
		outboxSink(func(event eventdelivery.Event) error {
			delivered = append(delivered, event.Sequence)
			return nil
		}),
		outboxWorkerConfig(restartedAt),
	)
	require.NoError(t, err)
	for range 2 {
		result, err = restartedWorker.Process(t.Context())
		require.NoError(t, err)
		require.Equal(t, eventdelivery.ProcessResult{Claimed: 1, Acknowledged: 1}, result)
	}
	require.Equal(t, []int64{1, 2}, delivered)
	result, err = restartedWorker.Process(t.Context())
	require.NoError(t, err)
	require.Equal(t, eventdelivery.ProcessResult{}, result)

	replayed, err = repository.ListAfter(t.Context(), eventdelivery.ReplayRequest{
		TournamentID: fixture.tournamentID,
		Limit:        10,
		Audience:     eventdelivery.AudienceOperator,
		PrincipalID:  principalID,
	})
	require.NoError(t, err)
	require.Len(t, replayed, 2)
	require.Equal(t, []int64{1, 2}, []int64{replayed[0].Sequence, replayed[1].Sequence})
	require.Equal(t, []int32{1, 2}, []int32{replayed[0].ProjectionOrdinal, replayed[1].ProjectionOrdinal})
	cursor, err := repository.Cursor(t.Context(), fixture.tournamentID)
	require.NoError(t, err)
	require.Equal(t, int64(2), cursor)
}

type outboxSink func(eventdelivery.Event) error

func (sink outboxSink) Deliver(_ context.Context, event eventdelivery.Event) error {
	return sink(event)
}

func outboxWorkerConfig(now time.Time) eventdelivery.WorkerConfig {
	return eventdelivery.WorkerConfig{
		WorkerID:       uuid.New(),
		BatchSize:      2,
		LeaseDuration:  time.Minute,
		MinimumBackoff: time.Millisecond,
		MaximumBackoff: time.Millisecond,
		Now:            func() time.Time { return now },
		NewToken:       uuid.New,
	}
}

func TestRealtimeOutboxPostgresUsesDeliveryIndexes(t *testing.T) {
	ctx := context.Background()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tx.Rollback(ctx)) })
	_, err = tx.Exec(ctx, "SET LOCAL enable_seqscan = off")
	require.NoError(t, err)
	var claimIndexDefinition, resumeIndexDefinition string
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT indexdef
		FROM pg_indexes
		WHERE schemaname = current_schema()
			AND indexname = 'outbox_events_claim_idx'`,
	).Scan(&claimIndexDefinition))
	require.Contains(t, claimIndexDefinition, "WHERE (published_at IS NULL)")
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT indexdef
		FROM pg_indexes
		WHERE schemaname = current_schema()
			AND indexname = 'outbox_events_resume_idx'`,
	).Scan(&resumeIndexDefinition))
	require.Contains(t, resumeIndexDefinition, "(tournament_id, sequence)")

	tests := []struct {
		name          string
		query         string
		args          []any
		acceptedIndex []string
	}{
		{
			name: "unpublished claim",
			query: `EXPLAIN (FORMAT JSON, COSTS OFF)
				SELECT event.id
				FROM outbox_events AS event
				INNER JOIN projection_revisions AS revision
					ON revision.id = event.projection_revision_id
					AND revision.tournament_id = event.tournament_id
					AND revision.roster_id = event.roster_id
					AND revision.revision_number = event.projection_revision
				WHERE event.published_at IS NULL
					AND revision.state IN ('published', 'superseded')
					AND event.available_at <= $1
					AND (event.claimed_until IS NULL OR event.claimed_until <= $1)
					AND NOT EXISTS (
						SELECT 1
						FROM outbox_events AS predecessor
						WHERE predecessor.tournament_id = event.tournament_id
							AND predecessor.sequence < event.sequence
							AND predecessor.published_at IS NULL
					)
				ORDER BY event.tournament_id, event.sequence
				LIMIT $2
				FOR UPDATE OF event SKIP LOCKED`,
			args: []any{time.Now().UTC(), int32(32)},
			acceptedIndex: []string{
				"outbox_events_claim_idx",
				"outbox_events_resume_idx",
			},
		},
		{
			name: "tournament resume",
			query: `EXPLAIN (FORMAT JSON, COSTS OFF)
				SELECT event.id
				FROM outbox_events AS event
				INNER JOIN projection_revisions AS revision
					ON revision.id = event.projection_revision_id
					AND revision.tournament_id = event.tournament_id
					AND revision.roster_id = event.roster_id
					AND revision.revision_number = event.projection_revision
				WHERE event.tournament_id = $1
					AND event.sequence > $2
					AND revision.state IN ('published', 'superseded')
					AND (
						event.audience = 'all'
						OR (event.audience = 'public' AND event.principal_id IS NULL)
					)
				ORDER BY event.tournament_id, event.sequence
				LIMIT $3`,
			args: []any{uuid.New(), int64(10), int32(128)},
			acceptedIndex: []string{
				"outbox_events_resume_idx",
				"outbox_events_sequence_key",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var rawPlan []byte
			err := tx.QueryRow(ctx, test.query, test.args...).Scan(&rawPlan)
			require.NoError(t, err)

			var plan any
			require.NoError(t, json.Unmarshal(rawPlan, &plan))
			indexes := postgresPlanIndexes(plan)
			require.NotEmpty(t, firstMatchingIndex(indexes, test.acceptedIndex), "plan indexes: %v", indexes)
		})
	}
}

func postgresPlanIndexes(value any) []string {
	indexes := make([]string, 0, 2)
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			indexes = append(indexes, postgresPlanIndexes(item)...)
		}
	case map[string]any:
		for key, item := range typed {
			if key == "Index Name" {
				if index, ok := item.(string); ok {
					indexes = append(indexes, index)
				}
			}
			indexes = append(indexes, postgresPlanIndexes(item)...)
		}
	}
	return indexes
}

func firstMatchingIndex(actual, accepted []string) string {
	for _, candidate := range actual {
		for _, expected := range accepted {
			if candidate == expected {
				return candidate
			}
		}
	}
	return ""
}
