package recovery_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	attemptusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/attempt"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/recovery/mocks"
	recoveryusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/recovery"
)

func newAuthorityReaderMock(
	t *testing.T,
	lease *authoritydomain.Lease,
	err error,
	calls ...int,
) *gamemocks.MockRecoveryAuthorityReader {
	t.Helper()
	wantCalls := 1
	if len(calls) > 0 {
		wantCalls = calls[0]
	}
	reader := gamemocks.NewMockRecoveryAuthorityReader(t)
	reader.EXPECT().LoadAuthority(mock.Anything, mock.Anything).
		RunAndReturn(func(
			context.Context,
			uuid.UUID,
		) (*authoritydomain.Lease, error) {
			if err != nil {
				return nil, err
			}
			if lease == nil {
				return nil, nil
			}
			clone := lease.Clone()
			return &clone, nil
		}).Times(wantCalls)
	return reader
}

type executionRecoverySourceHarness struct {
	source     *gamemocks.MockRecoverySource
	mu         sync.Mutex
	candidates []recoveryusecase.RecoveryCandidate
	loads      int
	err        error
}

func newExecutionRecoverySourceHarness(
	t *testing.T,
	candidates []recoveryusecase.RecoveryCandidate,
	err error,
) *executionRecoverySourceHarness {
	t.Helper()
	harness := &executionRecoverySourceHarness{
		candidates: append([]recoveryusecase.RecoveryCandidate(nil), candidates...),
		err:        err,
	}
	harness.source = gamemocks.NewMockRecoverySource(t)
	harness.source.EXPECT().ListActiveGames(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			context.Context,
			uuid.UUID,
			authoritydomain.Identity,
		) ([]recoveryusecase.RecoveryCandidate, error) {
			harness.mu.Lock()
			defer harness.mu.Unlock()
			harness.loads++
			if harness.err != nil {
				return nil, harness.err
			}
			return append([]recoveryusecase.RecoveryCandidate(nil), harness.candidates...), nil
		}).Maybe()
	return harness
}

func (r *executionRecoverySourceHarness) loadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loads
}

type deadlineRearmerHarness struct {
	rearmer            *gamemocks.MockDeadlineRearmer
	mu                 sync.Mutex
	arms               []recoveryusecase.DeadlineArm
	lease              authoritydomain.Lease
	transactionNow     time.Time
	advanceBeforeRearm time.Duration
}

func newDeadlineRearmerHarness(
	t *testing.T,
	lease authoritydomain.Lease,
	transactionNow time.Time,
) *deadlineRearmerHarness {
	t.Helper()
	harness := &deadlineRearmerHarness{lease: lease, transactionNow: transactionNow}
	harness.rearmer = gamemocks.NewMockDeadlineRearmer(t)
	harness.rearmer.EXPECT().RearmDeadline(mock.Anything, mock.Anything).
		RunAndReturn(harness.apply).Maybe()
	return harness
}

func (r *deadlineRearmerHarness) apply(_ context.Context, arm recoveryusecase.DeadlineArm) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.advanceBeforeRearm != 0 {
		r.transactionNow = r.transactionNow.Add(r.advanceBeforeRearm)
		r.advanceBeforeRearm = 0
	}
	if arm.Scope.TournamentID != r.lease.TournamentID || arm.RosterID != recoveryID(20) ||
		arm.Authority != *r.lease.Stamp() ||
		!r.lease.Proves(r.lease.Identity(), r.transactionNow) {
		return domain.ErrConflict
	}
	r.arms = append(r.arms, arm)
	return nil
}

func (r *deadlineRearmerHarness) advanceBeforeNextRearm(duration time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.advanceBeforeRearm = duration
}

func (r *deadlineRearmerHarness) armsSnapshot() []recoveryusecase.DeadlineArm {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recoveryusecase.DeadlineArm(nil), r.arms...)
}

type epochReplayerHarness struct {
	replayer *gamemocks.MockRecoveryEpochReplayer
	mu       sync.Mutex
	commands map[uuid.UUID]recoveryusecase.EpochReplayCommand
	writes   int
	err      error
}

func newEpochReplayerHarness(
	t *testing.T,
	err error,
) *epochReplayerHarness {
	t.Helper()
	harness := &epochReplayerHarness{err: err}
	harness.replayer = gamemocks.NewMockRecoveryEpochReplayer(t)
	harness.replayer.EXPECT().ReplayEpoch(mock.Anything, mock.Anything).
		RunAndReturn(harness.apply).Maybe()
	return harness
}

func (r *epochReplayerHarness) apply(
	_ context.Context,
	command recoveryusecase.EpochReplayCommand,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return false, r.err
	}
	if r.commands == nil {
		r.commands = make(map[uuid.UUID]recoveryusecase.EpochReplayCommand)
	}
	if _, exists := r.commands[command.Attempt.CommandID]; exists {
		return false, nil
	}
	r.commands[command.Attempt.CommandID] = command
	r.writes++
	return true, nil
}

func (r *epochReplayerHarness) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func newRecoveryClock(
	t *testing.T,
	now time.Time,
	calls int,
) *gamemocks.MockExecutionClock {
	t.Helper()
	clock := gamemocks.NewMockExecutionClock(t)
	expectation := clock.EXPECT().Now().Return(now)
	if calls > 0 {
		expectation.Times(calls)
	} else {
		expectation.Maybe()
	}
	return clock
}

func newRecoveryAuthorityTime(
	t *testing.T,
	now time.Time,
	calls int,
) *gamemocks.MockAuthorityTimeSource {
	t.Helper()
	timeSource := gamemocks.NewMockAuthorityTimeSource(t)
	timeSource.EXPECT().AuthorityTime(mock.Anything).Return(now, nil).Times(calls)
	return timeSource
}

func recoveryLease(now time.Time, epoch int64) authoritydomain.Lease {
	lease := authoritydomain.Lease{
		TournamentID: recoveryID(1), HolderID: recoveryID(2),
		LeaseID: recoveryID(3), Epoch: epoch,
		ProcessKind: authoritydomain.ProcessAuthority, Revision: epoch,
		CommandID:  recoveryID(4),
		AcquiredAt: now.Add(-time.Second), RenewedAt: now.Add(-time.Second),
		ExpiresAt: now.Add(time.Minute),
	}
	if epoch > 1 {
		lease.Previous = &authoritydomain.Stamp{
			LeaseID: recoveryID(5), Epoch: epoch - 1,
		}
	}
	return lease
}

func recoveryCandidate(
	now time.Time,
	bound *authoritydomain.Stamp,
	state domain.GameState,
) recoveryusecase.RecoveryCandidate {
	return recoveryusecase.RecoveryCandidate{
		Scope: domain.FailedAttemptScope{
			TournamentID: recoveryID(1), WaveID: recoveryID(6),
			SeriesID: recoveryID(7), SlotID: recoveryID(8),
			GameID: recoveryID(9), AssignmentID: recoveryID(10),
			AssignmentAttemptID: recoveryID(11),
		},
		RosterID:  recoveryID(20),
		AttemptNo: 1, State: state, SnapshotID: recoveryID(12),
		Category: domain.CategoryWeb, BoundAuthority: *bound,
		Deadline: now.Add(30 * time.Second),
	}
}

func recoveryEpochReplayCommand(
	candidate recoveryusecase.RecoveryCandidate,
	current authoritydomain.Identity,
) *recoveryusecase.EpochReplayCommand {
	return &recoveryusecase.EpochReplayCommand{
		CurrentAuthority: current,
		BrokenAuthority:  candidate.BoundAuthority,
		RosterID:         candidate.RosterID,
		Attempt: attemptusecase.AttemptCommand{
			Scope: candidate.Scope, CommandID: recoveryID(13),
			FailureClass: gamedomain.FailureExecutionEpochBreak,
			Expected: attemptusecase.Expectation{
				AttemptNo: candidate.AttemptNo, State: domain.GameStateActive,
				SnapshotID: candidate.SnapshotID, Category: candidate.Category,
			},
			Revisions: attemptusecase.AttemptRevisionSet{
				GameResultRevisionID: domain.OfficialResultRevisionID(recoveryID(14)),
				ScoreRevisionID:      domain.SeriesScoreRevisionID(recoveryID(15)),
				RouteEvidenceID:      recoveryID(16), AuditEventID: recoveryID(17),
				OutboxEventID: recoveryID(18), ProjectionRevisionID: recoveryID(19),
			},
		},
	}
}

var _ recoveryusecase.RecoveryAuthorityReader = (*gamemocks.MockRecoveryAuthorityReader)(nil)
var _ recoveryusecase.RecoverySource = (*gamemocks.MockRecoverySource)(nil)
var _ recoveryusecase.DeadlineRearmer = (*gamemocks.MockDeadlineRearmer)(nil)
var _ recoveryusecase.RecoveryEpochReplayer = (*gamemocks.MockRecoveryEpochReplayer)(nil)
var _ recoveryusecase.RecoveryEpochReplayer = (*recoveryusecase.EpochReplayUseCase)(nil)
var _ recoveryusecase.AuthorityTimeSource = (*gamemocks.MockAuthorityTimeSource)(nil)

func recoveryID(value int) uuid.UUID {
	name := fmt.Sprintf("execution-recovery-%d", value)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name))
}
