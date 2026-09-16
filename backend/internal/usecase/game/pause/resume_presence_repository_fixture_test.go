package pause_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

type pauseResumePresenceRepositoryHarness struct {
	*gamemocks.MockPauseResumePresenceRepository

	mu                                                 sync.Mutex
	authority                                          gameusecase.PauseResumePresenceAuthority
	commands                                           map[uuid.UUID]gameusecase.PauseResumePresenceRecord
	conflictsRemaining, loadCount, writes, commitCalls int
	gameDecisionWrites, seriesDecisionWrites           int
	mutateCommitInPlace                                bool
	mutateCommitResult                                 func(*gameusecase.PauseResumePresenceRecord)
	lastGraph                                          gameusecase.PauseGraph
	normalPauseState                                   gameusecase.PauseState
	loadStarted, loadRelease                           chan struct{}
	loadOnce                                           sync.Once
}

func newPauseResumePresenceRepositoryHarness(
	t *testing.T,
	authority gameusecase.PauseResumePresenceAuthority,
) *pauseResumePresenceRepositoryHarness {
	t.Helper()
	harness := &pauseResumePresenceRepositoryHarness{
		authority: clonePauseResumePresenceAuthority(authority),
		commands:  make(map[uuid.UUID]gameusecase.PauseResumePresenceRecord),
	}
	repository := gamemocks.NewMockPauseResumePresenceRepository(t)
	repository.EXPECT().
		FindPauseResumePresenceCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.findPauseResumePresenceCommand).
		Maybe()
	repository.EXPECT().
		LoadPauseResumePresenceAuthority(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.loadPauseResumePresenceAuthority).
		Maybe()
	repository.EXPECT().
		CommitPauseResumePresence(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.commitPauseResumePresence).
		Maybe()
	harness.MockPauseResumePresenceRepository = repository
	return harness
}

func (f *pauseResumePresenceRepositoryHarness) findPauseResumePresenceCommand(_ context.Context, tournamentID, commandID uuid.UUID) (*gameusecase.PauseResumePresenceRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.commands[commandID]
	if !ok || record.Command.Resume.Scope.TournamentID != tournamentID {
		return nil, nil
	}
	clone := clonePauseResumePresenceRecord(record)
	return &clone, nil
}

func (f *pauseResumePresenceRepositoryHarness) loadPauseResumePresenceAuthority(_ context.Context, _ pausedomain.GraphScope, _, _, _ uuid.UUID) (gameusecase.PauseResumePresenceAuthority, error) {
	f.mu.Lock()
	f.loadCount++
	started, release := f.loadStarted, f.loadRelease
	f.mu.Unlock()
	if started != nil {
		f.loadOnce.Do(func() { close(started) })
		<-release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return clonePauseResumePresenceAuthority(f.authority), nil
}

func (f *pauseResumePresenceRepositoryHarness) commitPauseResumePresence(_ context.Context, expected gameusecase.PauseResumePresenceExpectation, record gameusecase.PauseResumePresenceRecord) (*gameusecase.PauseResumePresenceRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitCalls++
	if existing, ok := f.commands[record.Command.Resume.CommandID]; ok {
		clone := clonePauseResumePresenceRecord(existing)
		return &clone, false, nil
	}
	if f.conflictsRemaining > 0 {
		f.conflictsRemaining--
		return nil, false, domain.ErrConflict
	}
	if !reflect.DeepEqual(expected, gameusecase.PauseResumePresenceExpectationFrom(f.authority)) {
		return nil, false, domain.ErrConflict
	}
	if f.mutateCommitInPlace {
		expected.Resume.Presence[0].Revision++
		record.Graph.Tournament.CreatedAt = record.Graph.Tournament.CreatedAt.Add(time.Second)
	}
	f.writes++
	f.commands[record.Command.Resume.CommandID] = clonePauseResumePresenceRecord(record)
	f.authority.GameDecision.DecisionNumber = record.GameDecision.DecisionNumber
	f.authority.GameDecision.CurrentRevisionID = record.GameDecision.ID
	f.authority.GameDecision.Revision++
	f.authority.GameDecision.State = record.GamePauseState
	gameClock := clonePauseResumeGameClock(record.GameClock)
	f.authority.GameDecision.GameClock = &gameClock
	f.gameDecisionWrites++
	if record.SeriesDecision != nil {
		f.authority.SeriesDecision.DecisionNumber = record.SeriesDecision.DecisionNumber
		f.authority.SeriesDecision.CurrentRevisionID = record.SeriesDecision.ID
		f.authority.SeriesDecision.Revision++
		f.authority.SeriesDecision.State = record.SeriesPauseState
		f.seriesDecisionWrites++
	}
	f.lastGraph = clonePauseGraph(record.Graph)
	f.normalPauseState = record.NormalPauseState
	f.authority.Resume.Pause.State = record.NormalPauseState
	if record.NormalPauseState == gameusecase.PauseStateResumed {
		f.authority.Resume.Pause.Graph = clonePauseGraph(record.Graph)
		f.authority.Resume.Pause.Revision++
		resolvedAt := record.DecidedAt
		f.authority.Resume.Pause.ResolvedAt = &resolvedAt
	}
	clone := clonePauseResumePresenceRecord(record)
	if f.mutateCommitResult != nil {
		f.mutateCommitResult(&clone)
	}
	return &clone, true, nil
}

func (f *pauseResumePresenceRepositoryHarness) blockNextLoad() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadStarted = make(chan struct{})
	f.loadRelease = make(chan struct{})
}
func (f *pauseResumePresenceRepositoryHarness) storeCommand(record gameusecase.PauseResumePresenceRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands[record.Command.Resume.CommandID] = clonePauseResumePresenceRecord(record)
}
func (f *pauseResumePresenceRepositoryHarness) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func (f *pauseResumePresenceRepositoryHarness) gameDecisionWriteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gameDecisionWrites
}

func (f *pauseResumePresenceRepositoryHarness) seriesDecisionWriteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seriesDecisionWrites
}
