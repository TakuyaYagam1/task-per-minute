package recovery_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	recoveryusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/recovery"
)

func TestExecutionRecovery(t *testing.T) {
	t.Parallel()

	t.Run("rearms the stored deadline under continuous authority", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
		lease := recoveryLease(now, 1)
		candidate := recoveryCandidate(now, lease.Stamp(), domain.GameStateActive)
		source := newExecutionRecoverySourceHarness(
			t, []recoveryusecase.RecoveryCandidate{candidate}, nil,
		)
		timers := newDeadlineRearmerHarness(t, lease, now)
		replayer := newEpochReplayerHarness(t, nil)
		recoverer := recoveryusecase.NewRecoverer(
			newAuthorityReaderMock(t, &lease, nil),
			source.source,
			timers.rearmer,
			replayer.replayer,
			newRecoveryAuthorityTime(t, now, 1),
		)

		report, err := recoverer.Recover(t.Context(), recoveryusecase.RecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		})
		require.NoError(t, err)
		require.Equal(t, recoveryusecase.RecoveryReport{Rearmed: 1}, report)
		require.Equal(t, []recoveryusecase.DeadlineArm{{
			Scope: candidate.Scope, RosterID: candidate.RosterID, AttemptNo: candidate.AttemptNo,
			Authority: *lease.Stamp(), Deadline: candidate.Deadline,
		}}, timers.armsSnapshot())
		require.Equal(t, 0, replayer.writeCount())
	})

	t.Run("uses database time rather than a skewed process clock for authority proof", func(t *testing.T) {
		t.Parallel()

		databaseNow := time.Date(2026, 8, 31, 10, 2, 0, 0, time.UTC)
		lease := recoveryLease(databaseNow, 1)
		hostNow := lease.ExpiresAt.Add(time.Hour)
		require.False(t, lease.Proves(lease.Identity(), hostNow))
		candidate := recoveryCandidate(databaseNow, lease.Stamp(), domain.GameStateActive)
		source := newExecutionRecoverySourceHarness(
			t, []recoveryusecase.RecoveryCandidate{candidate}, nil,
		)
		timers := newDeadlineRearmerHarness(t, lease, databaseNow)
		replayer := newEpochReplayerHarness(t, nil)
		recoverer := recoveryusecase.NewRecoverer(
			newAuthorityReaderMock(t, &lease, nil),
			source.source,
			timers.rearmer,
			replayer.replayer,
			newRecoveryAuthorityTime(t, databaseNow, 1),
		)

		report, err := recoverer.Recover(t.Context(), recoveryusecase.RecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		})
		require.NoError(t, err)
		require.Equal(t, recoveryusecase.RecoveryReport{Rearmed: 1}, report)
	})

	t.Run("routes one technical replay after an unpaused epoch break", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 5, 0, 0, time.UTC)
		lease := recoveryLease(now, 2)
		broken := authoritydomain.Stamp{LeaseID: recoveryID(20), Epoch: 1}
		candidate := recoveryCandidate(now, &broken, domain.GameStateActive)
		candidate.EpochReplay = recoveryEpochReplayCommand(candidate, lease.Identity())
		source := newExecutionRecoverySourceHarness(
			t, []recoveryusecase.RecoveryCandidate{candidate}, nil,
		)
		timers := newDeadlineRearmerHarness(t, lease, now)
		replayer := newEpochReplayerHarness(t, nil)
		recoverer := recoveryusecase.NewRecoverer(
			newAuthorityReaderMock(t, &lease, nil, 2),
			source.source,
			timers.rearmer,
			replayer.replayer,
			newRecoveryAuthorityTime(t, now, 2),
		)
		command := recoveryusecase.RecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		}

		first, err := recoverer.Recover(t.Context(), command)
		require.NoError(t, err)
		require.Equal(
			t,
			recoveryusecase.RecoveryReport{TechnicalReplays: 1, Changed: 1},
			first,
		)
		second, err := recoverer.Recover(t.Context(), command)
		require.NoError(t, err)
		require.Equal(t, recoveryusecase.RecoveryReport{TechnicalReplays: 1}, second)
		require.Empty(t, timers.armsSnapshot())
		require.Equal(t, 1, replayer.writeCount())
	})

	t.Run("keeps a paused Game inert across an authority takeover", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 10, 0, 0, time.UTC)
		lease := recoveryLease(now, 2)
		broken := authoritydomain.Stamp{LeaseID: recoveryID(30), Epoch: 1}
		candidate := recoveryCandidate(now, &broken, domain.GameStatePaused)
		source := newExecutionRecoverySourceHarness(
			t, []recoveryusecase.RecoveryCandidate{candidate}, nil,
		)
		timers := newDeadlineRearmerHarness(t, lease, now)
		replayer := newEpochReplayerHarness(t, nil)
		recoverer := recoveryusecase.NewRecoverer(
			newAuthorityReaderMock(t, &lease, nil),
			source.source,
			timers.rearmer,
			replayer.replayer,
			newRecoveryAuthorityTime(t, now, 1),
		)

		report, err := recoverer.Recover(t.Context(), recoveryusecase.RecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		})
		require.NoError(t, err)
		require.Equal(t, recoveryusecase.RecoveryReport{Paused: 1}, report)
		require.Empty(t, timers.armsSnapshot())
		require.Equal(t, 0, replayer.writeCount())
	})

	t.Run("rejects a non-authoritative restart before listing Games", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 15, 0, 0, time.UTC)
		lease := recoveryLease(now, 2)
		source := newExecutionRecoverySourceHarness(t, nil, nil)
		timers := newDeadlineRearmerHarness(t, lease, now)
		replayer := newEpochReplayerHarness(t, nil)
		recoverer := recoveryusecase.NewRecoverer(
			newAuthorityReaderMock(t, &lease, nil),
			source.source,
			timers.rearmer,
			replayer.replayer,
			newRecoveryAuthorityTime(t, now, 1),
		)
		foreign := lease.Identity()
		foreign.HolderID = recoveryID(40)

		report, err := recoverer.Recover(t.Context(), recoveryusecase.RecoveryCommand{
			TournamentID: lease.TournamentID, Authority: foreign,
		})
		require.Zero(t, report)
		require.ErrorIs(t, err, recoveryusecase.ErrNotAuthoritative)
		require.Equal(t, 0, source.loadCount())
		require.Empty(t, timers.armsSnapshot())
		require.Equal(t, 0, replayer.writeCount())
	})

	t.Run("rejects duplicate Games before recovery side effects", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 20, 0, 0, time.UTC)
		lease := recoveryLease(now, 1)
		candidate := recoveryCandidate(now, lease.Stamp(), domain.GameStateActive)
		source := newExecutionRecoverySourceHarness(
			t, []recoveryusecase.RecoveryCandidate{candidate, candidate}, nil,
		)
		timers := newDeadlineRearmerHarness(t, lease, now)
		replayer := newEpochReplayerHarness(t, nil)
		report, err := recoveryusecase.NewRecoverer(
			newAuthorityReaderMock(t, &lease, nil),
			source.source,
			timers.rearmer,
			replayer.replayer,
			newRecoveryAuthorityTime(t, now, 1),
		).Recover(t.Context(), recoveryusecase.RecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		})
		require.Zero(t, report)
		require.ErrorIs(t, err, recoveryusecase.ErrInvalidRecovery)
		require.Empty(t, timers.armsSnapshot())
		require.Equal(t, 0, replayer.writeCount())
	})

	t.Run("validates the whole batch before the first side effect", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 25, 0, 0, time.UTC)
		lease := recoveryLease(now, 1)
		valid := recoveryCandidate(now, lease.Stamp(), domain.GameStateActive)
		invalid := valid
		invalid.Scope.GameID = recoveryID(50)
		invalid.State = domain.GameState("invalid")
		source := newExecutionRecoverySourceHarness(
			t, []recoveryusecase.RecoveryCandidate{valid, invalid}, nil,
		)
		timers := newDeadlineRearmerHarness(t, lease, now)
		replayer := newEpochReplayerHarness(t, nil)
		report, err := recoveryusecase.NewRecoverer(
			newAuthorityReaderMock(t, &lease, nil),
			source.source,
			timers.rearmer,
			replayer.replayer,
			newRecoveryAuthorityTime(t, now, 1),
		).Recover(t.Context(), recoveryusecase.RecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		})
		require.Zero(t, report)
		require.ErrorIs(t, err, recoveryusecase.ErrInvalidRecovery)
		require.Empty(t, timers.armsSnapshot())
		require.Equal(t, 0, replayer.writeCount())
	})

	t.Run("fences deadline rearm at authoritative linearization time", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 28, 0, 0, time.UTC)
		lease := recoveryLease(now, 1)
		candidate := recoveryCandidate(now, lease.Stamp(), domain.GameStateActive)
		source := newExecutionRecoverySourceHarness(
			t, []recoveryusecase.RecoveryCandidate{candidate}, nil,
		)
		timers := newDeadlineRearmerHarness(t, lease, now)
		timers.advanceBeforeNextRearm(lease.ExpiresAt.Sub(now))
		replayer := newEpochReplayerHarness(t, nil)
		report, err := recoveryusecase.NewRecoverer(
			newAuthorityReaderMock(t, &lease, nil),
			source.source,
			timers.rearmer,
			replayer.replayer,
			newRecoveryAuthorityTime(t, now, 1),
		).Recover(t.Context(), recoveryusecase.RecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		})
		require.Zero(t, report)
		require.ErrorIs(t, err, domain.ErrConflict)
		require.ErrorContains(t, err, "rearm deadline")
		require.Empty(t, timers.armsSnapshot())
		require.Equal(t, 0, replayer.writeCount())
	})

	t.Run("preserves wrapped reader source and replayer errors", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 10, 30, 0, 0, time.UTC)
		lease := recoveryLease(now, 2)
		command := recoveryusecase.RecoveryCommand{
			TournamentID: lease.TournamentID, Authority: lease.Identity(),
		}
		readerError := errors.New("reader failed")
		sourceError := errors.New("source failed")
		replayError := errors.New("replayer failed")
		for _, test := range []struct {
			name        string
			readerError error
			sourceError error
			replayError error
			wantError   error
		}{
			{name: "reader", readerError: readerError, wantError: readerError},
			{name: "source", sourceError: sourceError, wantError: sourceError},
			{name: "replayer", replayError: replayError, wantError: replayError},
		} {
			t.Run(test.name, func(t *testing.T) {
				var candidates []recoveryusecase.RecoveryCandidate
				if test.name == "replayer" {
					broken := authoritydomain.Stamp{
						LeaseID: recoveryID(51), Epoch: 1,
					}
					candidate := recoveryCandidate(
						now,
						&broken,
						domain.GameStateActive,
					)
					candidate.EpochReplay = recoveryEpochReplayCommand(candidate, lease.Identity())
					candidates = []recoveryusecase.RecoveryCandidate{candidate}
				}
				readerLease := &lease
				if test.readerError != nil {
					readerLease = nil
				}
				source := newExecutionRecoverySourceHarness(t, candidates, test.sourceError)
				timers := newDeadlineRearmerHarness(t, lease, now)
				replayer := newEpochReplayerHarness(t, test.replayError)
				report, err := recoveryusecase.NewRecoverer(
					newAuthorityReaderMock(t, readerLease, test.readerError),
					source.source,
					timers.rearmer,
					replayer.replayer,
					newRecoveryAuthorityTime(t, now, 1),
				).Recover(t.Context(), command)
				require.Zero(t, report)
				require.ErrorIs(t, err, test.wantError)
				require.Empty(t, timers.armsSnapshot())
				require.Equal(t, 0, replayer.writeCount())
			})
		}
	})
}
