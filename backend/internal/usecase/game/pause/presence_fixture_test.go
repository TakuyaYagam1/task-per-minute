package pause_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

type pausedPresenceRepositoryHarness struct {
	*gamemocks.MockPausedPresenceRepository

	mu                      sync.Mutex
	authority               gameusecase.PausedPresenceAuthority
	commands                map[uuid.UUID]gameusecase.PausedPresenceRecord
	conflictOnce            bool
	conflictsRemaining      int
	loadErr                 error
	loadCount               int
	writes                  int
	commitCalls             int
	loadStarted             chan struct{}
	loadRelease             chan struct{}
	loadOnce                sync.Once
	replacePresenceOnCommit bool
}

func newPausedPresenceRepositoryHarness(
	t *testing.T,
	authority gameusecase.PausedPresenceAuthority,
) *pausedPresenceRepositoryHarness {
	t.Helper()
	harness := &pausedPresenceRepositoryHarness{
		authority: clonePausedPresenceAuthority(authority),
		commands:  make(map[uuid.UUID]gameusecase.PausedPresenceRecord),
	}
	repository := gamemocks.NewMockPausedPresenceRepository(t)
	repository.EXPECT().
		FindPausedPresenceCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.findPausedPresenceCommand).
		Maybe()
	repository.EXPECT().
		LoadPausedPresenceAuthority(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.loadPausedPresenceAuthority).
		Maybe()
	repository.EXPECT().
		CommitPausedPresence(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.commitPausedPresence).
		Maybe()
	harness.MockPausedPresenceRepository = repository
	return harness
}

func (f *pausedPresenceRepositoryHarness) findPausedPresenceCommand(_ context.Context, tournamentID, commandID uuid.UUID) (*gameusecase.PausedPresenceRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.commands[commandID]
	if !ok || record.Command.Scope.TournamentID != tournamentID {
		return nil, nil
	}
	clone := clonePausedPresenceRecord(record)
	return &clone, nil
}

func (f *pausedPresenceRepositoryHarness) loadPausedPresenceAuthority(_ context.Context, _ pausedomain.GraphScope, _ uuid.UUID) (gameusecase.PausedPresenceAuthority, error) {
	f.mu.Lock()
	f.loadCount++
	if f.loadErr != nil {
		f.mu.Unlock()
		return gameusecase.PausedPresenceAuthority{}, f.loadErr
	}
	started, release := f.loadStarted, f.loadRelease
	f.mu.Unlock()
	if started != nil {
		f.loadOnce.Do(func() { close(started) })
		<-release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return clonePausedPresenceAuthority(f.authority), nil
}

func (f *pausedPresenceRepositoryHarness) commitPausedPresence(_ context.Context, expected gameusecase.PausedPresenceExpectation, record gameusecase.PausedPresenceRecord) (*gameusecase.PausedPresenceRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitCalls++
	if existing, ok := f.commands[record.Command.CommandID]; ok {
		clone := clonePausedPresenceRecord(existing)
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
	current := f.authority
	if f.replacePresenceOnCommit {
		current.Presence.ID = uuid.New()
		f.authority = current
	}
	presenceRevision := gameusecase.PausePresenceRevision{
		ID: current.Presence.ID, TournamentID: current.Presence.TournamentID, RosterID: current.Presence.RosterID,
		SeriesID: current.Presence.SeriesID, ParticipantID: current.Presence.ParticipantID,
		PresenceEpoch: current.Presence.PresenceEpoch, Revision: current.Presence.Revision,
	}
	if expected.PauseID != current.Pause.PauseID || expected.GraphRevision != current.Pause.Graph.Revision ||
		expected.PauseRevision != current.Pause.Revision || expected.Presence != presenceRevision || expected.Authority != current.Pause.Scope.Authority {
		return nil, false, domain.ErrConflict
	}
	f.writes++
	f.authority = clonePausedPresenceAuthority(record.Authority)
	f.commands[record.Command.CommandID] = clonePausedPresenceRecord(record)
	clone := clonePausedPresenceRecord(record)
	return &clone, true, nil
}

func (f *pausedPresenceRepositoryHarness) blockNextLoad() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadStarted = make(chan struct{})
	f.loadRelease = make(chan struct{})
}

func (f *pausedPresenceRepositoryHarness) storeCommand(record gameusecase.PausedPresenceRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands[record.Command.CommandID] = clonePausedPresenceRecord(record)
}

func (f *pausedPresenceRepositoryHarness) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func pausedPresenceFixture(t *testing.T, now time.Time) (gameusecase.PausedPresenceAuthority, gameusecase.PausedPresenceCommand) {
	t.Helper()
	authority, pauseCommand := normalPauseFixture(now.Add(-time.Minute))
	repository := newNormalPauseRepositoryHarness(t, authority)
	useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, now.Add(-time.Minute)))
	pauseRecord, changed, err := useCase.Enter(t.Context(), pauseCommand)
	if err != nil || !changed {
		t.Fatalf("enter fixture pause: error = %v, changed = %v", err, changed)
	}
	presence := pauseRecord.Graph.Presence[0]
	authorityRecord := gameusecase.PausedPresenceAuthority{
		Pause: *pauseRecord, Presence: presence,
		Reconnect:              append([]pausedomain.PauseReconnectInterval(nil), pauseRecord.Graph.Reconnect...),
		Counters:               append([]pausedomain.PauseReconnectCounter(nil), pauseRecord.Graph.Counters...),
		FrozenDeadlines:        append([]gameusecase.PauseFrozenDeadline(nil), pauseRecord.Graph.FrozenDeadlines...),
		TerminalActionRevision: 8,
	}
	command := gameusecase.PausedPresenceCommand{
		Scope: pauseRecord.Scope, PauseID: pauseRecord.PauseID, CommandID: uuid.New(), ParticipantID: presence.ParticipantID,
		ExpectedGraphRevision: pauseRecord.Graph.Revision, ExpectedPauseRevision: pauseRecord.Revision,
		ExpectedPresenceEpoch: presence.PresenceEpoch, ExpectedPresenceRevision: presence.Revision,
		NextState: pausedomain.PresenceStateDisconnected,
	}
	return authorityRecord, command
}

func clonePausedPresenceAuthority(value gameusecase.PausedPresenceAuthority) gameusecase.PausedPresenceAuthority {
	value.Pause = cloneNormalPauseRecord(value.Pause)
	value.Presence = clonePausePresence(value.Presence)
	value.Reconnect = append([]pausedomain.PauseReconnectInterval(nil), value.Reconnect...)
	for index := range value.Reconnect {
		if value.Reconnect[index].ClosedAt != nil {
			closedAt := *value.Reconnect[index].ClosedAt
			value.Reconnect[index].ClosedAt = &closedAt
		}
	}
	value.Counters = append([]pausedomain.PauseReconnectCounter(nil), value.Counters...)
	value.FrozenDeadlines = append([]gameusecase.PauseFrozenDeadline(nil), value.FrozenDeadlines...)
	for index := range value.FrozenDeadlines {
		if value.FrozenDeadlines[index].ResumedAt != nil {
			resumedAt := *value.FrozenDeadlines[index].ResumedAt
			value.FrozenDeadlines[index].ResumedAt = &resumedAt
		}
		if value.FrozenDeadlines[index].ResumedDeadline != nil {
			deadline := *value.FrozenDeadlines[index].ResumedDeadline
			value.FrozenDeadlines[index].ResumedDeadline = &deadline
		}
	}
	return value
}

func clonePausedPresenceRecord(value gameusecase.PausedPresenceRecord) gameusecase.PausedPresenceRecord {
	value.Authority = clonePausedPresenceAuthority(value.Authority)
	return value
}

func clonePausePresence(value pausedomain.PausePresence) pausedomain.PausePresence {
	if value.DisconnectedAt != nil {
		at := *value.DisconnectedAt
		value.DisconnectedAt = &at
	}
	return value
}

func pausedPresenceImmutableEqual(first, second gameusecase.PausedPresenceAuthority) bool {
	return reflect.DeepEqual(first.Pause, second.Pause) && reflect.DeepEqual(first.Reconnect, second.Reconnect) &&
		reflect.DeepEqual(first.Counters, second.Counters) && reflect.DeepEqual(first.FrozenDeadlines, second.FrozenDeadlines) &&
		first.TerminalActionRevision == second.TerminalActionRevision
}
