package eventdelivery_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	delivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
	deliverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery/mocks"
)

const eventDeliveryTestWait = 2 * time.Second

func TestWorkerDeliversAndAcknowledgesClaimedEvent(t *testing.T) {
	t.Parallel()

	store := deliverymocks.NewMockStore(t)
	sink := deliverymocks.NewMockSink(t)
	observer := deliverymocks.NewMockWorkerObserver(t)
	event := validEvent()
	config := workerConfig(event.OccurredAt)
	store.EXPECT().Claim(mock.Anything, delivery.ClaimRequest{
		WorkerID: config.WorkerID, Token: config.NewToken(), Limit: config.BatchSize,
		ClaimedAt: event.OccurredAt, LeaseEnds: event.OccurredAt.Add(config.LeaseDuration),
	}).Return([]delivery.Event{event}, nil).Once()
	sink.EXPECT().Deliver(mock.Anything, event).Return(nil).Once()
	store.EXPECT().Acknowledge(mock.Anything, delivery.Acknowledgement{
		EventID: event.ID, WorkerID: config.WorkerID, ClaimToken: config.NewToken(),
		AcknowledgedAt: event.OccurredAt,
	}).Return(true, nil).Once()
	observer.EXPECT().ObserveEventDelivery(mock.Anything, delivery.WorkerEvent{
		EventID: event.ID, CorrelationID: event.CorrelationID, TournamentID: event.TournamentID,
		ProjectionRevision: event.ProjectionRevision, Sequence: event.Sequence,
		Outcome: delivery.WorkerOutcomeSuccess, Transition: "acknowledged",
		ReasonCode: "delivered",
	}).Once()
	worker, err := delivery.NewWorker(store, sink, config, observer)
	require.NoError(t, err)

	result, err := worker.Process(t.Context())

	require.NoError(t, err)
	require.Equal(t, delivery.ProcessResult{Claimed: 1, Acknowledged: 1}, result)
	health := worker.Health(event.OccurredAt)
	require.False(t, health.Started)
	require.False(t, health.Running)
	require.Zero(t, health.ConsecutiveFailures)
	require.Equal(t, event.OccurredAt, *health.LastSuccessAt)
}

func TestWorkerRetriesSinkFailureWithBoundedBackoff(t *testing.T) {
	t.Parallel()

	store := deliverymocks.NewMockStore(t)
	sink := deliverymocks.NewMockSink(t)
	observer := deliverymocks.NewMockWorkerObserver(t)
	event := validEvent()
	event.AttemptCount = 3
	config := workerConfig(event.OccurredAt)
	sinkErr := errors.New("socket unavailable")
	store.EXPECT().Claim(mock.Anything, mock.Anything).Return([]delivery.Event{event}, nil).Once()
	sink.EXPECT().Deliver(mock.Anything, event).Return(sinkErr).Once()
	store.EXPECT().Retry(mock.Anything, delivery.Retry{
		EventID: event.ID, WorkerID: config.WorkerID, ClaimToken: config.NewToken(),
		AvailableAt: event.OccurredAt.Add(400 * time.Millisecond), Reason: "sink_failed",
	}).Return(true, nil).Once()
	observer.EXPECT().ObserveEventDelivery(mock.Anything, delivery.WorkerEvent{
		EventID: event.ID, CorrelationID: event.CorrelationID, TournamentID: event.TournamentID,
		ProjectionRevision: event.ProjectionRevision, Sequence: event.Sequence,
		Outcome: delivery.WorkerOutcomeRetry, Transition: "retry_scheduled",
		ReasonCode: "sink_failed",
	}).Once()
	worker, err := delivery.NewWorker(store, sink, config, observer)
	require.NoError(t, err)

	result, err := worker.Process(t.Context())

	require.ErrorIs(t, err, delivery.ErrDeliveryFailed)
	require.ErrorIs(t, err, sinkErr)
	require.Equal(t, delivery.ProcessResult{Claimed: 1, Retried: 1}, result)
	health := worker.Health(event.OccurredAt)
	require.Equal(t, int64(1), health.ConsecutiveFailures)
	require.Equal(t, event.OccurredAt, *health.LastFailureAt)
}

func TestWorkerIsolatesObserverPanicAfterTerminalOutcome(t *testing.T) {
	t.Parallel()

	store := deliverymocks.NewMockStore(t)
	sink := deliverymocks.NewMockSink(t)
	observer := deliverymocks.NewMockWorkerObserver(t)
	event := validEvent()
	config := workerConfig(event.OccurredAt)
	store.EXPECT().Claim(mock.Anything, mock.Anything).Return([]delivery.Event{event}, nil).Once()
	sink.EXPECT().Deliver(mock.Anything, event).Return(nil).Once()
	store.EXPECT().Acknowledge(mock.Anything, mock.Anything).Return(true, nil).Once()
	observer.EXPECT().ObserveEventDelivery(mock.Anything, mock.Anything).
		Run(func(context.Context, delivery.WorkerEvent) { panic("observer failed") }).Once()
	worker, err := delivery.NewWorker(store, sink, config, observer)
	require.NoError(t, err)

	var result delivery.ProcessResult
	require.NotPanics(t, func() {
		result, err = worker.Process(t.Context())
	})
	require.NoError(t, err)
	require.Equal(t, delivery.ProcessResult{Claimed: 1, Acknowledged: 1}, result)
}

func TestWorkerRejectsConcurrentRunAndReleasesClaims(t *testing.T) {
	t.Parallel()

	store := deliverymocks.NewMockStore(t)
	sink := deliverymocks.NewMockSink(t)
	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	store.EXPECT().Claim(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ delivery.ClaimRequest) ([]delivery.Event, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}).Once()
	store.EXPECT().ReleaseClaims(mock.Anything, workerConfig(at).WorkerID).Return(nil).Once()
	worker, err := delivery.NewWorker(store, sink, workerConfig(at))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	awaitEventDeliverySignal(t, started)

	require.ErrorIs(t, worker.Run(t.Context()), delivery.ErrWorkerRunning)
	require.True(t, worker.Health(at).Running)
	cancel()
	require.NoError(t, awaitEventDeliveryWorker(t, done))
	require.False(t, worker.Health(at).Running)
}

func TestWorkerHealthBecomesStaleWhilePolling(t *testing.T) {
	t.Parallel()

	store := deliverymocks.NewMockStore(t)
	sink := deliverymocks.NewMockSink(t)
	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	processed := make(chan struct{})
	store.EXPECT().Claim(mock.Anything, mock.Anything).
		Run(func(context.Context, delivery.ClaimRequest) { close(processed) }).
		Return([]delivery.Event{}, nil).Once()
	store.EXPECT().ReleaseClaims(mock.Anything, workerConfig(at).WorkerID).Return(nil).Once()
	config := workerConfig(at)
	config.PollInterval = time.Hour
	config.StaleAfter = time.Minute
	worker, err := delivery.NewWorker(store, sink, config)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	awaitEventDeliverySignal(t, processed)
	require.Eventually(t, func() bool {
		return worker.Health(at).LastSuccessAt != nil
	}, eventDeliveryTestWait, time.Millisecond)

	health := worker.Health(at.Add(time.Minute + time.Nanosecond))
	require.True(t, health.Stale)
	require.True(t, health.Running)
	require.Zero(t, health.ConsecutiveFailures)
	cancel()
	require.NoError(t, awaitEventDeliveryWorker(t, done))
}

func TestWorkerRestartClearsFailureAndRequiresFreshSuccess(t *testing.T) {
	t.Parallel()

	store := deliverymocks.NewMockStore(t)
	sink := deliverymocks.NewMockSink(t)
	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	firstAttempt := make(chan struct{})
	secondAttempt := make(chan struct{})
	store.EXPECT().Claim(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ delivery.ClaimRequest) ([]delivery.Event, error) {
			close(firstAttempt)
			<-ctx.Done()
			return nil, errors.New("temporary dependency failure")
		}).Once()
	store.EXPECT().Claim(mock.Anything, mock.Anything).
		Run(func(context.Context, delivery.ClaimRequest) { close(secondAttempt) }).
		Return([]delivery.Event{}, nil).Once()
	store.EXPECT().ReleaseClaims(mock.Anything, workerConfig(at).WorkerID).Return(nil).Twice()
	worker, err := delivery.NewWorker(store, sink, workerConfig(at))
	require.NoError(t, err)

	firstCtx, cancelFirst := context.WithCancel(t.Context())
	firstDone := make(chan error, 1)
	go func() { firstDone <- worker.Run(firstCtx) }()
	awaitEventDeliverySignal(t, firstAttempt)
	cancelFirst()
	require.NoError(t, awaitEventDeliveryWorker(t, firstDone))
	require.Equal(t, int64(1), worker.Health(at).ConsecutiveFailures)

	secondCtx, cancelSecond := context.WithCancel(t.Context())
	secondDone := make(chan error, 1)
	go func() { secondDone <- worker.Run(secondCtx) }()
	awaitEventDeliverySignal(t, secondAttempt)
	require.Eventually(t, func() bool {
		health := worker.Health(at)
		return health.Running && health.LastSuccessAt != nil && health.ConsecutiveFailures == 0
	}, eventDeliveryTestWait, time.Millisecond)
	cancelSecond()
	require.NoError(t, awaitEventDeliveryWorker(t, secondDone))
}

func workerConfig(at time.Time) delivery.WorkerConfig {
	workerID := uuid.MustParse("20000000-0000-0000-0000-000000000001")
	token := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	return delivery.WorkerConfig{
		WorkerID: workerID, BatchSize: 4, LeaseDuration: time.Second,
		PollInterval: time.Second, MinimumBackoff: 100 * time.Millisecond,
		MaximumBackoff: time.Second, ShutdownTimeout: time.Second, StaleAfter: time.Minute,
		Now: func() time.Time { return at }, NewToken: func() uuid.UUID { return token },
	}
}

func awaitEventDeliverySignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(eventDeliveryTestWait):
		t.Fatal("timed out waiting for event delivery worker")
	}
}

func awaitEventDeliveryWorker(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(eventDeliveryTestWait):
		t.Fatal("event delivery worker did not stop")
		return nil
	}
}
