package pause_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
)

func pauseResumePresenceFixture(t *testing.T, decidedAt time.Time, preexisting [2]bool) (gameusecase.PauseResumePresenceAuthority, gameusecase.PauseResumePresenceCommand) {
	t.Helper()
	pausedAt := decidedAt.Add(-2 * time.Minute)
	authority, pauseCommand := normalPauseFixture(pausedAt)
	series := authority.Graph.Series[0].Execution.Series
	participants := [2]uuid.UUID{series.FirstParticipantID, series.SecondParticipantID}
	childPauseID := authority.Graph.Counters[0].PauseID
	authority.Graph.Counters = []pausedomain.PauseReconnectCounter{
		{PauseID: childPauseID, RosterID: authority.Scope.RosterID, ParticipantID: participants[0], Limit: 3, Revision: 1},
		{PauseID: childPauseID, RosterID: authority.Scope.RosterID, ParticipantID: participants[1], Limit: 3, Revision: 1},
	}
	authority.Graph.Reconnect = nil
	for index, absent := range preexisting {
		if !absent {
			continue
		}
		disconnectedAt := pausedAt.Add(time.Duration(-30-index*10) * time.Second)
		presence := presenceForParticipant(t, authority.Graph.Presence, participants[index])
		presence.State, presence.DisconnectedAt, presence.UpdatedAt = pausedomain.PresenceStateDisconnected, &disconnectedAt, disconnectedAt
		authority.Graph.Counters[index].Used, authority.Graph.Counters[index].Revision = 1, 2
		authority.Graph.Reconnect = append(authority.Graph.Reconnect, pausedomain.PauseReconnectInterval{
			ID: uuid.New(), PauseID: childPauseID, RosterID: authority.Scope.RosterID, SeriesID: series.ID,
			GameID: authority.Graph.Games[0].Game.ID, ParticipantID: participants[index], PresenceEpoch: presence.PresenceEpoch,
			Number: 1, ContinuationNumber: 0, State: pausedomain.ReconnectStateOpen, OpenedAt: disconnectedAt,
			Deadline: pausedAt.Add(time.Duration(40+index*25) * time.Second), Revision: 1, UpdatedAt: disconnectedAt,
		})
	}
	refreshNormalPauseRevisions(&authority, &pauseCommand)
	repository := newNormalPauseRepositoryHarness(t, authority)
	pauseRecord, changed, err := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt)).Enter(t.Context(), pauseCommand)
	if err != nil || !changed {
		t.Fatalf("enter fixture pause: error = %v, changed = %v", err, changed)
	}
	resume := pauseResumeAuthorityFromRecord(*pauseRecord)
	seriesPauseID := uuid.New()
	seriesStartedAt := pausedAt.Add(-2 * time.Minute)
	seriesDecision := gameusecase.PauseResumeDecisionAuthority{
		PauseID: seriesPauseID, ScopeKind: gameusecase.PauseResumeDecisionScopeSeries,
		CurrentRevisionID: uuid.New(), State: gameusecase.PauseStateActive, Revision: 1,
		SeriesID: series.ID, Depth: 0, DecisionNumber: 0, StartedAt: seriesStartedAt,
	}
	gameStartedAt := pausedAt.Add(-time.Minute)
	originalDeadline := pausedAt.Add(time.Minute)
	gameClock := pausedomain.PauseResumeGameClock{
		PauseID: childPauseID, GameID: authority.Graph.Games[0].Game.ID,
		OriginalDeadline: originalDeadline, FrozenAt: gameStartedAt, Remaining: originalDeadline.Sub(gameStartedAt), Revision: 1,
	}
	gameDecision := gameusecase.PauseResumeDecisionAuthority{
		PauseID: childPauseID, ScopeKind: gameusecase.PauseResumeDecisionScopeGameAttempt,
		CurrentRevisionID: uuid.New(), State: gameusecase.PauseStateActive, Revision: 1,
		SeriesID: series.ID, GameID: authority.Graph.Games[0].Game.ID, ParentPauseID: uuidPointer(seriesPauseID), Depth: 1,
		DecisionNumber: 0, StartedAt: gameStartedAt, GameClock: &gameClock,
	}
	result := gameusecase.PauseResumePresenceAuthority{Resume: resume, SeriesDecision: seriesDecision, GameDecision: gameDecision}
	presenceExpected := gameusecase.PauseResumePresenceExpectationFrom(result)
	command := gameusecase.PauseResumePresenceCommand{
		Resume:           gameusecase.PauseResumeCommand{Scope: pauseRecord.Scope, PauseID: pauseRecord.PauseID, CommandID: uuid.New(), ActorID: uuid.New(), Expected: gameusecase.PauseResumeExpectationFrom(resume)},
		SeriesDecisionID: uuid.New(), GameDecisionID: uuid.New(),
		SeriesExpected:  gameusecase.PauseResumeDecisionExpectationFrom(seriesDecision),
		GameExpected:    gameusecase.PauseResumeDecisionExpectationFrom(gameDecision),
		Presence:        presenceExpected.Presence,
		Reconnect:       presenceExpected.Reconnect,
		Counters:        presenceExpected.Counters,
		FrozenDeadlines: presenceExpected.FrozenDeadlines,
	}
	return result, command
}

func addCompletedPauseResumeDraft(
	t *testing.T,
	authority *gameusecase.PauseResumePresenceAuthority,
	command *gameusecase.PauseResumePresenceCommand,
	startedAt time.Time,
) {
	t.Helper()
	series := &authority.Resume.Pause.Graph.Series[0].Execution.Series
	revision := task029CategoryRevision(t, domain.TournamentStageSwiss, true, uuid.New(), startedAt.Add(-time.Minute))
	revision.SeriesID = series.ID
	initial, err := draftusecase.StartExecution(draftusecase.ExecutionStartCommand{
		CategoryRevision: revision, DraftID: uuid.New(), InitialRevisionID: uuid.New(), DecisionEvidenceID: uuid.New(),
		ParticipantIDs: [2]uuid.UUID{series.FirstParticipantID, series.SecondParticipantID}, ServiceEpoch: uuid.New(),
		CommandID: uuid.New(), StartedAt: startedAt,
	})
	if err != nil {
		t.Fatalf("start completed Draft fixture: %v", err)
	}
	draft := completeTask030Draft(t, initial, startedAt)
	series.FirstParticipantID = draft.FirstParticipantID
	series.SecondParticipantID = draft.SecondParticipantID
	authority.Resume.Pause.Graph.Draft = &draft
	authority.Resume.Pause.Expected.Draft = &draftusecase.RevisionExpectation{
		RevisionID: draft.RevisionID, Revision: draft.Revision, ServiceEpoch: draft.ServiceEpoch,
	}
	authority.Resume.Pause.Expected.DraftPreviousRevisionID = draft.PreviousRevisionID
	authority.Resume.Pause.DraftResultRevisionID = uuid.New()
	command.Resume.DraftResultRevisionID = uuid.New()
	refreshPauseResumePresenceExpectation(authority, command)
}

func refreshPauseResumePresenceExpectation(a *gameusecase.PauseResumePresenceAuthority, c *gameusecase.PauseResumePresenceCommand) {
	c.Resume.Expected = gameusecase.PauseResumeExpectationFrom(a.Resume)
	c.SeriesExpected = gameusecase.PauseResumeDecisionExpectationFrom(a.SeriesDecision)
	c.GameExpected = gameusecase.PauseResumeDecisionExpectationFrom(a.GameDecision)
	expected := gameusecase.PauseResumePresenceExpectationFrom(*a)
	c.Presence = expected.Presence
	c.Reconnect = expected.Reconnect
	c.Counters = expected.Counters
	c.FrozenDeadlines = expected.FrozenDeadlines
}

func disconnectPausePresence(p *pausedomain.PausePresence, at time.Time, delta int64) {
	p.State = pausedomain.PresenceStateDisconnected
	p.PresenceEpoch += delta
	p.Revision += delta
	if delta > 1 {
		p.ConnectedAt = at.Add(-time.Second)
	}
	p.DisconnectedAt, p.UpdatedAt = &at, at
}

func presenceForParticipant(tb testing.TB, values []pausedomain.PausePresence, id uuid.UUID) *pausedomain.PausePresence {
	tb.Helper()
	for i := range values {
		if values[i].ParticipantID == id {
			return &values[i]
		}
	}
	tb.Fatalf("Presence %s not found", id)
	return nil
}
func reconnectCounterForParticipant(tb testing.TB, values []pausedomain.PauseReconnectCounter, id uuid.UUID) *pausedomain.PauseReconnectCounter {
	tb.Helper()
	for i := range values {
		if values[i].ParticipantID == id {
			return &values[i]
		}
	}
	tb.Fatalf("counter %s not found", id)
	return nil
}
func reconnectByID(tb testing.TB, values []pausedomain.PauseReconnectInterval, id uuid.UUID) pausedomain.PauseReconnectInterval {
	tb.Helper()
	for _, value := range values {
		if value.ID == id {
			return value
		}
	}
	tb.Fatalf("Reconnect %s not found", id)
	return pausedomain.PauseReconnectInterval{}
}
func suspendedReconnectForParticipant(tb testing.TB, record gameusecase.NormalPauseRecord, id uuid.UUID) pausedomain.PauseReconnectInterval {
	tb.Helper()
	for _, item := range record.SuspendedReconnect {
		value := reconnectByID(tb, record.Graph.Reconnect, item.ID)
		if value.ParticipantID == id {
			return value
		}
	}
	tb.Fatalf("suspended Reconnect %s not found", id)
	return pausedomain.PauseReconnectInterval{}
}

func assertCurrentReconnectIdentity(tb testing.TB, current *pausedomain.PauseReconnectInterval, authority gameusecase.PauseResumePresenceAuthority, participantID uuid.UUID, decidedAt time.Time) {
	tb.Helper()
	series := authority.Resume.Pause.Graph.Series[0].Execution.Series
	if current == nil || current.RosterID != authority.Resume.Pause.Scope.RosterID || current.SeriesID != series.ID ||
		current.GameID != authority.GameDecision.GameID || current.ParticipantID != participantID || current.Revision != 1 ||
		!current.UpdatedAt.Equal(decidedAt) {
		tb.Fatalf("current Reconnect identity = %+v", current)
	}
}

func assertContinuationReconnectIdentity(tb testing.TB, source, current pausedomain.PauseReconnectInterval) {
	tb.Helper()
	if current.PauseID != source.PauseID || current.RosterID != source.RosterID || current.SeriesID != source.SeriesID ||
		current.GameID != source.GameID || current.ParticipantID != source.ParticipantID ||
		current.PresenceEpoch != source.PresenceEpoch || current.Number != source.Number {
		tb.Fatalf("continuation identity = %+v, source = %+v", current, source)
	}
}

func assertNormalSuspendedSource(tb testing.TB, source pausedomain.PauseReconnectInterval, normalPauseID uuid.UUID, pausedAt time.Time) {
	tb.Helper()
	if source.State != pausedomain.ReconnectStateCancelled || source.ClosedAt == nil || !source.ClosedAt.Equal(pausedAt) ||
		source.SuspendedByPauseID == nil || *source.SuspendedByPauseID != normalPauseID || !source.OpenedAt.Before(pausedAt) {
		tb.Fatalf("normal-pause source suspension = %+v", source)
	}
}

func assertDecisionReconnectIntervals(tb testing.TB, decision gameusecase.PauseResumeDecisionRecord, first, second *pausedomain.PauseReconnectInterval) {
	tb.Helper()
	var firstID, secondID *uuid.UUID
	if first != nil {
		firstID = uuidPointer(first.ID)
	}
	if second != nil {
		secondID = uuidPointer(second.ID)
	}
	if !reflect.DeepEqual(decision.FirstReconnectIntervalID, firstID) || !reflect.DeepEqual(decision.SecondReconnectIntervalID, secondID) {
		tb.Fatalf("decision Reconnect refs = %v, %v, want %v, %v", decision.FirstReconnectIntervalID, decision.SecondReconnectIntervalID, firstID, secondID)
	}
}

func uuidPointer(value uuid.UUID) *uuid.UUID {
	return &value
}

func assertReconnectWaitRecord(tb testing.TB, paused gameusecase.PauseGraph, record gameusecase.PauseResumePresenceRecord) {
	tb.Helper()
	got := record.Graph
	if got.Tournament.State != domain.TournamentStateTechnicalPause || got.Wave.Wave.State != domain.WaveStatePaused ||
		got.Series[0].Execution.Series.State != domain.SeriesStateTechnicalPause || got.Games[0].Game.State != domain.GameStatePaused ||
		!got.DeadlinesSuppressed || got.ActivePauseID != paused.ActivePauseID || got.FrozenDeadlines[0].ResumedAt != nil || got.FrozenDeadlines[0].ResumedDeadline != nil {
		tb.Fatalf("wait resumed parent clocks: %+v", got)
	}
	if record.GameDecision.ID != record.Command.GameDecisionID || record.GameDecision.PauseID != record.Command.GameExpected.PauseID ||
		record.GameDecision.DecisionNumber != record.Command.GameExpected.DecisionNumber+1 ||
		!record.GameDecision.DecidedAt.Equal(record.DecidedAt) ||
		record.Command.GameExpected.GameClock == nil || !reflect.DeepEqual(record.GameClock, *record.Command.GameExpected.GameClock) ||
		record.SeriesDecision != nil || record.GamePauseState != gameusecase.PauseStateActive ||
		record.SeriesPauseState != gameusecase.PauseStateActive || record.NormalPauseState != gameusecase.PauseStateActive ||
		record.NormalPauseResolvedAt != nil {
		tb.Fatalf("wait decision or pause chain = %+v", record)
	}
}

func assertOnlyGameDecisionWrite(tb testing.TB, repository *pauseResumePresenceRepositoryHarness) {
	tb.Helper()
	if repository.gameDecisionWriteCount() != 1 || repository.seriesDecisionWriteCount() != 0 {
		tb.Fatalf("decision writes: Game = %d, Series = %d", repository.gameDecisionWriteCount(), repository.seriesDecisionWriteCount())
	}
}

func clonePauseResumePresenceAuthority(value gameusecase.PauseResumePresenceAuthority) gameusecase.PauseResumePresenceAuthority {
	value.Resume = clonePauseResumeAuthority(value.Resume)
	value.SeriesDecision = clonePauseResumeDecisionAuthority(value.SeriesDecision)
	value.GameDecision = clonePauseResumeDecisionAuthority(value.GameDecision)
	return value
}

func clonePauseResumeDecisionAuthority(value gameusecase.PauseResumeDecisionAuthority) gameusecase.PauseResumeDecisionAuthority {
	if value.ParentPauseID != nil {
		id := *value.ParentPauseID
		value.ParentPauseID = &id
	}
	if value.GameClock != nil {
		clock := clonePauseResumeGameClock(*value.GameClock)
		value.GameClock = &clock
	}
	return value
}

func clonePauseResumeGameClock(value pausedomain.PauseResumeGameClock) pausedomain.PauseResumeGameClock {
	if value.ResumedAt != nil {
		at := *value.ResumedAt
		value.ResumedAt = &at
	}
	if value.ResumedDeadline != nil {
		deadline := *value.ResumedDeadline
		value.ResumedDeadline = &deadline
	}
	return value
}

func clonePauseResumePresenceRecord(value gameusecase.PauseResumePresenceRecord) gameusecase.PauseResumePresenceRecord {
	value.Command = clonePauseResumePresenceCommand(value.Command)
	value.Graph = clonePauseGraph(value.Graph)
	value.GameClock = clonePauseResumeGameClock(value.GameClock)
	value.First = clonePauseResumeParticipantResolution(value.First)
	value.Second = clonePauseResumeParticipantResolution(value.Second)
	value.GameDecision = clonePauseResumeDecisionRecord(value.GameDecision)
	if value.SeriesDecision != nil {
		decision := clonePauseResumeDecisionRecord(*value.SeriesDecision)
		value.SeriesDecision = &decision
	}
	return value
}

func clonePauseResumePresenceCommand(value gameusecase.PauseResumePresenceCommand) gameusecase.PauseResumePresenceCommand {
	value.Resume.Expected = clonePauseResumeExpectationExact(value.Resume.Expected)
	value.SeriesExpected = clonePauseResumeDecisionExpectation(value.SeriesExpected)
	value.GameExpected = clonePauseResumeDecisionExpectation(value.GameExpected)
	value.Presence = clonePauseGraph(gameusecase.PauseGraph{Presence: value.Presence}).Presence
	value.Reconnect = clonePauseGraph(gameusecase.PauseGraph{Reconnect: value.Reconnect}).Reconnect
	value.Counters = cloneTestSlice(value.Counters)
	value.FrozenDeadlines = clonePauseGraph(gameusecase.PauseGraph{FrozenDeadlines: value.FrozenDeadlines}).FrozenDeadlines
	value.FirstInterval = clonePauseResumeIntervalInput(value.FirstInterval)
	value.SecondInterval = clonePauseResumeIntervalInput(value.SecondInterval)
	return value
}

func clonePauseResumeExpectationExact(value gameusecase.PauseResumeExpectation) gameusecase.PauseResumeExpectation {
	value.Games = cloneTestSlice(value.Games)
	value.Series = cloneTestSlice(value.Series)
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

func clonePauseResumeDecisionExpectation(value gameusecase.PauseResumeDecisionExpectation) gameusecase.PauseResumeDecisionExpectation {
	if value.ParentPauseID != nil {
		id := *value.ParentPauseID
		value.ParentPauseID = &id
	}
	if value.GameClock != nil {
		clock := clonePauseResumeGameClock(*value.GameClock)
		value.GameClock = &clock
	}
	return value
}

func clonePauseResumeDecisionRecord(value gameusecase.PauseResumeDecisionRecord) gameusecase.PauseResumeDecisionRecord {
	if value.FirstReconnectIntervalID != nil {
		id := *value.FirstReconnectIntervalID
		value.FirstReconnectIntervalID = &id
	}
	if value.SecondReconnectIntervalID != nil {
		id := *value.SecondReconnectIntervalID
		value.SecondReconnectIntervalID = &id
	}
	return value
}

func clonePauseResumeIntervalInput(value *gameusecase.PauseResumeIntervalInput) *gameusecase.PauseResumeIntervalInput {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func clonePauseResumeParticipantResolution(value gameusecase.PauseResumeParticipantResolution) gameusecase.PauseResumeParticipantResolution {
	if value.SourceInterval != nil {
		item := clonePauseGraph(gameusecase.PauseGraph{Reconnect: []pausedomain.PauseReconnectInterval{*value.SourceInterval}}).Reconnect[0]
		value.SourceInterval = &item
	}
	if value.CurrentInterval != nil {
		item := clonePauseGraph(gameusecase.PauseGraph{Reconnect: []pausedomain.PauseReconnectInterval{*value.CurrentInterval}}).Reconnect[0]
		value.CurrentInterval = &item
	}
	return value
}
