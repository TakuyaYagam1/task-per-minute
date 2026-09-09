package recovery_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	recoverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery/mocks"
)

const workerTestWait = 2 * time.Second

func TestNewWorkerRejectsIncompleteSweep(t *testing.T) {
	t.Parallel()

	clock := recoverymocks.NewMockClock(t)
	worker, err := recovery.NewWorker(
		recovery.NewDeadlineSweep(nil, nil),
		clock,
		recovery.WorkerConfig{},
	)

	require.Nil(t, worker)
	require.ErrorIs(t, err, recovery.ErrInvalidWorkerConfig)
}

func TestWorkerSweepsImmediatelyOnStartup(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	sweep, source, _ := newDeadlineSweep(t)
	started := make(chan struct{})
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(4)).
		Run(func(context.Context, recovery.DeadlineCursor, int32) { close(started) }).
		Return([]recovery.PendingDeadline{}, nil).Once()
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(at).Maybe()
	worker, err := recovery.NewWorker(sweep, clock, recovery.WorkerConfig{
		BatchSize: 4, SweepInterval: time.Hour, SweepTimeout: time.Second, StaleAfter: time.Minute,
	})
	require.NoError(t, err)
	require.Equal(t, recovery.WorkerHealthStarting, worker.Health(at).State)

	ctx, cancel := context.WithCancel(t.Context())
	done := runRecoveryWorker(ctx, worker)
	awaitSignal(t, started)
	require.Eventually(t, func() bool {
		return worker.Health(at).State == recovery.WorkerHealthHealthy
	}, workerTestWait, time.Millisecond)
	cancel()
	require.NoError(t, awaitWorker(t, done))
	require.Equal(t, recovery.WorkerHealthStopped, worker.Health(at).State)
}

func TestWorkerRecoversAfterTransientSweepFailure(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	sweep, source, rearmer := newDeadlineSweep(t)
	failed := make(chan struct{})
	recovered := make(chan struct{})
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(2)).
		Run(func(context.Context, recovery.DeadlineCursor, int32) { close(failed) }).
		Return(nil, errors.New("temporary database failure")).Once()
	deadline := gameDeadline(at.Add(time.Minute), 2)
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(2)).
		Run(func(context.Context, recovery.DeadlineCursor, int32) { close(recovered) }).
		Return([]recovery.PendingDeadline{deadline}, nil).Once()
	allowEmptyDeadlineSweeps(source)
	rearmer.EXPECT().RearmDeadline(mock.Anything, deadline).Return(true, nil).Once()
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(at).Maybe()
	worker, err := recovery.NewWorker(sweep, clock, recovery.WorkerConfig{
		BatchSize: 2, SweepInterval: 20 * time.Millisecond,
		SweepTimeout: time.Second, StaleAfter: time.Minute,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := runRecoveryWorker(ctx, worker)
	awaitSignal(t, failed)
	require.Eventually(t, func() bool {
		health := worker.Health(at)
		return health.State == recovery.WorkerHealthFailed && health.ConsecutiveFailures == 1
	}, workerTestWait, time.Millisecond)
	awaitSignal(t, recovered)
	require.Eventually(t, func() bool {
		health := worker.Health(at)
		return health.State == recovery.WorkerHealthHealthy && health.ConsecutiveFailures == 0
	}, workerTestWait, time.Millisecond)
	cancel()
	require.NoError(t, awaitWorker(t, done))
}

func TestWorkerHealthBecomesStaleWithoutLeakingFailure(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	sweep, source, _ := newDeadlineSweep(t)
	succeeded := make(chan struct{})
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(1)).
		Run(func(context.Context, recovery.DeadlineCursor, int32) { close(succeeded) }).
		Return([]recovery.PendingDeadline{}, nil).Once()
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(at).Maybe()
	worker, err := recovery.NewWorker(sweep, clock, recovery.WorkerConfig{
		BatchSize: 1, SweepInterval: time.Hour, SweepTimeout: time.Second, StaleAfter: time.Minute,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := runRecoveryWorker(ctx, worker)
	awaitSignal(t, succeeded)
	require.Eventually(t, func() bool {
		return worker.Health(at).Ready
	}, workerTestWait, time.Millisecond)
	health := worker.Health(at.Add(time.Minute + time.Nanosecond))
	require.Equal(t, recovery.WorkerHealthStale, health.State)
	require.False(t, health.Ready)
	require.Zero(t, health.ConsecutiveFailures)
	cancel()
	require.NoError(t, awaitWorker(t, done))
}

func TestWorkerStopsPromptlyDuringStartupSweep(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	sweep, source, _ := newDeadlineSweep(t)
	started := make(chan struct{})
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(1)).
		RunAndReturn(func(ctx context.Context, _ recovery.DeadlineCursor, _ int32) ([]recovery.PendingDeadline, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}).Once()
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(at).Maybe()
	worker, err := recovery.NewWorker(sweep, clock, recovery.WorkerConfig{
		BatchSize: 1, SweepInterval: time.Hour, SweepTimeout: time.Hour, StaleAfter: time.Minute,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := runRecoveryWorker(ctx, worker)
	awaitSignal(t, started)
	cancel()
	require.NoError(t, awaitWorker(t, done))
	require.Equal(t, recovery.WorkerHealthStopped, worker.Health(at).State)
}

func TestWorkerCarriesCursorAcrossBoundedSweeps(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	first := gameDeadline(at.Add(time.Minute), 2)
	second := reconnectDeadline(at.Add(2*time.Minute), 3)
	sweep, source, rearmer := newDeadlineSweep(t)
	firstPage := make(chan struct{})
	secondPage := make(chan struct{})
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(1)).
		Run(func(context.Context, recovery.DeadlineCursor, int32) { close(firstPage) }).
		Return([]recovery.PendingDeadline{first}, nil).Once()
	source.EXPECT().ListPendingDeadlines(mock.Anything, first.Cursor(), int32(1)).
		Run(func(context.Context, recovery.DeadlineCursor, int32) { close(secondPage) }).
		Return([]recovery.PendingDeadline{second}, nil).Once()
	source.EXPECT().ListPendingDeadlines(mock.Anything, second.Cursor(), int32(1)).
		Return([]recovery.PendingDeadline{}, nil).Once()
	completedCycle := make(chan struct{})
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(1)).
		Run(func(context.Context, recovery.DeadlineCursor, int32) { close(completedCycle) }).
		Return([]recovery.PendingDeadline{}, nil).Once()
	rearmer.EXPECT().RearmDeadline(mock.Anything, first).Return(true, nil).Once()
	rearmer.EXPECT().RearmDeadline(mock.Anything, second).Return(true, nil).Once()
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(at).Maybe()
	worker, err := recovery.NewWorker(sweep, clock, recovery.WorkerConfig{
		BatchSize: 1, SweepInterval: 5 * time.Millisecond,
		SweepTimeout: time.Second, StaleAfter: time.Minute,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := runRecoveryWorker(ctx, worker)
	awaitSignal(t, firstPage)
	require.False(t, worker.Health(at).Ready)
	awaitSignal(t, secondPage)
	require.False(t, worker.Health(at).Ready)
	awaitSignal(t, completedCycle)
	require.Eventually(t, func() bool { return worker.Health(at).Ready }, workerTestWait, time.Millisecond)
	cancel()
	require.NoError(t, awaitWorker(t, done))
}

func TestWorkerHealthReportsLastSweepFailureWithoutErrorText(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	sweep, source, _ := newDeadlineSweep(t)
	failed := make(chan struct{})
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(1)).
		Run(func(context.Context, recovery.DeadlineCursor, int32) { close(failed) }).
		Return(nil, errors.New("secret backend detail")).Once()
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(at).Maybe()
	worker, err := recovery.NewWorker(sweep, clock, recovery.WorkerConfig{
		BatchSize: 1, SweepInterval: time.Hour, SweepTimeout: time.Second, StaleAfter: time.Minute,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := runRecoveryWorker(ctx, worker)
	awaitSignal(t, failed)
	require.Eventually(t, func() bool {
		health := worker.Health(at)
		return health.State == recovery.WorkerHealthFailed && !health.Ready && health.LastFailureAt != nil
	}, workerTestWait, time.Millisecond)
	cancel()
	require.NoError(t, awaitWorker(t, done))
}

func TestWorkerHealthIncludesDeadlineHandlerFailure(t *testing.T) {
	t.Parallel()

	at := time.Now().Round(0).UTC()
	sweep, source, _ := newDeadlineSweep(t)
	swept := make(chan struct{})
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(1)).
		Run(func(context.Context, recovery.DeadlineCursor, int32) { close(swept) }).
		Return([]recovery.PendingDeadline{}, nil).Once()
	handler := recoverymocks.NewMockDeadlineHandler(t)
	failed := make(chan struct{})
	deadline := gameDeadline(at.Add(30*time.Millisecond), 2)
	handler.EXPECT().HandleDeadline(mock.Anything, deadline).
		Run(func(context.Context, recovery.PendingDeadline) { close(failed) }).
		Return(false, errors.New("private database failure")).Once()
	scheduler, err := recovery.NewDeadlineScheduler(handler, recovery.DeadlineSchedulerConfig{
		HandlerRetryInterval: time.Hour,
	})
	require.NoError(t, err)
	clock := recoverymocks.NewMockClock(t)
	clock.EXPECT().Now().Return(at).Maybe()
	worker, err := recovery.NewWorker(sweep, clock, recovery.WorkerConfig{
		BatchSize: 1, SweepInterval: time.Hour, SweepTimeout: time.Second, StaleAfter: time.Minute,
	}, scheduler)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	schedulerDone := runDeadlineScheduler(ctx, scheduler)
	workerDone := runRecoveryWorker(ctx, worker)
	awaitSignal(t, swept)
	require.Eventually(t, func() bool { return worker.Health(at).Ready }, workerTestWait, time.Millisecond)
	changed, err := armDeadlineEventually(t, scheduler, deadline)
	require.NoError(t, err)
	require.True(t, changed)
	awaitSignal(t, failed)
	require.Eventually(t, func() bool {
		health := worker.Health(at)
		return health.State == recovery.WorkerHealthFailed && !health.Ready &&
			health.ConsecutiveFailures == 1 && health.LastFailureAt != nil
	}, workerTestWait, time.Millisecond)
	cancel()
	require.NoError(t, awaitWorker(t, workerDone))
	require.NoError(t, awaitScheduler(t, schedulerDone))
}

func runRecoveryWorker(ctx context.Context, worker *recovery.Worker) <-chan error {
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	return done
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(workerTestWait):
		t.Fatal("timed out waiting for recovery worker")
	}
}

func awaitWorker(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(workerTestWait):
		t.Fatal("recovery worker did not stop")
		return nil
	}
}

func TestWorkerRejectsConcurrentRun(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	sweep, source, _ := newDeadlineSweep(t)
	blocked := make(chan struct{})
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(1)).
		RunAndReturn(func(ctx context.Context, _ recovery.DeadlineCursor, _ int32) ([]recovery.PendingDeadline, error) {
			close(blocked)
			<-ctx.Done()
			return nil, ctx.Err()
		}).Once()
	clock := recoverymocks.NewMockClock(t)
	var now atomic.Int64
	now.Store(at.UnixNano())
	clock.EXPECT().Now().RunAndReturn(func() time.Time {
		return time.Unix(0, now.Load()).UTC()
	}).Maybe()
	worker, err := recovery.NewWorker(sweep, clock, recovery.WorkerConfig{
		BatchSize: 1, SweepInterval: time.Hour, SweepTimeout: time.Hour, StaleAfter: time.Minute,
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := runRecoveryWorker(ctx, worker)
	awaitSignal(t, blocked)
	require.ErrorIs(t, worker.Run(t.Context()), recovery.ErrWorkerRunning)
	cancel()
	require.NoError(t, awaitWorker(t, done))
}
