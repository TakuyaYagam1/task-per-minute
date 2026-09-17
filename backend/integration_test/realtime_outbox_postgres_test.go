//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	waverepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/execution/wave"
	realtimerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/realtime"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	cancellationrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/cancellation"
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
	waveRepository := waverepo.NewWavePostgres(postgres.NewTxManager(sharedPool))
	wave, err := waveRepository.Get(ctx, fixture.tournamentID, waveID)
	require.NoError(t, err)
	openedAt := fixture.createdAt.Add(time.Second)
	windowID := uuid.New()
	wave, changed, err := waveRepository.OpenReadyWindow(
		ctx,
		fixture.tournamentID,
		waveID,
		wave.Revision,
		waverepo.ReadyWindowInput{
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
		_, changed, publishErr := cancellationrepo.NewTournamentCancellationPostgres(
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

	repository := realtimerepo.NewRealtimeOutboxPostgres(postgres.NewTxManager(sharedPool))
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
		realtimerepo.NewRealtimeOutboxPostgres(postgres.NewTxManager(sharedPool)),
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
