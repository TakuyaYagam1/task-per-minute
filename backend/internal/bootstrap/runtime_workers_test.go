package bootstrap

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestRuntimeWorkersStartInOrderAndStopWithContext(t *testing.T) {
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	first := blockingRuntimeWorker(t, firstStarted)
	second := blockingRuntimeWorker(t, secondStarted)
	workers, err := newRuntimeWorkers(
		namedRuntimeWorker{name: "first", worker: first, ready: channelClosed(firstStarted)},
		namedRuntimeWorker{name: "second", worker: second, ready: channelClosed(secondStarted)},
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	errorsCh, err := workers.Start(ctx)
	require.NoError(t, err)
	require.NotNil(t, errorsCh)
	require.Eventually(t, channelClosed(secondStarted), time.Second, time.Millisecond)

	cancel()
	require.NoError(t, workers.Wait(t.Context()))
}

func TestRuntimeWorkersPropagateUnexpectedStop(t *testing.T) {
	workerErr := errors.New("worker failed")
	worker := NewMockRuntimeWorker(t)
	worker.EXPECT().Run(mock.Anything).Return(workerErr).Once()
	workers, err := newRuntimeWorkers(namedRuntimeWorker{
		name: "failing", worker: worker, ready: func() bool { return false },
	})
	require.NoError(t, err)

	_, err = workers.Start(t.Context())
	require.ErrorIs(t, err, workerErr)
	require.ErrorContains(t, err, "runtime worker failing")
	require.Equal(t, runtimeWorkerStateFailed, workers.WorkerHealth("failing").State)
	require.NoError(t, workers.Wait(t.Context()))
}

func TestRuntimeWorkersConvertWorkerPanicToStartupFailure(t *testing.T) {
	worker := NewMockRuntimeWorker(t)
	worker.EXPECT().Run(mock.Anything).RunAndReturn(func(context.Context) error {
		panic("worker panic")
	}).Once()
	workers, err := newRuntimeWorkers(namedRuntimeWorker{
		name: "panicking", worker: worker, ready: func() bool { return false },
	})
	require.NoError(t, err)

	_, err = workers.Start(t.Context())

	require.ErrorContains(t, err, "runtime worker panicking")
	require.ErrorContains(t, err, "panic")
	require.Equal(t, runtimeWorkerStateFailed, workers.WorkerHealth("panicking").State)
	require.NoError(t, workers.Wait(t.Context()))
}

func TestRuntimeWorkersReportCreatedHealthyStaleAndStoppedStates(t *testing.T) {
	var ready atomic.Bool
	ready.Store(true)
	started := make(chan struct{})
	worker := blockingRuntimeWorker(t, started)
	workers, err := newRuntimeWorkers(namedRuntimeWorker{
		name: "delivery", worker: worker, ready: ready.Load,
	})
	require.NoError(t, err)
	require.Equal(t, runtimeWorkerStateCreated, workers.WorkerHealth("delivery").State)

	ctx, cancel := context.WithCancel(t.Context())
	_, err = workers.Start(ctx)
	require.NoError(t, err)
	require.Eventually(t, channelClosed(started), time.Second, time.Millisecond)
	require.Equal(t, runtimeWorkerStateHealthy, workers.WorkerHealth("delivery").State)

	ready.Store(false)
	require.Equal(t, runtimeWorkerStateStale, workers.WorkerHealth("delivery").State)

	cancel()
	require.NoError(t, workers.Wait(t.Context()))
	require.Equal(t, runtimeWorkerStateStopped, workers.WorkerHealth("delivery").State)
}

func TestRuntimeWorkersRecordSharedHeartbeatAfterLocalReadiness(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	recorded := make(chan observability.RuntimeWorkerHeartbeat, 1)
	worker := blockingRuntimeWorker(t, started)
	workers, err := newRuntimeWorkers(namedRuntimeWorker{
		name: "event-delivery", worker: worker, ready: channelClosed(started),
	})
	require.NoError(t, err)
	now := time.Date(2026, time.September, 7, 16, 0, 0, 0, time.UTC)
	instanceID := uuid.MustParse("23000000-0000-4000-8000-000000000001")
	workers.configureRuntimeWorkerHeartbeats(
		runtimeWorkerHeartbeatReporterFunc(func(_ context.Context, heartbeat observability.RuntimeWorkerHeartbeat) error {
			recorded <- heartbeat
			return nil
		}),
		instanceID,
		clockFunc(func() time.Time { return now }),
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err = workers.Start(ctx)
	require.NoError(t, err)
	require.Equal(t, observability.RuntimeWorkerHeartbeat{
		InstanceID: instanceID, Worker: "event-delivery", ObservedAt: now,
	}, <-recorded)

	cancel()
	require.NoError(t, workers.Wait(t.Context()))
}

func TestRuntimeWorkersFailStartupWhenSharedHeartbeatCannotBeRecorded(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	worker := blockingRuntimeWorker(t, started)
	workers, err := newRuntimeWorkers(namedRuntimeWorker{
		name: "event-delivery", worker: worker, ready: channelClosed(started),
	})
	require.NoError(t, err)
	workers.configureRuntimeWorkerHeartbeats(
		runtimeWorkerHeartbeatReporterFunc(func(context.Context, observability.RuntimeWorkerHeartbeat) error {
			return errors.New("shared store unavailable")
		}),
		uuid.MustParse("23000000-0000-4000-8000-000000000002"),
		clockFunc(func() time.Time { return time.Date(2026, time.September, 7, 16, 0, 0, 0, time.UTC) }),
	)
	ctx, cancel := context.WithCancel(t.Context())

	_, err = workers.Start(ctx)
	require.ErrorContains(t, err, "heartbeat startup")
	require.Equal(t, runtimeWorkerStateFailed, workers.WorkerHealth("event-delivery").State)
	cancel()
	require.NoError(t, workers.Wait(t.Context()))
}

func TestRuntimeWorkersFailOnPeriodicSharedHeartbeatError(t *testing.T) {
	t.Parallel()

	const workerName = "event-delivery"
	heartbeatErr := errors.New("shared store unavailable")
	started := make(chan struct{})
	ticks := make(chan time.Time, 1)
	tickerStopped := make(chan struct{})
	periodicReport := make(chan struct{})
	worker := blockingRuntimeWorker(t, started)
	workers, err := newRuntimeWorkers(namedRuntimeWorker{
		name: workerName, worker: worker, ready: channelClosed(started),
	})
	require.NoError(t, err)

	var reports atomic.Int32
	workers.configureRuntimeWorkerHeartbeats(
		runtimeWorkerHeartbeatReporterFunc(func(context.Context, observability.RuntimeWorkerHeartbeat) error {
			if reports.Add(1) == 1 {
				return nil
			}
			close(periodicReport)
			return heartbeatErr
		}),
		uuid.MustParse("23000000-0000-4000-8000-000000000003"),
		clockFunc(func() time.Time { return time.Date(2026, time.September, 7, 16, 0, 0, 0, time.UTC) }),
	)
	workers.configureRuntimeWorkerHeartbeatTicker(func(time.Duration) (<-chan time.Time, func()) {
		return ticks, func() { close(tickerStopped) }
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errorsCh, err := workers.Start(ctx)
	require.NoError(t, err)
	ticks <- time.Date(2026, time.September, 7, 16, 0, 5, 0, time.UTC)

	select {
	case <-periodicReport:
	case <-time.After(time.Second):
		t.Fatal("periodic heartbeat was not reported")
	}
	select {
	case reportErr := <-errorsCh:
		require.ErrorIs(t, reportErr, heartbeatErr)
	case <-time.After(time.Second):
		t.Fatal("periodic heartbeat error was not emitted")
	}
	select {
	case <-tickerStopped:
	case <-time.After(time.Second):
		t.Fatal("heartbeat ticker was not stopped after failure")
	}

	require.Equal(t, runtimeWorkerStateFailed, workers.WorkerHealth(workerName).State)
	require.EqualValues(t, 2, reports.Load())
	select {
	case unexpected := <-errorsCh:
		t.Fatalf("unexpected second heartbeat error: %v", unexpected)
	default:
	}

	cancel()
	require.NoError(t, workers.Wait(t.Context()))
}

func TestRuntimeWorkersPeriodicHeartbeatErrorStopsOnContextCancelWhenErrorChannelIsBlocked(t *testing.T) {
	t.Parallel()

	const workerName = "deadline-recovery"
	heartbeatErr := errors.New("shared store unavailable")
	ticks := make(chan time.Time, 1)
	tickerStopped := make(chan struct{})
	periodicReport := make(chan struct{})
	worker := NewMockRuntimeWorker(t)
	workers, err := newRuntimeWorkers(namedRuntimeWorker{name: workerName, worker: worker})
	require.NoError(t, err)
	workers.setState(workerName, runtimeWorkerStateHealthy)

	var reports atomic.Int32
	workers.configureRuntimeWorkerHeartbeats(
		runtimeWorkerHeartbeatReporterFunc(func(context.Context, observability.RuntimeWorkerHeartbeat) error {
			if reports.Add(1) == 1 {
				return nil
			}
			close(periodicReport)
			return heartbeatErr
		}),
		uuid.MustParse("23000000-0000-4000-8000-000000000004"),
		clockFunc(func() time.Time { return time.Date(2026, time.September, 7, 16, 0, 0, 0, time.UTC) }),
	)
	workers.configureRuntimeWorkerHeartbeatTicker(func(time.Duration) (<-chan time.Time, func()) {
		return ticks, func() { close(tickerStopped) }
	})

	ctx, cancel := context.WithCancel(t.Context())
	require.NoError(t, workers.startWorkerHeartbeat(ctx, make(chan error), namedRuntimeWorker{name: workerName}))
	ticks <- time.Date(2026, time.September, 7, 16, 0, 5, 0, time.UTC)
	select {
	case <-periodicReport:
	case <-time.After(time.Second):
		t.Fatal("periodic heartbeat was not reported")
	}

	cancel()
	done := make(chan struct{})
	go func() {
		workers.heartbeatWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked heartbeat error did not stop after context cancellation")
	}
	select {
	case <-tickerStopped:
	case <-time.After(time.Second):
		t.Fatal("heartbeat ticker was not stopped after context cancellation")
	}

	require.Equal(t, runtimeWorkerStateFailed, workers.WorkerHealth(workerName).State)
	require.EqualValues(t, 2, reports.Load())
}

func TestRuntimeWorkersKeepOtherStartupStateWhenEarlierWorkerFails(t *testing.T) {
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	allowFirstFailure := make(chan struct{})
	firstFailure := errors.New("first worker failed")
	first := NewMockRuntimeWorker(t)
	first.EXPECT().Run(mock.Anything).RunAndReturn(func(ctx context.Context) error {
		close(firstStarted)
		select {
		case <-allowFirstFailure:
			return firstFailure
		case <-ctx.Done():
			return nil
		}
	}).Once()
	second := blockingRuntimeWorker(t, secondStarted)
	workers, err := newRuntimeWorkers(
		namedRuntimeWorker{name: "first", worker: first, ready: channelClosed(firstStarted)},
		namedRuntimeWorker{name: "second", worker: second, ready: func() bool { return false }},
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, startErr := workers.Start(ctx)
		result <- startErr
	}()
	require.Eventually(t, channelClosed(secondStarted), time.Second, time.Millisecond)

	close(allowFirstFailure)
	err = <-result
	require.ErrorIs(t, err, firstFailure)
	require.Equal(t, runtimeWorkerStateFailed, workers.WorkerHealth("first").State)
	require.Equal(t, runtimeWorkerStateStarting, workers.WorkerHealth("second").State)

	cancel()
	require.NoError(t, workers.Wait(t.Context()))
}

func TestRuntimeWorkersRejectInvalidGraphAndDuplicateStart(t *testing.T) {
	_, err := newRuntimeWorkers(namedRuntimeWorker{})
	require.ErrorContains(t, err, "invalid entry")

	worker := NewMockRuntimeWorker(t)
	_, err = newRuntimeWorkers(
		namedRuntimeWorker{name: "duplicate", worker: worker},
		namedRuntimeWorker{name: "duplicate", worker: worker},
	)
	require.ErrorContains(t, err, "duplicate name")

	workers, err := newRuntimeWorkers()
	require.NoError(t, err)
	_, err = workers.Start(t.Context())
	require.NoError(t, err)
	_, err = workers.Start(t.Context())
	require.ErrorContains(t, err, "already started")
}

func blockingRuntimeWorker(t *testing.T, started chan struct{}) *MockRuntimeWorker {
	t.Helper()
	worker := NewMockRuntimeWorker(t)
	worker.EXPECT().Run(mock.Anything).RunAndReturn(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	}).Once()
	return worker
}

func channelClosed(channel <-chan struct{}) func() bool {
	return func() bool {
		select {
		case <-channel:
			return true
		default:
			return false
		}
	}
}
