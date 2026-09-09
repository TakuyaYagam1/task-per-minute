package game_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func TestNoSolveReplayPipeline(t *testing.T) {
	t.Parallel()

	t.Run("uses both planned same-category reserves with fresh execution identities", func(t *testing.T) {
		t.Parallel()

		for activeIndex := 0; activeIndex < domain.AssignmentReserveCount; activeIndex++ {
			t.Run(fmt.Sprintf("reserve_%d", activeIndex+1), func(t *testing.T) {
				t.Parallel()

				now := time.Date(2026, 8, 30, 22, 40+activeIndex, 0, 0, time.UTC)
				authority, command := replayReplacementFixture(t, now, activeIndex)
				repository := newReplayReplacementRepositoryHarness(t, authority)
				usecase := gameusecase.NewReplayReplacementUseCase(
					repository,
					newFixedClock(t, now),
				)

				replacement, changed, err := usecase.Replace(t.Context(), command)
				require.NoError(t, err)
				require.True(t, changed)
				require.NoError(t, replacement.Validate())
				require.Equal(t, activeIndex+2, replacement.ReservePosition)
				require.Equal(t, authority.ReserveChain.Snapshots[activeIndex+1].SnapshotID,
					replacement.Snapshot.SnapshotID)
				require.Equal(t, domain.CategoryWeb, replacement.Category)
				require.Equal(t, command.GameID, replacement.Game.ID)
				require.Equal(t, activeIndex+2, replacement.Game.AttemptNo)
				require.Equal(t, authority.Scope.SlotID, replacement.Game.SlotID)
				require.Equal(t, domain.WaveStateReadyWindowOpen, replacement.Wave.State)
				require.Equal(t, domain.ReadyWindowStateOpen, replacement.Wave.ReadyWindow.State)
				require.False(t, replacement.Wave.Members[0].Ready)
				require.False(t, replacement.Wave.Members[1].Ready)
				require.NotEqual(t, authority.Scope.OldWaveID, replacement.Wave.ID)
				require.Equal(t, 1, repository.writeCount())

				repeated, changed, err := usecase.Replace(t.Context(), command)
				require.NoError(t, err)
				require.False(t, changed)
				require.Equal(t, replacement, repeated)
				require.Equal(t, 1, repository.writeCount())
			})
		}
	})

	t.Run("exhaustion keeps the old Wave closed and creates no replacement", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 45, 0, 0, time.UTC)
		authority, command := replayReplacementFixture(
			t,
			now,
			domain.AssignmentReserveCount,
		)
		repository := newReplayReplacementRepositoryHarness(t, authority)

		replacement, changed, err := gameusecase.NewReplayReplacementUseCase(
			repository,
			newFixedClock(t, now),
		).Replace(t.Context(), command)
		require.Nil(t, replacement)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrReplayReservesExhausted)
		require.Equal(t, domain.WaveStateCompleted, authority.OldWaveClosure.Wave.State)
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("orders terminalization, closure, and replacement", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 50, 0, 0, time.UTC)
		authority, replacementCommand := replayReplacementFixture(t, now, 0)
		replacementRepository := newReplayReplacementRepositoryHarness(t, authority)
		replacement, replacementChanged, err := gameusecase.NewReplayReplacementUseCase(
			replacementRepository,
			newFixedClock(t, now),
		).Replace(t.Context(), replacementCommand)
		require.NoError(t, err)
		require.True(t, replacementChanged)
		failedCommand := replayFailedCommandFromAuthority(authority)
		closeCommand := gameusecase.CloseCommand{
			Scope:                  authority.OldWaveClosure.Scope,
			CommandID:              authority.OldWaveClosure.CommandID,
			ExpectedWaveRevisionID: authority.OldWaveClosure.PreviousWaveRevisionID,
			ClosedWaveRevisionID:   authority.OldWaveClosure.Wave.RevisionID,
		}
		pipeline := &replayPipelineState{
			failed: &authority.FailedAttempt, closure: &authority.OldWaveClosure,
			replacement: replacement,
		}
		terminalizer, closer, replacer := newReplayPipelineMocks(t, pipeline)
		command := gameusecase.NoSolveReplayCommand{
			Terminalize: failedCommand,
			Close:       closeCommand,
			Replace:     replacementCommand,
		}

		result, changed, err := gameusecase.NewNoSolveReplayUseCase(terminalizer, closer, replacer).
			Replay(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, result.FailedAttempt)
		require.NotNil(t, result.OldWaveClosure)
		require.NotNil(t, result.Replacement)
		require.Equal(t, []string{"terminalize", "close", "replace"}, pipeline.callOrder())
	})

	t.Run("pipeline reports exhaustion only after closure", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 55, 0, 0, time.UTC)
		authority, replacementCommand := replayReplacementFixture(
			t,
			now,
			domain.AssignmentReserveCount,
		)
		failedCommand := replayFailedCommandFromAuthority(authority)
		closeCommand := gameusecase.CloseCommand{
			Scope:                  authority.OldWaveClosure.Scope,
			CommandID:              authority.OldWaveClosure.CommandID,
			ExpectedWaveRevisionID: authority.OldWaveClosure.PreviousWaveRevisionID,
			ClosedWaveRevisionID:   authority.OldWaveClosure.Wave.RevisionID,
		}
		pipeline := &replayPipelineState{
			failed:     &authority.FailedAttempt,
			closure:    &authority.OldWaveClosure,
			replaceErr: gameusecase.ErrReplayReservesExhausted,
		}
		terminalizer, closer, replacer := newReplayPipelineMocks(t, pipeline)

		result, changed, err := gameusecase.NewNoSolveReplayUseCase(terminalizer, closer, replacer).Replay(
			t.Context(),
			gameusecase.NoSolveReplayCommand{
				Terminalize: failedCommand,
				Close:       closeCommand,
				Replace:     replacementCommand,
			},
		)
		require.NoError(t, err)
		require.True(t, changed)
		require.True(t, result.Exhausted)
		require.Nil(t, result.Replacement)
		require.Equal(t, domain.WaveStateCompleted, result.OldWaveClosure.Wave.State)
		require.Equal(t, []string{"terminalize", "close", "replace"}, pipeline.callOrder())
	})

	t.Run("rejects a stage record that does not match its command", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 23, 0, 0, 0, time.UTC)
		authority, replacementCommand := replayReplacementFixture(t, now, 0)
		failedCommand := replayFailedCommandFromAuthority(authority)
		failedCommand.Revisions.AuditEventID = replayID(999)
		closeCommand := gameusecase.CloseCommand{
			Scope:                  authority.OldWaveClosure.Scope,
			CommandID:              authority.OldWaveClosure.CommandID,
			ExpectedWaveRevisionID: authority.OldWaveClosure.PreviousWaveRevisionID,
			ClosedWaveRevisionID:   authority.OldWaveClosure.Wave.RevisionID,
		}
		pipeline := &replayPipelineState{
			failed: &authority.FailedAttempt, closure: &authority.OldWaveClosure,
		}
		terminalizer, closer, replacer := newReplayPipelineMocks(t, pipeline)

		result, changed, err := gameusecase.NewNoSolveReplayUseCase(terminalizer, closer, replacer).Replay(
			t.Context(),
			gameusecase.NoSolveReplayCommand{
				Terminalize: failedCommand,
				Close:       closeCommand,
				Replace:     replacementCommand,
			},
		)
		require.Nil(t, result)
		require.False(t, changed)
		require.ErrorIs(t, err, domain.ErrInternal)
		require.Equal(t, []string{"terminalize"}, pipeline.callOrder())
	})
}

func TestReplayReplacementAfterOperatorReserve(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 23, 5, 0, 0, time.UTC)

	t.Run("uses the one operator-added fourth reserve", func(t *testing.T) {
		t.Parallel()

		authority, command := replayReplacementFixture(t, now, domain.AssignmentReserveCount)
		authority.ReserveChain.Snapshots = append(authority.ReserveChain.Snapshots, replaySnapshot(t, 3))
		repository := newReplayReplacementRepositoryHarness(t, authority)
		usecase := gameusecase.NewReplayReplacementUseCase(repository, newFixedClock(t, now))

		replacement, changed, err := usecase.Replace(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.AssignmentReserveCount+2, replacement.ReservePosition)
		require.Equal(t, authority.ReserveChain.Snapshots[3].SnapshotID, replacement.Snapshot.SnapshotID)

		repeated, changed, err := usecase.Replace(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, replacement, repeated)
	})

	t.Run("rejects an operator chain that skips an unconsumed base reserve", func(t *testing.T) {
		t.Parallel()

		authority, command := replayReplacementFixture(t, now, domain.AssignmentReserveCount-1)
		authority.ReserveChain.Snapshots = append(authority.ReserveChain.Snapshots, replaySnapshot(t, 3))

		replacement, changed, err := gameusecase.NewReplayReplacementUseCase(
			newReplayReplacementRepositoryHarness(t, authority),
			newFixedClock(t, now),
		).Replace(t.Context(), command)
		require.Nil(t, replacement)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrInvalidReplayReplacement)
	})

	t.Run("rejects more than one operator-added reserve", func(t *testing.T) {
		t.Parallel()

		authority, command := replayReplacementFixture(t, now, domain.AssignmentReserveCount)
		authority.ReserveChain.Snapshots = append(
			authority.ReserveChain.Snapshots,
			replaySnapshot(t, 3),
			replaySnapshot(t, 4),
		)

		replacement, changed, err := gameusecase.NewReplayReplacementUseCase(
			newReplayReplacementRepositoryHarness(t, authority),
			newFixedClock(t, now),
		).Replace(t.Context(), command)
		require.Nil(t, replacement)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrInvalidReplayReplacement)
	})
}

func TestReplayReplacementReturnsDurableCurrentBeforeFreshAuthorityValidation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 30, 23, 10, 0, 0, time.UTC)
	authority, command := replayReplacementFixture(t, now, 0)
	repository := newReplayReplacementRepositoryHarness(t, authority)
	usecase := gameusecase.NewReplayReplacementUseCase(repository, newFixedClock(t, now))

	created, changed, err := usecase.Replace(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)

	repository.state.mu.Lock()
	repository.state.authority.Revision = 0
	repository.state.mu.Unlock()

	current, changed, err := usecase.Replace(t.Context(), command)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, created, current)
}
