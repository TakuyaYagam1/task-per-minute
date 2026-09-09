package task

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAvailabilityMonitorRecordsDurableScanFailureAndRecovery(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 17, 0, 0, 0, time.UTC)
	var attempts atomic.Int32
	monitor, err := NewAvailabilityMonitor(BacklogSourceFunc(func(context.Context) (BacklogSnapshot, error) {
		if attempts.Add(1) == 1 {
			return BacklogSnapshot{}, errors.New("database unavailable")
		}
		return BacklogSnapshot{}, nil
	}), AvailabilityMonitorConfig{Now: func() time.Time { return now }})
	require.NoError(t, err)

	_, err = monitor.Process(t.Context())
	require.ErrorIs(t, err, ErrBacklogUnavailable)
	failed := monitor.Health(now)
	require.Equal(t, 1, failed.ConsecutiveFailures)
	require.Equal(t, now, *failed.LastFailureAt)
	require.Nil(t, failed.LastSuccessAt)

	_, err = monitor.Process(t.Context())
	require.NoError(t, err)
	recovered := monitor.Health(now)
	require.Zero(t, recovered.ConsecutiveFailures)
	require.Equal(t, now, *recovered.LastSuccessAt)
}

func TestAvailabilityMonitorBecomesReadyAfterDurableScanAndStopsWithContext(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 17, 5, 0, 0, time.UTC)
	scanned := make(chan struct{})
	monitor, err := NewAvailabilityMonitor(BacklogSourceFunc(func(context.Context) (BacklogSnapshot, error) {
		select {
		case <-scanned:
		default:
			close(scanned)
		}
		return BacklogSnapshot{}, nil
	}), AvailabilityMonitorConfig{
		PollInterval: time.Hour,
		Now:          func() time.Time { return now },
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- monitor.Run(ctx) }()
	<-scanned
	// The source returning only establishes that the durable scan has completed.
	// Process records the resulting health snapshot immediately before it returns,
	// so wait for that observable state instead of assuming a goroutine schedule.
	require.Eventually(t, monitor.Ready, time.Second, time.Millisecond)

	cancel()
	require.NoError(t, <-done)
	require.False(t, monitor.Health(now).Running)
}

func TestAvailabilityMonitorRejectsInvalidDurableBacklog(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 17, 10, 0, 0, time.UTC)
	monitor, err := NewAvailabilityMonitor(BacklogSourceFunc(func(context.Context) (BacklogSnapshot, error) {
		return BacklogSnapshot{PendingCount: 1}, nil
	}), AvailabilityMonitorConfig{Now: func() time.Time { return now }})
	require.NoError(t, err)

	_, err = monitor.Process(t.Context())
	require.ErrorIs(t, err, ErrInvalidBacklog)
	health := monitor.Health(now)
	require.Equal(t, 1, health.ConsecutiveFailures)
	require.Equal(t, now, *health.LastFailureAt)
}

func TestAvailabilityMonitorBoundsDurableBacklogScan(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 17, 12, 0, 0, time.UTC)
	monitor, err := NewAvailabilityMonitor(BacklogSourceFunc(func(ctx context.Context) (BacklogSnapshot, error) {
		deadline, bounded := ctx.Deadline()
		require.True(t, bounded)
		require.False(t, deadline.IsZero())
		return BacklogSnapshot{}, nil
	}), AvailabilityMonitorConfig{
		ScanTimeout: 25 * time.Millisecond,
		Now:         func() time.Time { return now },
	})
	require.NoError(t, err)

	_, err = monitor.Process(t.Context())
	require.NoError(t, err)
}

func TestBacklogSnapshotDoesNotExposePrivateDeliveryIdentity(t *testing.T) {
	t.Parallel()

	deadline := time.Date(2026, time.September, 7, 17, 15, 0, 0, time.UTC)
	require.NoError(t, (BacklogSnapshot{PendingCount: 1, OldestPendingAt: &deadline}).Validate())
	require.ErrorIs(t, (BacklogSnapshot{PendingCount: 0, OldestPendingAt: &deadline}).Validate(), ErrInvalidBacklog)
	require.ErrorIs(t, (BacklogSnapshot{PendingCount: 1}).Validate(), ErrInvalidBacklog)
}
