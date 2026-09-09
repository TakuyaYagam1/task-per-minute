package game_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
)

func TestRecoveryRunnerReadinessRequiresCompletedInitialScan(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	lease := recoveryLease(now, 1)
	listed := make(chan struct{})
	release := make(chan struct{})
	tournaments := gamemocks.NewMockRecoveryTournamentSource(t)
	tournaments.EXPECT().ListRecoveryTournaments(mock.Anything).
		RunAndReturn(func(ctx context.Context) ([]uuid.UUID, error) {
			close(listed)
			<-release
			return []uuid.UUID{lease.TournamentID}, nil
		}).Once()
	authority := gamemocks.NewMockRecoveryAuthorityProvider(t)
	authority.EXPECT().RecoveryAuthorityFor(mock.Anything, lease.TournamentID).
		Return(lease.Identity(), true, nil).Once()
	reader := newAuthorityReaderMock(t, &lease, nil)
	source := newExecutionRecoverySourceHarness(t, nil, nil)
	rearmer := newDeadlineRearmerHarness(t, lease, now)
	replayer := newEpochReplayerHarness(t, nil)
	clock := newRecoveryClock(t, now, 1)
	recoverer := gameusecase.NewRecoverer(
		reader, source.source, rearmer.rearmer, replayer.replayer, newRecoveryAuthorityTime(t, now, 1),
	)
	runner, err := gameusecase.NewRecoveryRunner(
		tournaments,
		authority,
		recoverer,
		clock,
		gameusecase.RecoveryRunnerConfig{Interval: time.Hour},
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errs := make(chan error, 1)
	go func() { errs <- runner.Run(ctx) }()
	<-listed
	require.False(t, runner.Ready())

	close(release)
	require.Eventually(t, runner.Ready, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-errs)
}

func TestRecoveryRunnerCompletesInitialScanForPausedStaleEpoch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 6, 10, 2, 0, 0, time.UTC)
	lease := recoveryLease(now, 2)
	stale := *lease.Previous
	candidate := recoveryCandidate(now, &stale, domain.GameStatePaused)
	tournaments := gamemocks.NewMockRecoveryTournamentSource(t)
	tournaments.EXPECT().ListRecoveryTournaments(mock.Anything).
		Return([]uuid.UUID{lease.TournamentID}, nil).Once()
	authority := gamemocks.NewMockRecoveryAuthorityProvider(t)
	authority.EXPECT().RecoveryAuthorityFor(mock.Anything, lease.TournamentID).
		Return(lease.Identity(), true, nil).Once()
	reader := newAuthorityReaderMock(t, &lease, nil)
	source := newExecutionRecoverySourceHarness(t, []gameusecase.RecoveryCandidate{candidate}, nil)
	rearmer := newDeadlineRearmerHarness(t, lease, now)
	replayer := newEpochReplayerHarness(t, nil)
	clock := newRecoveryClock(t, now, 1)
	recoverer := gameusecase.NewRecoverer(
		reader, source.source, rearmer.rearmer, replayer.replayer, newRecoveryAuthorityTime(t, now, 1),
	)
	runner, err := gameusecase.NewRecoveryRunner(
		tournaments,
		authority,
		recoverer,
		clock,
		gameusecase.RecoveryRunnerConfig{Interval: time.Hour},
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	errs := make(chan error, 1)
	go func() { errs <- runner.Run(ctx) }()
	require.Eventually(t, runner.Ready, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-errs)
	health := runner.Health()
	require.True(t, health.InitialScanComplete)
	require.Nil(t, health.LastFailureAt)
	require.Empty(t, rearmer.armsSnapshot())
	require.Equal(t, 0, replayer.writeCount())
}

func TestRecoveryRunnerEmitsOneTerminalEventAfterRearm(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 13, 0, 0, 0, time.UTC)
	lease := recoveryLease(now, 3)
	candidate := recoveryCandidate(now, lease.Stamp(), domain.GameStateActive)
	tournaments := gamemocks.NewMockRecoveryTournamentSource(t)
	tournaments.EXPECT().ListRecoveryTournaments(mock.Anything).
		Return([]uuid.UUID{lease.TournamentID}, nil).Once()
	authority := gamemocks.NewMockRecoveryAuthorityProvider(t)
	authority.EXPECT().RecoveryAuthorityFor(mock.Anything, lease.TournamentID).
		Return(lease.Identity(), true, nil).Once()
	reader := newAuthorityReaderMock(t, &lease, nil)
	source := newExecutionRecoverySourceHarness(t, []gameusecase.RecoveryCandidate{candidate}, nil)
	rearmer := newDeadlineRearmerHarness(t, lease, now)
	replayer := newEpochReplayerHarness(t, nil)
	events := make(chan gameusecase.RecoveryEvent, 1)
	observer := gamemocks.NewMockRecoveryObserver(t)
	observer.EXPECT().ObserveExecutionRecovery(mock.Anything, mock.Anything).
		Run(func(_ context.Context, event gameusecase.RecoveryEvent) { events <- event }).Once()
	runner, err := gameusecase.NewRecoveryRunner(
		tournaments,
		authority,
		gameusecase.NewRecoverer(
			reader,
			source.source,
			rearmer.rearmer,
			replayer.replayer,
			newRecoveryAuthorityTime(t, now, 1),
		),
		newRecoveryClock(t, now, 1),
		gameusecase.RecoveryRunnerConfig{
			Interval: time.Hour,
			Observer: observer,
		},
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errs := make(chan error, 1)
	go func() { errs <- runner.Run(ctx) }()

	select {
	case event := <-events:
		require.Equal(t, gameusecase.RecoveryEvent{
			TournamentID: lease.TournamentID,
			Outcome:      gameusecase.RecoveryOutcomeSuccess,
			Transition:   "scan_completed",
			ReasonCode:   "deadline_rearmed",
			Revision:     lease.Epoch,
		}, event)
	case <-time.After(time.Second):
		t.Fatal("recovery runner did not emit a terminal event")
	}
	cancel()
	require.NoError(t, <-errs)
}

func TestRecoveryRunnerRestoresReadinessAfterPeriodicFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 6, 10, 3, 0, 0, time.UTC)
	lease := recoveryLease(now, 1)
	periodicFailure := errors.New("temporary database outage")
	initialScan := make(chan struct{})
	allowPeriodicFailure := make(chan struct{})
	failureObserved := make(chan struct{})
	allowRecovery := make(chan struct{})
	var scans atomic.Int32
	tournaments := gamemocks.NewMockRecoveryTournamentSource(t)
	tournaments.EXPECT().ListRecoveryTournaments(mock.Anything).
		RunAndReturn(func(ctx context.Context) ([]uuid.UUID, error) {
			switch scans.Add(1) {
			case 1:
				close(initialScan)
				return []uuid.UUID{lease.TournamentID}, nil
			case 2:
				select {
				case <-allowPeriodicFailure:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				close(failureObserved)
				return nil, periodicFailure
			case 3:
				select {
				case <-allowRecovery:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return []uuid.UUID{lease.TournamentID}, nil
			default:
				<-ctx.Done()
				return nil, ctx.Err()
			}
		}).Maybe()
	authority := gamemocks.NewMockRecoveryAuthorityProvider(t)
	authority.EXPECT().RecoveryAuthorityFor(mock.Anything, lease.TournamentID).
		Return(lease.Identity(), true, nil).Twice()
	reader := newAuthorityReaderMock(t, &lease, nil, 2)
	source := newExecutionRecoverySourceHarness(t, nil, nil)
	rearmer := newDeadlineRearmerHarness(t, lease, now)
	replayer := newEpochReplayerHarness(t, nil)
	clock := newRecoveryClock(t, now, 0)
	recoverer := gameusecase.NewRecoverer(
		reader, source.source, rearmer.rearmer, replayer.replayer, newRecoveryAuthorityTime(t, now, 2),
	)
	runner, err := gameusecase.NewRecoveryRunner(
		tournaments,
		authority,
		recoverer,
		clock,
		gameusecase.RecoveryRunnerConfig{Interval: time.Millisecond},
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errs := make(chan error, 1)
	go func() { errs <- runner.Run(ctx) }()
	<-initialScan
	require.Eventually(t, runner.Ready, time.Second, time.Millisecond)
	close(allowPeriodicFailure)
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	select {
	case <-failureObserved:
	case <-timeout.C:
		t.Fatal("periodic scan did not report its dependency failure")
	}
	require.Eventually(t, func() bool { return !runner.Ready() }, time.Second, time.Millisecond)
	select {
	case err := <-errs:
		t.Fatalf("periodic recovery worker stopped: %v", err)
	default:
	}

	close(allowRecovery)
	require.Eventually(t, runner.Ready, time.Second, time.Millisecond)
	cancel()
	require.Eventually(t, func() bool {
		select {
		case err := <-errs:
			require.NoError(t, err)
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
}

func TestRecoveryRunnerPrevalidatesBatchBeforeAuthoritySideEffects(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 6, 10, 4, 0, 0, time.UTC)
	first := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	tournaments := gamemocks.NewMockRecoveryTournamentSource(t)
	tournaments.EXPECT().ListRecoveryTournaments(mock.Anything).
		Return([]uuid.UUID{first, second}, nil).Once()
	authority := gamemocks.NewMockRecoveryAuthorityProvider(t)
	clock := newRecoveryClock(t, now, 1)
	runner, err := gameusecase.NewRecoveryRunner(
		tournaments,
		authority,
		gameusecase.NewRecoverer(nil, nil, nil, nil, nil),
		clock,
		gameusecase.RecoveryRunnerConfig{Interval: time.Hour},
	)
	require.NoError(t, err)

	err = runner.Run(t.Context())
	require.ErrorIs(t, err, gameusecase.ErrInvalidRecovery)
	require.False(t, runner.Ready())
	require.NotErrorIs(t, err, context.Canceled)
}
