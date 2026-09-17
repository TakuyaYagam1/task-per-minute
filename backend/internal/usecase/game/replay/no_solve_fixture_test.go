package replay_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	attemptusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/attempt"
	closeusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/close"
	replayusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/replay"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/replay/mocks"
)

type replayReplacementRepositoryState struct {
	mu        sync.Mutex
	authority replayusecase.ReplayReplacementAuthority
	writes    int
}

type replayReplacementRepositoryHarness struct {
	*gamemocks.MockReplayReplacementRepository

	state *replayReplacementRepositoryState
}

func newReplayReplacementRepositoryHarness(
	t *testing.T,
	authority replayusecase.ReplayReplacementAuthority,
) *replayReplacementRepositoryHarness {
	t.Helper()
	state := &replayReplacementRepositoryState{authority: authority}
	repository := gamemocks.NewMockReplayReplacementRepository(t)
	repository.EXPECT().
		LoadReplayReplacementAuthority(mock.Anything, mock.Anything).
		RunAndReturn(state.loadAuthority).
		Maybe()
	repository.EXPECT().
		CommitReplayReplacement(mock.Anything, mock.Anything).
		RunAndReturn(state.commitReplacement).
		Maybe()
	return &replayReplacementRepositoryHarness{
		MockReplayReplacementRepository: repository,
		state:                           state,
	}
}

func (s *replayReplacementRepositoryState) loadAuthority(
	_ context.Context,
	_ replayusecase.ReplayReplacementScope,
) (replayusecase.ReplayReplacementAuthority, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authority, nil
}

func (s *replayReplacementRepositoryState) commitReplacement(
	_ context.Context,
	replacement replayusecase.ReplayReplacement,
) (*replayusecase.ReplayReplacement, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if replacement.ExpectedAuthorityRevision != s.authority.Revision || s.authority.Current != nil {
		return nil, false, domain.ErrConflict
	}
	stored := replacement
	s.authority.Revision++
	s.authority.ReserveChain.ActiveIndex = stored.ReservePosition - 1
	s.authority.Current = &stored
	s.writes++
	return &stored, true, nil
}

func (h *replayReplacementRepositoryHarness) writeCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.writes
}

type replayPipelineState struct {
	mu          sync.Mutex
	order       []string
	failed      *attemptusecase.AttemptRecord
	closure     *closeusecase.Closure
	replacement *replayusecase.ReplayReplacement
	replaceErr  error
}

func newReplayPipelineMocks(
	t *testing.T,
	state *replayPipelineState,
) (
	*gamemocks.MockFailedAttemptTerminalizer,
	*gamemocks.MockOldWaveCloser,
	*gamemocks.MockReplayReplacementPlanner,
) {
	t.Helper()
	terminalizer := gamemocks.NewMockFailedAttemptTerminalizer(t)
	terminalizer.EXPECT().
		Terminalize(mock.Anything, mock.Anything).
		RunAndReturn(state.terminalize).
		Maybe()
	closer := gamemocks.NewMockOldWaveCloser(t)
	closer.EXPECT().
		Close(mock.Anything, mock.Anything).
		RunAndReturn(state.closeWave).
		Maybe()
	replacer := gamemocks.NewMockReplayReplacementPlanner(t)
	replacer.EXPECT().
		Replace(mock.Anything, mock.Anything).
		RunAndReturn(state.replace).
		Maybe()
	return terminalizer, closer, replacer
}

func (s *replayPipelineState) terminalize(
	_ context.Context,
	_ attemptusecase.AttemptCommand,
) (*attemptusecase.AttemptRecord, bool, error) {
	s.appendCall("terminalize")
	return s.failed, true, nil
}

func (s *replayPipelineState) closeWave(
	_ context.Context,
	_ closeusecase.CloseCommand,
) (*closeusecase.Closure, bool, error) {
	s.appendCall("close")
	return s.closure, true, nil
}

func (s *replayPipelineState) replace(
	_ context.Context,
	_ replayusecase.ReplayReplacementCommand,
) (*replayusecase.ReplayReplacement, bool, error) {
	s.appendCall("replace")
	if s.replaceErr != nil {
		return nil, false, s.replaceErr
	}
	return s.replacement, true, nil
}

func (s *replayPipelineState) appendCall(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.order = append(s.order, call)
}

func (s *replayPipelineState) callOrder() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

func replayReplacementFixture(
	t *testing.T,
	now time.Time,
	activeIndex int,
) (replayusecase.ReplayReplacementAuthority, replayusecase.ReplayReplacementCommand) {
	t.Helper()

	snapshots := []domain.AssignmentTaskSnapshot{
		replaySnapshot(t, 0), replaySnapshot(t, 1), replaySnapshot(t, 2),
	}
	failedAuthority, failedCommand := replayFailedAttemptFixture(t, now)
	failedAuthority.ActiveSnapshotID = snapshots[activeIndex].SnapshotID
	failedCommand.Expected.SnapshotID = failedAuthority.ActiveSnapshotID
	replaySetAttemptIndex(t, &failedAuthority, &failedCommand, activeIndex)
	failed, err := attemptusecase.BuildRecord(
		failedCommand,
		failedAuthority,
		now.Round(0).UTC(),
	)
	require.NoError(t, err)
	require.NoError(t, failed.Validate())

	closeScope := closeusecase.CloseScope{
		TournamentID: failed.Scope.TournamentID,
		WaveID:       failed.Scope.WaveID,
	}
	children := []closeusecase.CloseChild{{
		SeriesID: failed.Scope.SeriesID, SlotID: failed.Scope.SlotID,
		GameID: failed.Scope.GameID, State: failed.Game.State,
		RouteID: failed.WaveRoute.ID,
	}}
	closeCommand := closeusecase.CloseCommand{
		Scope: closeScope, CommandID: replayID(500 + activeIndex*20),
		ExpectedWaveRevisionID: failedAuthority.Wave.RevisionID,
		ClosedWaveRevisionID:   domain.WaveRevisionID(replayID(501 + activeIndex*20)),
	}
	closedWave := failedAuthority.Wave
	closedWave.State = domain.WaveStateCompleted
	closedWave.RevisionID = closeCommand.ClosedWaveRevisionID
	closure := closeusecase.Closure{
		Scope: closeCommand.Scope, CommandID: closeCommand.CommandID,
		ExpectedAuthorityRevision: 5,
		PreviousWaveRevisionID:    closeCommand.ExpectedWaveRevisionID,
		Wave:                      closedWave,
		Children:                  append([]closeusecase.CloseChild(nil), children...),
		ClosedAt:                  now.Round(0).UTC(),
	}
	require.NoError(t, closure.Validate())

	scope := replayusecase.ReplayReplacementScope{
		TournamentID: failed.Scope.TournamentID, OldWaveID: failed.Scope.WaveID,
		SeriesID: failed.Scope.SeriesID, SlotID: failed.Scope.SlotID,
		AssignmentID: failed.Scope.AssignmentID,
	}
	authority := replayusecase.ReplayReplacementAuthority{
		Scope: scope, Revision: 9, FailedAttempt: failed, OldWaveClosure: closure,
		ReserveChain: replayusecase.ReplayReserveChain{
			AssignmentID: scope.AssignmentID, ActiveIndex: activeIndex, Snapshots: snapshots,
		},
		ParticipantIDs: [2]uuid.UUID{
			failed.Series.Series.FirstParticipantID,
			failed.Series.Series.SecondParticipantID,
		},
	}
	base := 600 + activeIndex*20
	command := replayusecase.ReplayReplacementCommand{
		Scope: scope, CommandID: replayID(base),
		ExpectedClosureRevisionID: closure.Wave.RevisionID,
		AssignmentAttemptID:       replayID(base + 1), GameID: replayID(base + 2),
		WaveID:                replayID(base + 3),
		WaveRevisionID:        domain.WaveRevisionID(replayID(base + 4)),
		ReadyWindowID:         replayID(base + 5),
		ReadyWindowRevisionID: domain.ReadyWindowRevisionID(replayID(base + 6)),
	}
	return authority, command
}

func replaySetAttemptIndex(
	t *testing.T,
	authority *attemptusecase.AttemptAuthority,
	command *attemptusecase.AttemptCommand,
	activeIndex int,
) {
	t.Helper()

	slot := &authority.Series.Series.Slots[0]
	slot.Attempts = make([]domain.Game, activeIndex+1)
	authority.CurrentGameResultRevisionIDs = nil
	for index := range slot.Attempts {
		gameID := replayID(400 + activeIndex*20 + index)
		slot.Attempts[index] = domain.Game{
			ID: gameID, SlotID: slot.ID, AttemptNo: index + 1,
			State: domain.GameStateActive,
		}
		if index < activeIndex {
			revisionID := domain.OfficialResultRevisionID(replayID(450 + activeIndex*20 + index))
			slot.Attempts[index].State = domain.GameStateVoid
			slot.Attempts[index].ResultReason = domain.GameResultReasonNoSolve
			slot.Attempts[index].ResultRevisionID = &revisionID
			authority.CurrentGameResultRevisionIDs = append(
				authority.CurrentGameResultRevisionIDs,
				revisionID,
			)
		}
	}
	current := slot.Attempts[len(slot.Attempts)-1]
	authority.Scope.GameID = current.ID
	authority.Scope.AssignmentAttemptID = replayID(480 + activeIndex)
	authority.CurrentOrdinal = 1 + activeIndex*2
	command.Scope = authority.Scope
	command.Expected.AttemptNo = current.AttemptNo
	require.NoError(t, authority.Series.Validate())
}

func replaySnapshot(t *testing.T, index int) domain.AssignmentTaskSnapshot {
	t.Helper()

	task := domain.Task{
		ID: replayID(700 + index), Title: fmt.Sprintf("Replay task %d", index+1),
		Description: "Solve the isolated replay task.", Category: domain.CategoryWeb,
		Difficulty: domain.DifficultyHard, TimeLimit: 180,
		Flag:  fmt.Sprintf("FLAG{replay_%d}", index+1),
		Hints: []string{"first hint", "second hint"},
	}
	snapshot, err := domain.NewAssignmentTaskSnapshot(
		replayID(710+index),
		index+1,
		domain.AssignmentTaskKindNormal,
		task,
	)
	require.NoError(t, err)
	return snapshot
}

func replayFailedCommandFromAuthority(
	authority replayusecase.ReplayReplacementAuthority,
) attemptusecase.AttemptCommand {
	failed := authority.FailedAttempt
	return attemptusecase.AttemptCommand{
		Scope: failed.Scope, CommandID: failed.CommandID,
		FailureClass: failed.Failure.Class,
		Expected: attemptusecase.Expectation{
			AttemptNo: failed.Game.AttemptNo, State: domain.GameStateActive,
			SnapshotID: failed.ActiveSnapshotID, Category: failed.Failure.CategoryCutoff,
		},
		Revisions: attemptusecase.AttemptRevisionSet{
			GameResultRevisionID: failed.AttemptGameResultRevision.ID,
			ScoreRevisionID:      failed.ScoreRevision.ID,
			RouteEvidenceID:      failed.WaveRoute.ID,
			AuditEventID:         failed.Evidence.AuditEventID,
			OutboxEventID:        failed.Evidence.OutboxEventID,
			ProjectionRevisionID: failed.Evidence.ProjectionRevisionID,
		},
	}
}

func replayFailedAttemptFixture(
	t *testing.T,
	now time.Time,
) (attemptusecase.AttemptAuthority, attemptusecase.AttemptCommand) {
	t.Helper()

	tournamentID := replayID(1)
	waveID := replayID(2)
	seriesID := replayID(3)
	slotID := replayID(4)
	gameID := replayID(5)
	participants := [2]uuid.UUID{replayID(6), replayID(7)}
	currentScoreRevisionID := domain.SeriesScoreRevisionID(replayID(8))
	series := seriesdomain.Execution{Series: domain.Series{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: participants[0], SecondParticipantID: participants[1],
		Format: domain.SeriesFormatBO3, State: domain.SeriesStateActive,
		CurrentScoreRevisionID: &currentScoreRevisionID,
		Slots: []domain.GameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.SeriesScore{},
			Attempts: []domain.Game{{
				ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStateActive,
			}},
		}},
	}}
	require.NoError(t, series.Validate())
	wave := replayActiveWave(t, tournamentID, waveID, participants, now)
	scope := domain.FailedAttemptScope{
		TournamentID: tournamentID, WaveID: waveID, SeriesID: seriesID,
		SlotID: slotID, GameID: gameID, AssignmentID: replayID(9),
		AssignmentAttemptID: replayID(10),
	}
	authority := attemptusecase.AttemptAuthority{
		Scope: scope, Revision: 7, Wave: wave, Series: series,
		ActiveSnapshotID: replayID(11), CurrentOrdinal: 1,
		CurrentProjectionRevision: 3,
	}
	command := attemptusecase.AttemptCommand{
		Scope: scope, CommandID: replayID(12), FailureClass: gamedomain.FailureNoSolve,
		Expected: attemptusecase.Expectation{
			AttemptNo: 1, State: domain.GameStateActive,
			SnapshotID: authority.ActiveSnapshotID, Category: domain.CategoryWeb,
		},
		Revisions: attemptusecase.AttemptRevisionSet{
			GameResultRevisionID: domain.OfficialResultRevisionID(replayID(13)),
			ScoreRevisionID:      domain.SeriesScoreRevisionID(replayID(14)),
			RouteEvidenceID:      replayID(15), AuditEventID: replayID(16),
			OutboxEventID: replayID(17), ProjectionRevisionID: replayID(18),
		},
	}
	return authority, command
}

func replayActiveWave(
	t *testing.T,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	participants [2]uuid.UUID,
	now time.Time,
) domain.Wave {
	t.Helper()

	wave := domain.Wave{
		ID: waveID, TournamentID: tournamentID,
		RevisionID: domain.WaveRevisionID(replayID(19)),
		State:      domain.WaveStatePlanned,
		Members: []domain.WaveMember{
			{ParticipantID: participants[0]}, {ParticipantID: participants[1]},
		},
	}
	openedAt := now.Add(-20 * time.Second)
	require.NoError(t, wave.OpenReadyWindow(
		replayID(20),
		domain.ReadyWindowRevisionID(replayID(21)),
		openedAt,
		now.Add(10*time.Second),
	))
	for _, participantID := range participants {
		_, err := wave.MarkReady(wave.ReadyWindow.ID, participantID, openedAt.Add(time.Second))
		require.NoError(t, err)
	}
	_, err := wave.Start(wave.ReadyWindow.ID, now.Add(-5*time.Second))
	require.NoError(t, err)
	require.NoError(t, wave.Validate())
	return wave
}

func replayID(value int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, fmt.Appendf(nil, "execution-replay-%d", value))
}
