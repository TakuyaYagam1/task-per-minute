package pause_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

func newNormalPauseRepositoryHarness(
	t *testing.T,
	authority gameusecase.NormalPauseAuthority,
) *normalPauseRepositoryHarness {
	t.Helper()
	harness := &normalPauseRepositoryHarness{
		authority: cloneNormalPauseAuthority(authority),
		commands:  make(map[uuid.UUID]gameusecase.NormalPauseRecord),
	}
	repository := gamemocks.NewMockNormalPauseRepository(t)
	repository.EXPECT().
		FindNormalPauseCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.findNormalPauseCommand).
		Maybe()
	repository.EXPECT().
		LoadNormalPauseAuthority(mock.Anything, mock.Anything).
		RunAndReturn(harness.loadNormalPauseAuthority).
		Maybe()
	repository.EXPECT().
		CommitNormalPause(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.commitNormalPause).
		Maybe()
	harness.MockNormalPauseRepository = repository
	return harness
}

func (f *normalPauseRepositoryHarness) findNormalPauseCommand(_ context.Context, tournamentID, commandID uuid.UUID) (*gameusecase.NormalPauseRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.commands[commandID]
	if !ok || record.Scope.TournamentID != tournamentID {
		return nil, nil
	}
	clone := cloneNormalPauseRecord(record)
	return &clone, nil
}

func (f *normalPauseRepositoryHarness) loadNormalPauseAuthority(_ context.Context, _ pausedomain.GraphScope) (gameusecase.NormalPauseAuthority, error) {
	f.mu.Lock()
	f.loadCount++
	if f.loadErr != nil {
		f.mu.Unlock()
		return gameusecase.NormalPauseAuthority{}, f.loadErr
	}
	started, release := f.loadStarted, f.loadRelease
	f.mu.Unlock()
	if started != nil {
		f.loadOnce.Do(func() { close(started) })
		<-release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneNormalPauseAuthority(f.authority), nil
}

func (f *normalPauseRepositoryHarness) commitNormalPause(_ context.Context, expected gameusecase.PauseGraphRevisions, record gameusecase.NormalPauseRecord) (*gameusecase.NormalPauseRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitCalls++
	if f.commitErr != nil {
		return nil, false, f.commitErr
	}
	if existing, ok := f.commands[record.CommandID]; ok {
		clone := cloneNormalPauseRecord(existing)
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
	if !reflect.DeepEqual(expected, f.authority.Revisions) {
		return nil, false, domain.ErrConflict
	}
	if f.mutateCommitInPlace {
		expected.Presence[0].Revision++
		record.Graph.Presence[0], record.Graph.Presence[1] = record.Graph.Presence[1], record.Graph.Presence[0]
	}
	f.writes++
	f.commands[record.CommandID] = cloneNormalPauseRecord(record)
	f.authority.Graph = clonePauseGraph(record.Graph)
	f.authority.Revisions = gameusecase.PauseGraphRevisionsFrom(record.Graph)
	clone := cloneNormalPauseRecord(record)
	return &clone, true, nil
}

func (f *normalPauseRepositoryHarness) blockNextLoad() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadStarted = make(chan struct{})
	f.loadRelease = make(chan struct{})
}

func (f *normalPauseRepositoryHarness) storeCommand(record gameusecase.NormalPauseRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands[record.CommandID] = cloneNormalPauseRecord(record)
}

func (f *normalPauseRepositoryHarness) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func (f *normalPauseRepositoryHarness) bumpGraphRevision() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authority.Graph.Revision++
	f.authority.Revisions.GraphRevision++
}

func normalPauseFixture(now time.Time) (gameusecase.NormalPauseAuthority, gameusecase.NormalPauseCommand) {
	tournamentID, rosterID, waveID := uuid.New(), uuid.New(), uuid.New()
	seriesID, slotID, gameID := uuid.New(), uuid.New(), uuid.New()
	firstID, secondID := uuid.New(), uuid.New()
	startedAt := now.Add(-5 * time.Minute)
	openedAt := startedAt.Add(-time.Minute)
	consumedAt := startedAt
	gameDeadline := now.Add(time.Minute)
	reconnectPauseID := uuid.New()
	reconnectOpenedAt := now.Add(-4 * time.Minute)
	reconnectDeadline := now.Add(-2 * time.Minute)
	reconnectClosedAt := now.Add(-3 * time.Minute)
	wave := domain.Wave{
		ID: waveID, TournamentID: tournamentID, RevisionID: domain.WaveRevisionID(uuid.New()),
		State:   domain.WaveStateActive,
		Members: []domain.WaveMember{{ParticipantID: firstID, Ready: true}, {ParticipantID: secondID, Ready: true}},
		ReadyWindow: &domain.ReadyWindow{
			ID: uuid.New(), WaveID: waveID, RevisionID: domain.ReadyWindowRevisionID(uuid.New()),
			State: domain.ReadyWindowStateConsumed, OpenedAt: openedAt, Deadline: startedAt, ConsumedAt: &consumedAt,
		},
		StartedAt: &startedAt,
	}
	game := domain.Game{ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStateActive}
	series := domain.Series{
		ID: seriesID, TournamentID: tournamentID, FirstParticipantID: firstID, SecondParticipantID: secondID,
		Format: domain.SeriesFormatBO1, State: domain.SeriesStateActive,
		Slots: []domain.GameSlot{{ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb, Attempts: []domain.Game{game}}},
	}
	scope := pausedomain.GraphScope{
		TournamentID: tournamentID, RosterID: rosterID, WaveID: waveID,
		Authority: authoritydomain.Identity{TournamentID: tournamentID, HolderID: uuid.New(), LeaseID: uuid.New(), Epoch: 3, ProcessKind: authoritydomain.ProcessAuthority},
	}
	graph := gameusecase.PauseGraph{
		Scope: scope, Revision: 11,
		Tournament: gameusecase.TournamentRecord{ID: tournamentID, RosterID: rosterID, Preset: domain.TournamentPresetV1, State: domain.TournamentStateSwiss, Revision: 7, RosterSize: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute), StartedAt: &startedAt},
		Wave:       gameusecase.PauseWave{Wave: wave, Revision: 4},
		Series:     []gameusecase.PauseSeries{{Execution: seriesdomain.Execution{Series: series}, Revision: 5, CurrentGameID: &gameID}},
		Games:      []gameusecase.PauseGame{{SeriesID: seriesID, Game: game, Revision: 6, Deadline: &gameDeadline}},
		Presence: []pausedomain.PausePresence{
			{ID: uuid.New(), TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID, ParticipantID: firstID, State: pausedomain.PresenceStateConnected, PresenceEpoch: 2, Revision: 4, ConnectedAt: startedAt, UpdatedAt: startedAt},
			{ID: uuid.New(), TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID, ParticipantID: secondID, State: pausedomain.PresenceStateConnected, PresenceEpoch: 3, Revision: 5, ConnectedAt: startedAt, UpdatedAt: startedAt},
		},
		Reconnect:              []pausedomain.PauseReconnectInterval{{ID: uuid.New(), PauseID: reconnectPauseID, RosterID: rosterID, SeriesID: seriesID, GameID: gameID, ParticipantID: firstID, PresenceEpoch: 2, Number: 1, State: pausedomain.ReconnectStateReconnected, OpenedAt: reconnectOpenedAt, Deadline: reconnectDeadline, ClosedAt: &reconnectClosedAt, Revision: 2, UpdatedAt: reconnectClosedAt}},
		Counters:               []pausedomain.PauseReconnectCounter{{PauseID: reconnectPauseID, RosterID: rosterID, ParticipantID: firstID, Limit: 3, Used: 1, Revision: 2}},
		TerminalActionRevision: 8,
	}
	revisions := gameusecase.PauseGraphRevisionsFrom(graph)
	command := gameusecase.NormalPauseCommand{Scope: scope, CommandID: uuid.New(), PauseID: uuid.New(), ActorID: uuid.New(), Reason: gameusecase.PauseReasonOperator, Expected: clonePauseGraphRevisions(revisions)}
	return gameusecase.NormalPauseAuthority{Scope: scope, Revisions: revisions, Graph: graph, Complete: true}, command
}

func makeReconnectOpenBeforePause(authority *gameusecase.NormalPauseAuthority, command *gameusecase.NormalPauseCommand, pausedAt time.Time, remaining time.Duration) {
	interval := &authority.Graph.Reconnect[0]
	presence := &authority.Graph.Presence[0]
	disconnectedAt := pausedAt.Add(-20 * time.Second)
	presence.State = pausedomain.PresenceStateDisconnected
	presence.DisconnectedAt = &disconnectedAt
	presence.UpdatedAt = disconnectedAt
	interval.State = pausedomain.ReconnectStateOpen
	interval.SuspendedByPauseID = nil
	interval.OpenedAt = disconnectedAt
	interval.Deadline = pausedAt.Add(remaining)
	interval.ClosedAt = nil
	interval.UpdatedAt = disconnectedAt
	interval.PresenceEpoch = presence.PresenceEpoch
	refreshNormalPauseRevisions(authority, command)
}

func makeReconnectContinuationBeforePause(authority *gameusecase.NormalPauseAuthority, command *gameusecase.NormalPauseCommand, pausedAt time.Time) {
	source := authority.Graph.Reconnect[0]
	oldPauseID := uuid.New()
	source.State = pausedomain.ReconnectStateCancelled
	source.ContinuationNumber = 0
	source.ContinuedFromID = nil
	source.SuspendedByPauseID = &oldPauseID
	source.OpenedAt = pausedAt.Add(-2 * time.Minute)
	sourceClosedAt := pausedAt.Add(-time.Minute)
	source.Deadline = sourceClosedAt.Add(30 * time.Second)
	source.ClosedAt = &sourceClosedAt
	source.UpdatedAt = sourceClosedAt
	currentOpenedAt := pausedAt.Add(-20 * time.Second)
	current := source
	current.ID = uuid.New()
	current.State = pausedomain.ReconnectStateOpen
	current.ContinuationNumber = 1
	current.ContinuedFromID = &source.ID
	current.SuspendedByPauseID = nil
	current.OpenedAt = currentOpenedAt
	current.Deadline = currentOpenedAt.Add(source.Deadline.Sub(sourceClosedAt))
	current.ClosedAt = nil
	current.Revision = 1
	current.UpdatedAt = currentOpenedAt
	presence := &authority.Graph.Presence[0]
	presence.State = pausedomain.PresenceStateDisconnected
	presence.DisconnectedAt = &currentOpenedAt
	presence.UpdatedAt = currentOpenedAt
	source.PresenceEpoch = presence.PresenceEpoch
	current.PresenceEpoch = presence.PresenceEpoch
	authority.Graph.Reconnect = []pausedomain.PauseReconnectInterval{source, current}
	authority.Graph.Counters[0].Used = 1
	refreshNormalPauseRevisions(authority, command)
}

func refreshNormalPauseRevisions(authority *gameusecase.NormalPauseAuthority, command *gameusecase.NormalPauseCommand) {
	authority.Revisions = gameusecase.PauseGraphRevisionsFrom(authority.Graph)
	command.Expected = clonePauseGraphRevisions(authority.Revisions)
}

func cloneNormalPauseAuthority(value gameusecase.NormalPauseAuthority) gameusecase.NormalPauseAuthority {
	value.Revisions = clonePauseGraphRevisions(value.Revisions)
	value.Graph = clonePauseGraph(value.Graph)
	return value
}

func cloneNormalPauseRecord(value gameusecase.NormalPauseRecord) gameusecase.NormalPauseRecord {
	value.Expected = clonePauseGraphRevisions(value.Expected)
	value.Graph = clonePauseGraph(value.Graph)
	value.SuspendedReconnect = cloneTestSlice(value.SuspendedReconnect)
	return value
}

func clonePauseGraphRevisions(value gameusecase.PauseGraphRevisions) gameusecase.PauseGraphRevisions {
	value.Series = cloneTestSlice(value.Series)
	value.Games = cloneTestSlice(value.Games)
	value.Presence = cloneTestSlice(value.Presence)
	value.Reconnect = cloneTestSlice(value.Reconnect)
	value.Counters = cloneTestSlice(value.Counters)
	value.FrozenDeadlines = cloneTestSlice(value.FrozenDeadlines)
	if value.Draft != nil {
		draft := *value.Draft
		value.Draft = &draft
	}
	return value
}

func cloneTestSlice[T any](value []T) []T {
	if value == nil {
		return nil
	}
	return append(make([]T, 0, len(value)), value...)
}

func clonePauseGraph(value gameusecase.PauseGraph) gameusecase.PauseGraph {
	value.Tournament = *cloneTournamentRecord(value.Tournament)
	value.Wave.Wave.Members = append([]domain.WaveMember(nil), value.Wave.Wave.Members...)
	if value.Wave.Wave.ReadyWindow != nil {
		window := *value.Wave.Wave.ReadyWindow
		if window.ConsumedAt != nil {
			consumedAt := *window.ConsumedAt
			window.ConsumedAt = &consumedAt
		}
		value.Wave.Wave.ReadyWindow = &window
	}
	value.Series = append([]gameusecase.PauseSeries(nil), value.Series...)
	for i := range value.Series {
		value.Series[i].Execution = cloneTestSeriesExecution(value.Series[i].Execution)
		if value.Series[i].CurrentGameID != nil {
			id := *value.Series[i].CurrentGameID
			value.Series[i].CurrentGameID = &id
		}
	}
	value.Games = append([]gameusecase.PauseGame(nil), value.Games...)
	for i := range value.Games {
		if value.Games[i].Deadline != nil {
			deadline := *value.Games[i].Deadline
			value.Games[i].Deadline = &deadline
		}
		if value.Games[i].ResumeState != nil {
			state := *value.Games[i].ResumeState
			value.Games[i].ResumeState = &state
		}
	}
	if value.Draft != nil {
		draft := *value.Draft
		value.Draft = &draft
	}
	value.Presence = append([]pausedomain.PausePresence(nil), value.Presence...)
	value.Reconnect = append([]pausedomain.PauseReconnectInterval(nil), value.Reconnect...)
	for i := range value.Reconnect {
		if value.Reconnect[i].ClosedAt != nil {
			closedAt := *value.Reconnect[i].ClosedAt
			value.Reconnect[i].ClosedAt = &closedAt
		}
		if value.Reconnect[i].ContinuedFromID != nil {
			id := *value.Reconnect[i].ContinuedFromID
			value.Reconnect[i].ContinuedFromID = &id
		}
		if value.Reconnect[i].SuspendedByPauseID != nil {
			id := *value.Reconnect[i].SuspendedByPauseID
			value.Reconnect[i].SuspendedByPauseID = &id
		}
	}
	value.Counters = append([]pausedomain.PauseReconnectCounter(nil), value.Counters...)
	value.FrozenDeadlines = append([]gameusecase.PauseFrozenDeadline(nil), value.FrozenDeadlines...)
	for i := range value.FrozenDeadlines {
		if value.FrozenDeadlines[i].ResumedAt != nil {
			resumedAt := *value.FrozenDeadlines[i].ResumedAt
			value.FrozenDeadlines[i].ResumedAt = &resumedAt
		}
		if value.FrozenDeadlines[i].ResumedDeadline != nil {
			deadline := *value.FrozenDeadlines[i].ResumedDeadline
			value.FrozenDeadlines[i].ResumedDeadline = &deadline
		}
	}
	return value
}

func cloneTestSeriesExecution(value seriesdomain.Execution) seriesdomain.Execution {
	if value.ResumeState != nil {
		state := *value.ResumeState
		value.ResumeState = &state
	}
	value.Series.Slots = append([]domain.GameSlot(nil), value.Series.Slots...)
	for i := range value.Series.Slots {
		value.Series.Slots[i].Attempts = append([]domain.Game(nil), value.Series.Slots[i].Attempts...)
	}
	return value
}
