package connection

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
)

func TestReaperRunsInitialScanBeforeReadyAndStopsOnContext(t *testing.T) {
	h := newLifecycleHarness(t)
	h.recovery = &recordingRecoveryRepository{
		candidates: []OrphanedConnectionLease{{Lease: h.lease, Revision: 1, Authority: h.resolved.Authority}},
		result:     CloseConnectionResult{Lease: h.lease, Closed: false, ActiveLeaseCount: 1, Action: ResolvedAction{Kind: ActionNone}},
	}
	coordinator, err := NewCoordinator(Dependencies{
		Transactions: h.tx, Authority: h.provider, Repository: h.repo, Recovery: h.recovery,
		Readiness: h.readiness, PausedPresence: h.paused, Disconnect: h.disconnect,
		Reconnect: h.reconnect, TerminalAdvancer: h.terminal, Clock: h.clock,
		Config: Config{ReconnectDuration: time.Second},
	})
	require.NoError(t, err)
	reaper, err := NewReaper(coordinator, h.recovery, h.provider, ReaperConfig{Interval: time.Hour, BatchSize: 8})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reaper.Run(ctx) }()
	require.Eventually(t, reaper.Ready, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	require.False(t, reaper.Ready())
}

func TestReaperLeavesLiveForeignAuthorityForItsOwner(t *testing.T) {
	h := newLifecycleHarness(t)
	h.provider.err = authorityusecase.ErrNotOwner
	h.recovery = &recordingRecoveryRepository{
		candidates: []OrphanedConnectionLease{{Lease: h.lease, Revision: 1, Authority: h.resolved.Authority}},
	}
	coordinator, err := NewCoordinator(Dependencies{
		Transactions: h.tx, Authority: h.provider, Repository: h.repo, Recovery: h.recovery,
		Clock: h.clock, Config: Config{ReconnectDuration: time.Second},
	})
	require.NoError(t, err)
	reaper, err := NewReaper(coordinator, h.recovery, h.provider, ReaperConfig{Interval: time.Hour, BatchSize: 1})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reaper.Run(ctx) }()
	require.Eventually(t, reaper.Ready, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-done)
	// The foreign owner is evidence-only; the repository close is never called.
	require.Equal(t, 1, h.tx.count())
	require.Len(t, h.recovery.candidates, 1)
}

func TestReaperRenewsLiveConnectionAuthorityBeforeLookingForOrphans(t *testing.T) {
	h := newLifecycleHarness(t)
	h.recovery = &recordingRecoveryRepository{
		tournaments: []uuid.UUID{h.resolved.TournamentID},
	}
	coordinator, err := NewCoordinator(Dependencies{
		Transactions: h.tx, Authority: h.provider, Repository: h.repo, Recovery: h.recovery,
		Clock: h.clock, Config: Config{ReconnectDuration: time.Second},
	})
	require.NoError(t, err)
	reaper, err := NewReaper(coordinator, h.recovery, h.provider, ReaperConfig{Interval: time.Hour, BatchSize: 1})
	require.NoError(t, err)

	require.NoError(t, reaper.runScan(context.Background()))
	h.provider.mu.Lock()
	recoveryCalls := append([]uuid.UUID(nil), h.provider.recoveryCalls...)
	h.provider.mu.Unlock()
	require.Equal(t, []uuid.UUID{h.resolved.TournamentID}, recoveryCalls)
}
