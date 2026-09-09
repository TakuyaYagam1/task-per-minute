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

func TestDeadlineSchedulerDeduplicatesAndFencesArms(t *testing.T) {
	t.Parallel()

	handler := recoverymocks.NewMockDeadlineHandler(t)
	scheduler, err := recovery.NewDeadlineScheduler(handler)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := runDeadlineScheduler(ctx, scheduler)

	deadline := gameDeadline(time.Now().UTC().Add(time.Hour), 4)
	changed, err := armDeadlineEventually(t, scheduler, deadline)
	require.NoError(t, err)
	require.True(t, changed)

	changed, err = scheduler.ArmDeadline(t.Context(), deadline)
	require.NoError(t, err)
	require.False(t, changed)

	older := deadline
	older.ExpectedRevision--
	changed, err = scheduler.ArmDeadline(t.Context(), older)
	require.NoError(t, err)
	require.False(t, changed)

	conflict := deadline
	conflict.DueAt = conflict.DueAt.Add(time.Second)
	_, err = scheduler.ArmDeadline(t.Context(), conflict)
	require.ErrorIs(t, err, recovery.ErrDeadlineArmConflict)

	newer := conflict
	newer.ExpectedRevision++
	changed, err = scheduler.ArmDeadline(t.Context(), newer)
	require.NoError(t, err)
	require.True(t, changed)

	cancel()
	require.NoError(t, awaitScheduler(t, done))
	_, err = scheduler.ArmDeadline(t.Context(), newer)
	require.ErrorIs(t, err, recovery.ErrSchedulerNotRunning)
}

func TestDeadlineSchedulerExecutesDueDeadlineOnce(t *testing.T) {
	t.Parallel()

	handler := recoverymocks.NewMockDeadlineHandler(t)
	handled := make(chan struct{})
	deadline := reconnectDeadline(time.Now().UTC().Add(40*time.Millisecond), 9)
	handler.EXPECT().HandleDeadline(mock.Anything, deadline).
		Run(func(context.Context, recovery.PendingDeadline) { close(handled) }).
		Return(true, nil).Once()
	scheduler, err := recovery.NewDeadlineScheduler(handler)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := runDeadlineScheduler(ctx, scheduler)

	changed, err := armDeadlineEventually(t, scheduler, deadline)
	require.NoError(t, err)
	require.True(t, changed)
	awaitSignal(t, handled)
	cancel()
	require.NoError(t, awaitScheduler(t, done))
}

func TestDeadlineSchedulerAllowsRetryAfterHandlerFailure(t *testing.T) {
	t.Parallel()

	handler := recoverymocks.NewMockDeadlineHandler(t)
	firstAttempt := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseSecond := make(chan struct{})
	secondAttempt := make(chan struct{})
	var attempts atomic.Int32
	deadline := reconnectDeadline(time.Now().UTC().Add(40*time.Millisecond), 9)
	handler.EXPECT().HandleDeadline(mock.Anything, deadline).
		RunAndReturn(func(context.Context, recovery.PendingDeadline) (bool, error) {
			if attempts.Add(1) == 1 {
				close(firstAttempt)
				return false, errors.New("temporary handler failure")
			}
			close(secondStarted)
			<-releaseSecond
			close(secondAttempt)
			return true, nil
		}).Twice()
	scheduler, err := recovery.NewDeadlineScheduler(handler, recovery.DeadlineSchedulerConfig{
		HandlerRetryInterval: 20 * time.Millisecond,
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := runDeadlineScheduler(ctx, scheduler)

	changed, err := armDeadlineEventually(t, scheduler, deadline)
	require.NoError(t, err)
	require.True(t, changed)
	awaitSignal(t, firstAttempt)
	require.Eventually(t, func() bool {
		health := scheduler.ExecutionHealth()
		return health.ConsecutiveFailures == 1 && health.LastFailureAt != nil
	}, workerTestWait, time.Millisecond)
	awaitSignal(t, secondStarted)
	close(releaseSecond)
	awaitSignal(t, secondAttempt)
	require.Eventually(t, func() bool {
		health := scheduler.ExecutionHealth()
		return health.ConsecutiveFailures == 0 && health.LastSuccessAt != nil
	}, workerTestWait, time.Millisecond)
	cancel()
	require.NoError(t, awaitScheduler(t, done))
}

func runDeadlineScheduler(ctx context.Context, scheduler *recovery.DeadlineScheduler) <-chan error {
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	return done
}

func armDeadlineEventually(
	t *testing.T,
	scheduler *recovery.DeadlineScheduler,
	deadline recovery.PendingDeadline,
) (bool, error) {
	t.Helper()
	var changed bool
	var err error
	require.Eventually(t, func() bool {
		changed, err = scheduler.ArmDeadline(t.Context(), deadline)
		return err == nil
	}, workerTestWait, time.Millisecond)
	return changed, err
}

func awaitScheduler(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(workerTestWait):
		t.Fatal("recovery deadline scheduler did not stop")
		return nil
	}
}
