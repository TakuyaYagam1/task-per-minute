package pause_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/mocks"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

type pauseResumeRepositoryHarness struct {
	*gamemocks.MockPauseResumeRepository

	mu                 sync.Mutex
	authority          gameusecase.PauseResumeAuthority
	commands           map[uuid.UUID]gameusecase.PauseResumeRecord
	conflictOnce       bool
	conflictsRemaining int
	loadErr            error
	loadCount          int
	writes             int
	commitCalls        int
	loadStarted        chan struct{}
	loadRelease        chan struct{}
	loadOnce           sync.Once
}

func newPauseResumeRepositoryHarness(
	t *testing.T,
	authority gameusecase.PauseResumeAuthority,
) *pauseResumeRepositoryHarness {
	t.Helper()
	harness := &pauseResumeRepositoryHarness{
		authority: clonePauseResumeAuthority(authority),
		commands:  make(map[uuid.UUID]gameusecase.PauseResumeRecord),
	}
	repository := gamemocks.NewMockPauseResumeRepository(t)
	repository.EXPECT().
		FindPauseResumeCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.findPauseResumeCommand).
		Maybe()
	repository.EXPECT().
		LoadPauseResumeAuthority(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.loadPauseResumeAuthority).
		Maybe()
	repository.EXPECT().
		CommitPauseResume(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.commitPauseResume).
		Maybe()
	harness.MockPauseResumeRepository = repository
	return harness
}

func (f *pauseResumeRepositoryHarness) findPauseResumeCommand(_ context.Context, tournamentID, commandID uuid.UUID) (*gameusecase.PauseResumeRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.commands[commandID]
	if !ok || record.Scope.TournamentID != tournamentID {
		return nil, nil
	}
	clone := clonePauseResumeRecord(record)
	return &clone, nil
}

func (f *pauseResumeRepositoryHarness) loadPauseResumeAuthority(_ context.Context, _ pausedomain.GraphScope, _ uuid.UUID) (gameusecase.PauseResumeAuthority, error) {
	f.mu.Lock()
	f.loadCount++
	if f.loadErr != nil {
		f.mu.Unlock()
		return gameusecase.PauseResumeAuthority{}, f.loadErr
	}
	started, release := f.loadStarted, f.loadRelease
	f.mu.Unlock()
	if started != nil {
		f.loadOnce.Do(func() { close(started) })
		<-release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return clonePauseResumeAuthority(f.authority), nil
}

func (f *pauseResumeRepositoryHarness) commitPauseResume(_ context.Context, expected gameusecase.PauseResumeExpectation, record gameusecase.PauseResumeRecord) (*gameusecase.PauseResumeRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitCalls++
	if existing, ok := f.commands[record.CommandID]; ok {
		clone := clonePauseResumeRecord(existing)
		return &clone, false, nil
	}
	if f.conflictOnce {
		f.conflictOnce = false
		return nil, false, domain.ErrConflict
	}
	if f.conflictsRemaining > 0 {
		f.conflictsRemaining--
		return nil, false, domain.ErrConflict
	}
	current := gameusecase.PauseResumeExpectationFrom(f.authority)
	normalizePauseResumeExpectation(&expected)
	normalizePauseResumeExpectation(&current)
	if !reflect.DeepEqual(expected, current) {
		return nil, false, domain.ErrConflict
	}
	f.writes++
	f.commands[record.CommandID] = clonePauseResumeRecord(record)
	f.authority.Pause = gameusecase.NormalPauseRecord{Scope: record.Scope, PauseID: record.PauseID, State: record.State, Revision: record.Revision, Graph: clonePauseGraph(record.Graph)}
	clone := clonePauseResumeRecord(record)
	return &clone, true, nil
}

func normalizePauseResumeExpectation(value *gameusecase.PauseResumeExpectation) {
	if len(value.Series) == 0 {
		value.Series = nil
	}
	if len(value.Games) == 0 {
		value.Games = nil
	}
	if len(value.Presence) == 0 {
		value.Presence = nil
	}
	if len(value.Reconnect) == 0 {
		value.Reconnect = nil
	}
	if len(value.Counters) == 0 {
		value.Counters = nil
	}
	if len(value.FrozenDeadlines) == 0 {
		value.FrozenDeadlines = nil
	}
}

func (f *pauseResumeRepositoryHarness) blockNextLoad() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadStarted = make(chan struct{})
	f.loadRelease = make(chan struct{})
}

func (f *pauseResumeRepositoryHarness) storeCommand(record gameusecase.PauseResumeRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands[record.CommandID] = clonePauseResumeRecord(record)
}

func (f *pauseResumeRepositoryHarness) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func pauseResumeFixture(t *testing.T, now time.Time) (gameusecase.PauseResumeAuthority, gameusecase.PauseResumeCommand) {
	t.Helper()
	authority, command := normalPauseFixture(now.Add(-2 * time.Minute))
	repository := newNormalPauseRepositoryHarness(t, authority)
	useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, now.Add(-2*time.Minute)))
	pauseRecord, changed, err := useCase.Enter(t.Context(), command)
	if err != nil || !changed {
		t.Fatalf("enter fixture pause: error = %v, changed = %v", err, changed)
	}
	resumeAuthority := gameusecase.PauseResumeAuthority{
		Pause:                  *pauseRecord,
		Presence:               append([]pausedomain.PausePresence(nil), pauseRecord.Graph.Presence...),
		Reconnect:              append([]pausedomain.PauseReconnectInterval(nil), pauseRecord.Graph.Reconnect...),
		Counters:               append([]pausedomain.PauseReconnectCounter(nil), pauseRecord.Graph.Counters...),
		FrozenDeadlines:        append([]gameusecase.PauseFrozenDeadline(nil), pauseRecord.Graph.FrozenDeadlines...),
		TerminalActionRevision: pauseRecord.Graph.TerminalActionRevision,
	}
	resumeCommand := gameusecase.PauseResumeCommand{
		Scope: pauseRecord.Scope, PauseID: pauseRecord.PauseID, CommandID: uuid.New(), ActorID: uuid.New(),
		Expected: gameusecase.PauseResumeExpectationFrom(resumeAuthority),
	}
	return resumeAuthority, resumeCommand
}

func readyWindowPauseResumeFixture(t *testing.T, resumedAt time.Time) (gameusecase.PauseResumeAuthority, gameusecase.PauseResumeCommand) {
	t.Helper()
	pausedAt := resumedAt.Add(-2 * time.Minute)
	authority, command := normalPauseFixture(pausedAt)
	windowOpenedAt := pausedAt.Add(-time.Minute)
	windowDeadline := pausedAt.Add(3 * time.Minute)
	authority.Graph.Wave.Wave.State = domain.WaveStateReadyWindowOpen
	authority.Graph.Wave.Wave.StartedAt = nil
	authority.Graph.Wave.Wave.PausedAt = nil
	for index := range authority.Graph.Wave.Wave.Members {
		authority.Graph.Wave.Wave.Members[index].Ready = false
	}
	authority.Graph.Wave.Wave.ReadyWindow.State = domain.ReadyWindowStateOpen
	authority.Graph.Wave.Wave.ReadyWindow.OpenedAt = windowOpenedAt
	authority.Graph.Wave.Wave.ReadyWindow.Deadline = windowDeadline
	authority.Graph.Wave.Wave.ReadyWindow.ConsumedAt = nil
	authority.Graph.Series[0].Execution.Series.State = domain.SeriesStatePlanned
	authority.Graph.Series[0].CurrentGameID = nil
	authority.Graph.Series[0].Execution.Series.Slots[0].Attempts[0].State = domain.GameStatePlanned
	authority.Graph.Games = nil
	authority.Graph.Reconnect = nil
	authority.Graph.Counters[0].Used = 0
	refreshNormalPauseRevisions(&authority, &command)
	return pauseResumeFromNormalAuthority(t, authority, command, pausedAt)
}

func draftPauseResumeFixture(t *testing.T, resumedAt time.Time) (gameusecase.PauseResumeAuthority, gameusecase.PauseResumeCommand) {
	t.Helper()
	pausedAt := resumedAt.Add(-2 * time.Minute)
	authority, command := draftNormalPauseFixture(t, pausedAt)
	return pauseResumeFromNormalAuthority(t, authority, command, pausedAt)
}

func draftNormalPauseFixture(t *testing.T, pausedAt time.Time) (gameusecase.NormalPauseAuthority, gameusecase.NormalPauseCommand) {
	t.Helper()
	authority, command := normalPauseFixture(pausedAt)
	draft := task030Draft(t, pausedAt)
	series := &authority.Graph.Series[0]
	series.Execution.Series.ID = draft.SeriesID
	series.Execution.Series.FirstParticipantID = draft.FirstParticipantID
	series.Execution.Series.SecondParticipantID = draft.SecondParticipantID
	series.Execution.Series.State = domain.SeriesStateDraft
	series.Execution.Series.Slots[0].SeriesID = draft.SeriesID
	series.Execution.Series.Slots[0].Attempts[0].State = domain.GameStatePlanned
	series.CurrentGameID = nil
	authority.Graph.Wave.Wave.Members[0].ParticipantID = draft.FirstParticipantID
	authority.Graph.Wave.Wave.Members[1].ParticipantID = draft.SecondParticipantID
	authority.Graph.Presence[0].SeriesID = draft.SeriesID
	authority.Graph.Presence[0].ParticipantID = draft.FirstParticipantID
	authority.Graph.Presence[1].SeriesID = draft.SeriesID
	authority.Graph.Presence[1].ParticipantID = draft.SecondParticipantID
	authority.Graph.Games = nil
	authority.Graph.Reconnect = nil
	authority.Graph.Counters[0].ParticipantID = draft.FirstParticipantID
	authority.Graph.Counters[0].Used = 0
	authority.Graph.Draft = &draft
	refreshNormalPauseRevisions(&authority, &command)
	command.DraftResultRevisionID = uuid.New()
	return authority, command
}

func draftRevisionTwoNormalPauseFixture(t *testing.T, pausedAt time.Time) (gameusecase.NormalPauseAuthority, gameusecase.NormalPauseCommand) {
	t.Helper()
	authority, command := draftNormalPauseFixture(t, pausedAt)
	initial := task030Draft(t, pausedAt.Add(-2*time.Second))
	repository := newPauseDraftRepositoryHarness(t, initial)
	useCase := draftusecase.NewActionUseCase(
		repository.mock,
		newPauseDraftClock(t, pausedAt.Add(-time.Second)),
	)
	result, err := useCase.Apply(t.Context(), task030ActionCommand(initial, uuid.New(), domain.CategoryWeb))
	if err != nil || !result.Changed || result.Draft.Revision != 2 {
		t.Fatalf("prepare revision-two Draft: result = %+v, error = %v", result, err)
	}
	series := &authority.Graph.Series[0].Execution.Series
	series.FirstParticipantID = result.Draft.FirstParticipantID
	series.SecondParticipantID = result.Draft.SecondParticipantID
	authority.Graph.Wave.Wave.Members[0].ParticipantID = result.Draft.FirstParticipantID
	authority.Graph.Wave.Wave.Members[1].ParticipantID = result.Draft.SecondParticipantID
	authority.Graph.Presence[0].ParticipantID = result.Draft.FirstParticipantID
	authority.Graph.Presence[1].ParticipantID = result.Draft.SecondParticipantID
	if result.Draft.SeriesID != series.ID || result.Draft.FirstParticipantID != series.FirstParticipantID ||
		result.Draft.SecondParticipantID != series.SecondParticipantID || result.Draft.State != draftusecase.ExecutionStateActive {
		t.Fatalf("revision-two Draft ownership = %+v, Series = %+v", result.Draft, *series)
	}
	authority.Graph.Draft = &result.Draft
	refreshNormalPauseRevisions(&authority, &command)
	command.DraftResultRevisionID = uuid.New()
	return authority, command
}

func completedDraftNormalPauseFixture(t *testing.T, pausedAt time.Time) (gameusecase.NormalPauseAuthority, gameusecase.NormalPauseCommand) {
	t.Helper()
	authority, command := draftNormalPauseFixture(t, pausedAt)
	startedAt := pausedAt.Add(-3 * time.Second)
	draft := completeTask030Draft(t, task030Draft(t, startedAt), startedAt)
	series := &authority.Graph.Series[0].Execution.Series
	series.FirstParticipantID = draft.FirstParticipantID
	series.SecondParticipantID = draft.SecondParticipantID
	authority.Graph.Wave.Wave.Members[0].ParticipantID = draft.FirstParticipantID
	authority.Graph.Wave.Wave.Members[1].ParticipantID = draft.SecondParticipantID
	authority.Graph.Presence[0].ParticipantID = draft.FirstParticipantID
	authority.Graph.Presence[1].ParticipantID = draft.SecondParticipantID
	authority.Graph.Draft = &draft
	refreshNormalPauseRevisions(&authority, &command)
	command.DraftResultRevisionID = uuid.New()
	return authority, command
}

func pauseResumeFromNormalAuthority(t *testing.T, authority gameusecase.NormalPauseAuthority, pauseCommand gameusecase.NormalPauseCommand, pausedAt time.Time) (gameusecase.PauseResumeAuthority, gameusecase.PauseResumeCommand) {
	t.Helper()
	repository := newNormalPauseRepositoryHarness(t, authority)
	useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt))
	pauseRecord, changed, err := useCase.Enter(t.Context(), pauseCommand)
	if err != nil || !changed {
		t.Fatalf("enter fixture pause: error = %v, changed = %v", err, changed)
	}
	resumeAuthority := pauseResumeAuthorityFromRecord(*pauseRecord)
	resumeCommand := gameusecase.PauseResumeCommand{
		Scope: pauseRecord.Scope, PauseID: pauseRecord.PauseID, CommandID: uuid.New(), ActorID: uuid.New(),
		Expected: gameusecase.PauseResumeExpectationFrom(resumeAuthority),
	}
	if resumeAuthority.Pause.Graph.Draft != nil {
		resumeCommand.DraftResultRevisionID = uuid.New()
	}
	return resumeAuthority, resumeCommand
}

func pauseResumeAuthorityFromRecord(record gameusecase.NormalPauseRecord) gameusecase.PauseResumeAuthority {
	return gameusecase.PauseResumeAuthority{
		Pause:                  record,
		Presence:               append([]pausedomain.PausePresence(nil), record.Graph.Presence...),
		Reconnect:              append([]pausedomain.PauseReconnectInterval(nil), record.Graph.Reconnect...),
		Counters:               append([]pausedomain.PauseReconnectCounter(nil), record.Graph.Counters...),
		FrozenDeadlines:        append([]gameusecase.PauseFrozenDeadline(nil), record.Graph.FrozenDeadlines...),
		TerminalActionRevision: record.Graph.TerminalActionRevision,
	}
}

func clonePauseResumeAuthority(value gameusecase.PauseResumeAuthority) gameusecase.PauseResumeAuthority {
	value.Pause = cloneNormalPauseRecord(value.Pause)
	value.Presence = append([]pausedomain.PausePresence(nil), value.Presence...)
	value.Reconnect = append([]pausedomain.PauseReconnectInterval(nil), value.Reconnect...)
	value.Counters = append([]pausedomain.PauseReconnectCounter(nil), value.Counters...)
	value.FrozenDeadlines = append([]gameusecase.PauseFrozenDeadline(nil), value.FrozenDeadlines...)
	return value
}

func clonePauseResumeRecord(value gameusecase.PauseResumeRecord) gameusecase.PauseResumeRecord {
	value.Expected = clonePauseResumeExpectation(value.Expected)
	value.Graph = clonePauseGraph(value.Graph)
	return value
}

func clonePauseResumeExpectation(value gameusecase.PauseResumeExpectation) gameusecase.PauseResumeExpectation {
	value.Games = append([]gameusecase.PauseChildRevision(nil), value.Games...)
	value.Series = append([]gameusecase.PauseChildRevision(nil), value.Series...)
	value.Presence = append([]gameusecase.PausePresenceRevision(nil), value.Presence...)
	value.Reconnect = append([]gameusecase.PauseChildRevision(nil), value.Reconnect...)
	value.Counters = append([]gameusecase.PauseReconnectCounterRevision(nil), value.Counters...)
	value.FrozenDeadlines = append([]gameusecase.PauseFrozenDeadlineRevision(nil), value.FrozenDeadlines...)
	if value.Draft != nil {
		draft := *value.Draft
		value.Draft = &draft
	}
	return value
}
