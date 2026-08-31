package recovery

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestArenaActiveGameEpochRecovery(t *testing.T) {
	t.Parallel()

	t.Run("rearms the stored deadline under continuous authority", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
		lease := recoveryArenaLease(now, 1)
		candidate := recoveryArenaCandidate(now, lease.Stamp(), domain.ArenaGameStateActive)
		source := &arenaExecutionRecoverySourceFake{candidates: []ArenaActiveGameRecovery{candidate}}
		timers := newArenaDeadlineRearmerFake(lease, now)
		replayer := &arenaEpochReplayerFake{}
		recoverer := NewArenaExecutionRecoverer(
			&arenaAuthorityReaderFake{lease: &lease}, source, timers, replayer, recoveryClock{now: now},
		)

		report, err := recoverer.Recover(t.Context(), ArenaExecutionRecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		})
		require.NoError(t, err)
		require.Equal(t, ArenaExecutionRecoveryReport{Rearmed: 1}, report)
		require.Equal(t, []ArenaGameDeadlineArm{{
			Scope: candidate.Scope, AttemptNo: candidate.AttemptNo,
			Authority: *lease.Stamp(), Deadline: candidate.Deadline,
		}}, timers.armsSnapshot())
		require.Equal(t, 0, replayer.writeCount())
	})

	t.Run("routes one technical replay after an unpaused epoch break", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 5, 0, 0, time.UTC)
		lease := recoveryArenaLease(now, 2)
		broken := arena.ExecutionAuthorityStamp{LeaseID: recoveryArenaID(20), Epoch: 1}
		candidate := recoveryArenaCandidate(now, &broken, domain.ArenaGameStateActive)
		candidate.EpochReplay = recoveryEpochReplayCommand(candidate, lease.Identity())
		source := &arenaExecutionRecoverySourceFake{candidates: []ArenaActiveGameRecovery{candidate}}
		timers := newArenaDeadlineRearmerFake(lease, now)
		replayer := &arenaEpochReplayerFake{}
		recoverer := NewArenaExecutionRecoverer(
			&arenaAuthorityReaderFake{lease: &lease}, source, timers, replayer, recoveryClock{now: now},
		)
		command := ArenaExecutionRecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		}

		first, err := recoverer.Recover(t.Context(), command)
		require.NoError(t, err)
		require.Equal(t, ArenaExecutionRecoveryReport{TechnicalReplays: 1, Changed: 1}, first)
		second, err := recoverer.Recover(t.Context(), command)
		require.NoError(t, err)
		require.Equal(t, ArenaExecutionRecoveryReport{TechnicalReplays: 1}, second)
		require.Empty(t, timers.armsSnapshot())
		require.Equal(t, 1, replayer.writeCount())
	})

	t.Run("leaves a paused Game unchanged across an epoch break", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 10, 0, 0, time.UTC)
		lease := recoveryArenaLease(now, 2)
		broken := arena.ExecutionAuthorityStamp{LeaseID: recoveryArenaID(30), Epoch: 1}
		candidate := recoveryArenaCandidate(now, &broken, domain.ArenaGameStatePaused)
		source := &arenaExecutionRecoverySourceFake{candidates: []ArenaActiveGameRecovery{candidate}}
		timers := newArenaDeadlineRearmerFake(lease, now)
		replayer := &arenaEpochReplayerFake{}
		recoverer := NewArenaExecutionRecoverer(
			&arenaAuthorityReaderFake{lease: &lease}, source, timers, replayer, recoveryClock{now: now},
		)

		report, err := recoverer.Recover(t.Context(), ArenaExecutionRecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		})
		require.NoError(t, err)
		require.Equal(t, ArenaExecutionRecoveryReport{Paused: 1}, report)
		require.Empty(t, timers.armsSnapshot())
		require.Equal(t, 0, replayer.writeCount())
	})

	t.Run("rejects a non-authoritative restart before listing Games", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 15, 0, 0, time.UTC)
		lease := recoveryArenaLease(now, 2)
		source := &arenaExecutionRecoverySourceFake{}
		timers := newArenaDeadlineRearmerFake(lease, now)
		replayer := &arenaEpochReplayerFake{}
		recoverer := NewArenaExecutionRecoverer(
			&arenaAuthorityReaderFake{lease: &lease}, source, timers, replayer, recoveryClock{now: now},
		)
		foreign := lease.Identity()
		foreign.HolderID = recoveryArenaID(40)

		report, err := recoverer.Recover(t.Context(), ArenaExecutionRecoveryCommand{
			TournamentID: lease.TournamentID, Authority: foreign,
		})
		require.Zero(t, report)
		require.ErrorIs(t, err, ErrArenaExecutionNotAuthoritative)
		require.Equal(t, 0, source.loadCount())
		require.Empty(t, timers.armsSnapshot())
		require.Equal(t, 0, replayer.writeCount())
	})

	t.Run("rejects duplicate Games before recovery side effects", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 20, 0, 0, time.UTC)
		lease := recoveryArenaLease(now, 1)
		candidate := recoveryArenaCandidate(now, lease.Stamp(), domain.ArenaGameStateActive)
		source := &arenaExecutionRecoverySourceFake{
			candidates: []ArenaActiveGameRecovery{candidate, candidate},
		}
		timers := newArenaDeadlineRearmerFake(lease, now)
		replayer := &arenaEpochReplayerFake{}
		report, err := NewArenaExecutionRecoverer(
			&arenaAuthorityReaderFake{lease: &lease}, source, timers, replayer,
			recoveryClock{now: now},
		).Recover(t.Context(), ArenaExecutionRecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		})
		require.Zero(t, report)
		require.ErrorIs(t, err, ErrInvalidArenaExecutionRecovery)
		require.Empty(t, timers.armsSnapshot())
		require.Equal(t, 0, replayer.writeCount())
	})

	t.Run("validates the whole batch before the first side effect", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 25, 0, 0, time.UTC)
		lease := recoveryArenaLease(now, 1)
		valid := recoveryArenaCandidate(now, lease.Stamp(), domain.ArenaGameStateActive)
		invalid := valid
		invalid.Scope.GameID = recoveryArenaID(50)
		invalid.State = domain.ArenaGameState("invalid")
		source := &arenaExecutionRecoverySourceFake{
			candidates: []ArenaActiveGameRecovery{valid, invalid},
		}
		timers := newArenaDeadlineRearmerFake(lease, now)
		replayer := &arenaEpochReplayerFake{}
		report, err := NewArenaExecutionRecoverer(
			&arenaAuthorityReaderFake{lease: &lease}, source, timers, replayer,
			recoveryClock{now: now},
		).Recover(t.Context(), ArenaExecutionRecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		})
		require.Zero(t, report)
		require.ErrorIs(t, err, ErrInvalidArenaExecutionRecovery)
		require.Empty(t, timers.armsSnapshot())
		require.Equal(t, 0, replayer.writeCount())
	})

	t.Run("fences deadline rearm at authoritative linearization time", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 28, 0, 0, time.UTC)
		lease := recoveryArenaLease(now, 1)
		candidate := recoveryArenaCandidate(now, lease.Stamp(), domain.ArenaGameStateActive)
		source := &arenaExecutionRecoverySourceFake{
			candidates: []ArenaActiveGameRecovery{candidate},
		}
		timers := newArenaDeadlineRearmerFake(lease, now)
		timers.advanceBeforeRearm = lease.ExpiresAt.Sub(now)
		replayer := &arenaEpochReplayerFake{}
		report, err := NewArenaExecutionRecoverer(
			&arenaAuthorityReaderFake{lease: &lease}, source, timers, replayer,
			recoveryClock{now: now},
		).Recover(t.Context(), ArenaExecutionRecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		})
		require.Zero(t, report)
		require.ErrorIs(t, err, domain.ErrConflict)
		require.ErrorContains(t, err, "rearm Game deadline")
		require.Empty(t, timers.armsSnapshot())
		require.Equal(t, 0, replayer.writeCount())
	})

	t.Run("preserves wrapped reader source and replayer errors", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 30, 0, 0, time.UTC)
		lease := recoveryArenaLease(now, 2)
		command := ArenaExecutionRecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		}
		for _, test := range []struct {
			name      string
			reader    *arenaAuthorityReaderFake
			source    *arenaExecutionRecoverySourceFake
			replayer  *arenaEpochReplayerFake
			wantError error
		}{
			{
				name: "reader", reader: &arenaAuthorityReaderFake{err: errors.New("reader failed")},
				source: &arenaExecutionRecoverySourceFake{}, replayer: &arenaEpochReplayerFake{},
			},
			{
				name: "source", reader: &arenaAuthorityReaderFake{lease: &lease},
				source:   &arenaExecutionRecoverySourceFake{err: errors.New("source failed")},
				replayer: &arenaEpochReplayerFake{},
			},
			{
				name: "replayer", reader: &arenaAuthorityReaderFake{lease: &lease},
				source:   &arenaExecutionRecoverySourceFake{},
				replayer: &arenaEpochReplayerFake{err: errors.New("replayer failed")},
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				if test.name == "replayer" {
					broken := arena.ExecutionAuthorityStamp{
						LeaseID: recoveryArenaID(51), Epoch: 1,
					}
					candidate := recoveryArenaCandidate(
						now,
						&broken,
						domain.ArenaGameStateActive,
					)
					candidate.EpochReplay = recoveryEpochReplayCommand(candidate, lease.Identity())
					test.source.candidates = []ArenaActiveGameRecovery{candidate}
				}
				switch test.name {
				case "reader":
					test.wantError = test.reader.err
				case "source":
					test.wantError = test.source.err
				case "replayer":
					test.wantError = test.replayer.err
				}
				timers := newArenaDeadlineRearmerFake(lease, now)
				report, err := NewArenaExecutionRecoverer(
					test.reader, test.source, timers, test.replayer, recoveryClock{now: now},
				).Recover(t.Context(), command)
				require.Zero(t, report)
				require.ErrorIs(t, err, test.wantError)
				require.Empty(t, timers.armsSnapshot())
				require.Equal(t, 0, test.replayer.writeCount())
			})
		}
	})
}

type arenaAuthorityReaderFake struct {
	lease *arena.ExecutionAuthorityLease
	err   error
}

func (r *arenaAuthorityReaderFake) LoadExecutionAuthority(
	context.Context,
	uuid.UUID,
) (*arena.ExecutionAuthorityLease, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.lease == nil {
		return nil, nil
	}
	clone := *r.lease
	return &clone, nil
}

type arenaExecutionRecoverySourceFake struct {
	mu         sync.Mutex
	candidates []ArenaActiveGameRecovery
	loads      int
	err        error
}

func (r *arenaExecutionRecoverySourceFake) ListActiveArenaGames(
	context.Context,
	uuid.UUID,
) ([]ArenaActiveGameRecovery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loads++
	if r.err != nil {
		return nil, r.err
	}
	return append([]ArenaActiveGameRecovery(nil), r.candidates...), nil
}

func (r *arenaExecutionRecoverySourceFake) loadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loads
}

type arenaDeadlineRearmerFake struct {
	mu                 sync.Mutex
	arms               []ArenaGameDeadlineArm
	lease              arena.ExecutionAuthorityLease
	transactionNow     time.Time
	advanceBeforeRearm time.Duration
	err                error
}

func newArenaDeadlineRearmerFake(
	lease arena.ExecutionAuthorityLease,
	transactionNow time.Time,
) *arenaDeadlineRearmerFake {
	return &arenaDeadlineRearmerFake{lease: lease, transactionNow: transactionNow}
}

func (r *arenaDeadlineRearmerFake) RearmArenaGameDeadline(arm ArenaGameDeadlineArm) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	if r.advanceBeforeRearm != 0 {
		r.transactionNow = r.transactionNow.Add(r.advanceBeforeRearm)
		r.advanceBeforeRearm = 0
	}
	if arm.Scope.TournamentID != r.lease.TournamentID || arm.Authority != *r.lease.Stamp() ||
		!r.lease.Proves(r.lease.Identity(), r.transactionNow) {
		return domain.ErrConflict
	}
	r.arms = append(r.arms, arm)
	return nil
}

func (r *arenaDeadlineRearmerFake) armsSnapshot() []ArenaGameDeadlineArm {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ArenaGameDeadlineArm(nil), r.arms...)
}

type arenaEpochReplayerFake struct {
	mu       sync.Mutex
	commands map[uuid.UUID]arena.ExecutionEpochReplayCommand
	writes   int
	err      error
}

func (r *arenaEpochReplayerFake) ReplayExecutionEpoch(
	_ context.Context,
	command arena.ExecutionEpochReplayCommand,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return false, r.err
	}
	if r.commands == nil {
		r.commands = make(map[uuid.UUID]arena.ExecutionEpochReplayCommand)
	}
	if _, exists := r.commands[command.Attempt.CommandID]; exists {
		return false, nil
	}
	r.commands[command.Attempt.CommandID] = command
	r.writes++
	return true, nil
}

func (r *arenaEpochReplayerFake) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func recoveryArenaLease(now time.Time, epoch int64) arena.ExecutionAuthorityLease {
	lease := arena.ExecutionAuthorityLease{
		TournamentID: recoveryArenaID(1), HolderID: recoveryArenaID(2),
		LeaseID: recoveryArenaID(3), Epoch: epoch,
		ProcessKind: arena.ExecutionProcessKindAuthority, Revision: epoch,
		CommandID:  recoveryArenaID(4),
		AcquiredAt: now.Add(-time.Second), RenewedAt: now.Add(-time.Second),
		ExpiresAt: now.Add(time.Minute),
	}
	if epoch > 1 {
		lease.Previous = &arena.ExecutionAuthorityStamp{
			LeaseID: recoveryArenaID(5), Epoch: epoch - 1,
		}
	}
	return lease
}

func recoveryArenaCandidate(
	now time.Time,
	bound *arena.ExecutionAuthorityStamp,
	state domain.ArenaGameState,
) ArenaActiveGameRecovery {
	return ArenaActiveGameRecovery{
		Scope: arena.FailedAttemptScope{
			TournamentID: recoveryArenaID(1), WaveID: recoveryArenaID(6),
			SeriesID: recoveryArenaID(7), SlotID: recoveryArenaID(8),
			GameID: recoveryArenaID(9), AssignmentID: recoveryArenaID(10),
			AssignmentAttemptID: recoveryArenaID(11),
		},
		AttemptNo: 1, State: state, SnapshotID: recoveryArenaID(12),
		Category: domain.CategoryWeb, BoundAuthority: *bound,
		Deadline: now.Add(30 * time.Second),
	}
}

func recoveryEpochReplayCommand(
	candidate ArenaActiveGameRecovery,
	current arena.ExecutionAuthorityIdentity,
) *arena.ExecutionEpochReplayCommand {
	return &arena.ExecutionEpochReplayCommand{
		CurrentAuthority: current,
		BrokenAuthority:  candidate.BoundAuthority,
		Attempt: arena.FailedAttemptCommand{
			Scope: candidate.Scope, CommandID: recoveryArenaID(13),
			FailureClass: arena.NormalAttemptFailureExecutionEpochBreak,
			Expected: arena.FailedAttemptExpectation{
				AttemptNo: candidate.AttemptNo, State: domain.ArenaGameStateActive,
				SnapshotID: candidate.SnapshotID, Category: candidate.Category,
			},
			Revisions: arena.FailedAttemptRevisionSet{
				GameResultRevisionID: domain.ArenaOfficialResultRevisionID(recoveryArenaID(14)),
				ScoreRevisionID:      domain.ArenaSeriesScoreRevisionID(recoveryArenaID(15)),
				RouteEvidenceID:      recoveryArenaID(16), AuditEventID: recoveryArenaID(17),
				OutboxEventID: recoveryArenaID(18), ProjectionRevisionID: recoveryArenaID(19),
			},
		},
	}
}

func recoveryArenaID(value int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(time.Unix(int64(value), 0).UTC().Format(time.RFC3339)))
}

var _ ArenaExecutionAuthorityReader = (*arenaAuthorityReaderFake)(nil)
var _ ArenaActiveGameRecoverySource = (*arenaExecutionRecoverySourceFake)(nil)
var _ ArenaGameDeadlineRearmer = (*arenaDeadlineRearmerFake)(nil)
var _ ArenaExecutionEpochReplayer = (*arenaEpochReplayerFake)(nil)
var _ ArenaExecutionEpochReplayer = (*arena.ExecutionEpochReplayUseCase)(nil)
