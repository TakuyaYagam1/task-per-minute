package arena_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestNoSolveReplayPipeline(t *testing.T) {
	t.Parallel()

	t.Run("uses both planned same-category reserves with fresh execution identities", func(t *testing.T) {
		t.Parallel()

		for activeIndex := 0; activeIndex < domain.ArenaAssignmentReserveCount; activeIndex++ {
			t.Run(fmt.Sprintf("reserve_%d", activeIndex+1), func(t *testing.T) {
				t.Parallel()

				now := time.Date(2026, 8, 30, 22, 40+activeIndex, 0, 0, time.UTC)
				authority, command := task040ReplayReplacementFixture(t, now, activeIndex)
				repository := &replayReplacementRepositoryFake{authority: authority}
				usecase := arena.NewReplayReplacementUseCase(
					repository,
					fixedArenaClock{now: now},
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
				require.Equal(t, domain.ArenaWaveStateReadyWindowOpen, replacement.Wave.State)
				require.Equal(t, domain.ArenaReadyWindowStateOpen, replacement.Wave.ReadyWindow.State)
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
		authority, command := task040ReplayReplacementFixture(
			t,
			now,
			domain.ArenaAssignmentReserveCount,
		)
		repository := &replayReplacementRepositoryFake{authority: authority}

		replacement, changed, err := arena.NewReplayReplacementUseCase(
			repository,
			fixedArenaClock{now: now},
		).Replace(t.Context(), command)
		require.Nil(t, replacement)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrReplayReservesExhausted)
		require.Equal(t, domain.ArenaWaveStateCompleted, authority.OldWaveClosure.Wave.State)
		require.Equal(t, 0, repository.writeCount())
	})

	t.Run("orders terminalization, closure, and replacement", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 50, 0, 0, time.UTC)
		authority, replacementCommand := task040ReplayReplacementFixture(t, now, 0)
		replacementRepository := &replayReplacementRepositoryFake{authority: authority}
		replacement, replacementChanged, err := arena.NewReplayReplacementUseCase(
			replacementRepository,
			fixedArenaClock{now: now},
		).Replace(t.Context(), replacementCommand)
		require.NoError(t, err)
		require.True(t, replacementChanged)
		failedCommand := task040FailedCommandFromAuthority(authority)
		closeCommand := arena.OldWaveCloseCommand{
			Scope:                  authority.OldWaveClosure.Scope,
			CommandID:              authority.OldWaveClosure.CommandID,
			ExpectedWaveRevisionID: authority.OldWaveClosure.PreviousWaveRevisionID,
			ClosedWaveRevisionID:   authority.OldWaveClosure.Wave.RevisionID,
		}
		spy := &replayPipelineSpy{
			failed: &authority.FailedAttempt, closure: &authority.OldWaveClosure,
			replacement: replacement,
		}
		command := arena.NoSolveReplayCommand{
			Terminalize: failedCommand,
			Close:       closeCommand,
			Replace:     replacementCommand,
		}

		result, changed, err := arena.NewNoSolveReplayUseCase(spy, spy, spy).
			Replay(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NotNil(t, result.FailedAttempt)
		require.NotNil(t, result.OldWaveClosure)
		require.NotNil(t, result.Replacement)
		require.Equal(t, []string{"terminalize", "close", "replace"}, spy.callOrder())
	})

	t.Run("pipeline reports exhaustion only after closure", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 22, 55, 0, 0, time.UTC)
		authority, replacementCommand := task040ReplayReplacementFixture(
			t,
			now,
			domain.ArenaAssignmentReserveCount,
		)
		failedCommand := task040FailedCommandFromAuthority(authority)
		closeCommand := arena.OldWaveCloseCommand{
			Scope:                  authority.OldWaveClosure.Scope,
			CommandID:              authority.OldWaveClosure.CommandID,
			ExpectedWaveRevisionID: authority.OldWaveClosure.PreviousWaveRevisionID,
			ClosedWaveRevisionID:   authority.OldWaveClosure.Wave.RevisionID,
		}
		spy := &replayPipelineSpy{
			failed:     &authority.FailedAttempt,
			closure:    &authority.OldWaveClosure,
			replaceErr: arena.ErrReplayReservesExhausted,
		}

		result, changed, err := arena.NewNoSolveReplayUseCase(spy, spy, spy).Replay(
			t.Context(),
			arena.NoSolveReplayCommand{
				Terminalize: failedCommand,
				Close:       closeCommand,
				Replace:     replacementCommand,
			},
		)
		require.NoError(t, err)
		require.True(t, changed)
		require.True(t, result.Exhausted)
		require.Nil(t, result.Replacement)
		require.Equal(t, domain.ArenaWaveStateCompleted, result.OldWaveClosure.Wave.State)
		require.Equal(t, []string{"terminalize", "close", "replace"}, spy.callOrder())
	})

	t.Run("rejects a stage record that does not match its command", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 23, 0, 0, 0, time.UTC)
		authority, replacementCommand := task040ReplayReplacementFixture(t, now, 0)
		failedCommand := task040FailedCommandFromAuthority(authority)
		failedCommand.Revisions.AuditEventID = task040ID(999)
		closeCommand := arena.OldWaveCloseCommand{
			Scope:                  authority.OldWaveClosure.Scope,
			CommandID:              authority.OldWaveClosure.CommandID,
			ExpectedWaveRevisionID: authority.OldWaveClosure.PreviousWaveRevisionID,
			ClosedWaveRevisionID:   authority.OldWaveClosure.Wave.RevisionID,
		}
		spy := &replayPipelineSpy{
			failed: &authority.FailedAttempt, closure: &authority.OldWaveClosure,
		}

		result, changed, err := arena.NewNoSolveReplayUseCase(spy, spy, spy).Replay(
			t.Context(),
			arena.NoSolveReplayCommand{
				Terminalize: failedCommand,
				Close:       closeCommand,
				Replace:     replacementCommand,
			},
		)
		require.Nil(t, result)
		require.False(t, changed)
		require.ErrorIs(t, err, domain.ErrInternal)
		require.Equal(t, []string{"terminalize"}, spy.callOrder())
	})
}

type replayReplacementRepositoryFake struct {
	mu        sync.Mutex
	authority arena.ReplayReplacementAuthority
	writes    int
}

func (r *replayReplacementRepositoryFake) LoadReplayReplacementAuthority(
	_ context.Context,
	_ arena.ReplayReplacementScope,
) (arena.ReplayReplacementAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authority, nil
}

func (r *replayReplacementRepositoryFake) CommitReplayReplacement(
	_ context.Context,
	replacement arena.ReplayReplacement,
) (*arena.ReplayReplacement, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if replacement.ExpectedAuthorityRevision != r.authority.Revision || r.authority.Current != nil {
		return nil, false, domain.ErrConflict
	}
	stored := replacement
	r.authority.Revision++
	r.authority.ReserveChain.ActiveIndex = stored.ReservePosition - 1
	r.authority.Current = &stored
	r.writes++
	return &stored, true, nil
}

func (r *replayReplacementRepositoryFake) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

type replayPipelineSpy struct {
	mu          sync.Mutex
	order       []string
	failed      *arena.FailedAttemptRecord
	closure     *arena.OldWaveClosure
	replacement *arena.ReplayReplacement
	replaceErr  error
}

func (s *replayPipelineSpy) Terminalize(
	_ context.Context,
	_ arena.FailedAttemptCommand,
) (*arena.FailedAttemptRecord, bool, error) {
	s.appendCall("terminalize")
	return s.failed, true, nil
}

func (s *replayPipelineSpy) Close(
	_ context.Context,
	_ arena.OldWaveCloseCommand,
) (*arena.OldWaveClosure, bool, error) {
	s.appendCall("close")
	return s.closure, true, nil
}

func (s *replayPipelineSpy) Replace(
	_ context.Context,
	_ arena.ReplayReplacementCommand,
) (*arena.ReplayReplacement, bool, error) {
	s.appendCall("replace")
	if s.replaceErr != nil {
		return nil, false, s.replaceErr
	}
	return s.replacement, true, nil
}

func (s *replayPipelineSpy) appendCall(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.order = append(s.order, call)
}

func (s *replayPipelineSpy) callOrder() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

func task040ReplayReplacementFixture(
	t *testing.T,
	now time.Time,
	activeIndex int,
) (arena.ReplayReplacementAuthority, arena.ReplayReplacementCommand) {
	t.Helper()

	snapshots := []domain.ArenaTaskSnapshot{
		task040Snapshot(t, 0), task040Snapshot(t, 1), task040Snapshot(t, 2),
	}
	failedAuthority, failedCommand := task040FailedAttemptFixture(t, now)
	failedAuthority.ActiveSnapshotID = snapshots[activeIndex].SnapshotID
	failedCommand.Expected.SnapshotID = failedAuthority.ActiveSnapshotID
	task040SetAttemptIndex(t, &failedAuthority, &failedCommand, activeIndex)
	failedRepository := &failedAttemptRepositoryFake{authority: failedAuthority}
	failed, changed, err := arena.NewFailedAttemptUseCase(
		failedRepository,
		fixedArenaClock{now: now},
	).Terminalize(t.Context(), failedCommand)
	require.NoError(t, err)
	require.True(t, changed)

	closeAuthority := arena.OldWaveCloseAuthority{
		Scope: arena.OldWaveScope{
			TournamentID: failed.Scope.TournamentID,
			WaveID:       failed.Scope.WaveID,
		},
		Revision: 5, Wave: failedAuthority.Wave,
		Children: []arena.OldWaveChild{{
			SeriesID: failed.Scope.SeriesID, SlotID: failed.Scope.SlotID,
			GameID: failed.Scope.GameID, State: failed.Game.State,
			RouteID: failed.WaveRoute.ID,
		}},
	}
	closeCommand := arena.OldWaveCloseCommand{
		Scope: closeAuthority.Scope, CommandID: task040ID(500 + activeIndex*20),
		ExpectedWaveRevisionID: failedAuthority.Wave.RevisionID,
		ClosedWaveRevisionID:   domain.ArenaWaveRevisionID(task040ID(501 + activeIndex*20)),
	}
	closeRepository := &oldWaveCloseRepositoryFake{authority: closeAuthority}
	closure, changed, err := arena.NewOldWaveCloseUseCase(
		closeRepository,
		fixedArenaClock{now: now},
	).Close(t.Context(), closeCommand)
	require.NoError(t, err)
	require.True(t, changed)

	scope := arena.ReplayReplacementScope{
		TournamentID: failed.Scope.TournamentID, OldWaveID: failed.Scope.WaveID,
		SeriesID: failed.Scope.SeriesID, SlotID: failed.Scope.SlotID,
		AssignmentID: failed.Scope.AssignmentID,
	}
	authority := arena.ReplayReplacementAuthority{
		Scope: scope, Revision: 9, FailedAttempt: *failed, OldWaveClosure: *closure,
		ReserveChain: arena.ReplayReserveChain{
			AssignmentID: scope.AssignmentID, ActiveIndex: activeIndex, Snapshots: snapshots,
		},
		ParticipantIDs: [2]uuid.UUID{
			failed.Series.Series.FirstParticipantID,
			failed.Series.Series.SecondParticipantID,
		},
	}
	base := 600 + activeIndex*20
	command := arena.ReplayReplacementCommand{
		Scope: scope, CommandID: task040ID(base),
		ExpectedClosureRevisionID: closure.Wave.RevisionID,
		AssignmentAttemptID:       task040ID(base + 1), GameID: task040ID(base + 2),
		WaveID:                task040ID(base + 3),
		WaveRevisionID:        domain.ArenaWaveRevisionID(task040ID(base + 4)),
		ReadyWindowID:         task040ID(base + 5),
		ReadyWindowRevisionID: domain.ArenaReadyWindowRevisionID(task040ID(base + 6)),
	}
	return authority, command
}

func task040SetAttemptIndex(
	t *testing.T,
	authority *arena.FailedAttemptAuthority,
	command *arena.FailedAttemptCommand,
	activeIndex int,
) {
	t.Helper()

	slot := &authority.Series.Series.Slots[0]
	slot.Attempts = make([]domain.ArenaGame, activeIndex+1)
	authority.CurrentGameResultRevisionIDs = nil
	for index := range slot.Attempts {
		gameID := task040ID(400 + activeIndex*20 + index)
		slot.Attempts[index] = domain.ArenaGame{
			ID: gameID, SlotID: slot.ID, AttemptNo: index + 1,
			State: domain.ArenaGameStateActive,
		}
		if index < activeIndex {
			revisionID := domain.ArenaOfficialResultRevisionID(task040ID(450 + activeIndex*20 + index))
			slot.Attempts[index].State = domain.ArenaGameStateVoid
			slot.Attempts[index].ResultReason = domain.ArenaGameResultReasonNoSolve
			slot.Attempts[index].ResultRevisionID = &revisionID
			authority.CurrentGameResultRevisionIDs = append(
				authority.CurrentGameResultRevisionIDs,
				revisionID,
			)
		}
	}
	current := slot.Attempts[len(slot.Attempts)-1]
	authority.Scope.GameID = current.ID
	authority.Scope.AssignmentAttemptID = task040ID(480 + activeIndex)
	authority.CurrentOrdinal = 1 + activeIndex*2
	command.Scope = authority.Scope
	command.Expected.AttemptNo = current.AttemptNo
	require.NoError(t, authority.Series.Validate())
}

func task040Snapshot(t *testing.T, index int) domain.ArenaTaskSnapshot {
	t.Helper()

	task := domain.Task{
		ID: task040ID(700 + index), Title: fmt.Sprintf("Replay task %d", index+1),
		Description: "Solve the isolated replay task.", Category: domain.CategoryWeb,
		Difficulty: domain.DifficultyHard, TimeLimit: 180,
		Flag:  fmt.Sprintf("FLAG{replay_%d}", index+1),
		Hints: []string{"first hint", "second hint"},
	}
	snapshot, err := domain.NewArenaTaskSnapshot(
		task040ID(710+index),
		index+1,
		domain.ArenaTaskKindNormal,
		task,
	)
	require.NoError(t, err)
	return snapshot
}

func task040FailedCommandFromAuthority(
	authority arena.ReplayReplacementAuthority,
) arena.FailedAttemptCommand {
	failed := authority.FailedAttempt
	return arena.FailedAttemptCommand{
		Scope: failed.Scope, CommandID: failed.CommandID,
		FailureClass: failed.Failure.Class,
		Expected: arena.FailedAttemptExpectation{
			AttemptNo: failed.Game.AttemptNo, State: domain.ArenaGameStateActive,
			SnapshotID: failed.ActiveSnapshotID, Category: failed.Failure.CategoryCutoff,
		},
		Revisions: arena.FailedAttemptRevisionSet{
			GameResultRevisionID: failed.GameResultRevision.ID,
			ScoreRevisionID:      failed.ScoreRevision.ID,
			RouteEvidenceID:      failed.WaveRoute.ID,
			AuditEventID:         failed.Evidence.AuditEventID,
			OutboxEventID:        failed.Evidence.OutboxEventID,
			ProjectionRevisionID: failed.Evidence.ProjectionRevisionID,
		},
	}
}

var _ arena.ReplayReplacementRepository = (*replayReplacementRepositoryFake)(nil)
var _ arena.FailedAttemptTerminalizer = (*replayPipelineSpy)(nil)
var _ arena.OldWaveCloser = (*replayPipelineSpy)(nil)
var _ arena.ReplayReplacementPlanner = (*replayPipelineSpy)(nil)
