package pause

import (
	"errors"
	"math"
	"time"

	"github.com/google/uuid"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

type pauseResumePresenceBuild struct {
	action PauseResumePresenceAction
	graph  PauseGraph
	first  PauseResumeParticipantResolution
	second PauseResumeParticipantResolution
}

func buildPauseResumePresenceRecord(authority PauseResumePresenceAuthority, command PauseResumePresenceCommand, decidedAt time.Time) (PauseResumePresenceRecord, error) {
	if decidedAt.Before(authority.Resume.Pause.PausedAt) || !resumeTimeCoversAuthorityHistory(authority.Resume, decidedAt) {
		return PauseResumePresenceRecord{}, pauseResumePresenceError("decision time precedes authority")
	}
	if authority.GameDecision.DecisionNumber == math.MaxInt64 {
		return PauseResumePresenceRecord{}, ErrPauseResumePresenceOverflow
	}
	built, err := classifyPauseResumePresence(authority, command, decidedAt)
	if err != nil {
		return PauseResumePresenceRecord{}, err
	}
	if built.action == PauseResumeActionResume && authority.SeriesDecision.DecisionNumber == math.MaxInt64 {
		return PauseResumePresenceRecord{}, ErrPauseResumePresenceOverflow
	}
	gameDecision := newPauseResumeDecisionRecord(command.GameDecisionID, authority.GameDecision, built.action, built.first.CurrentInterval, built.second.CurrentInterval, decidedAt)
	record := PauseResumePresenceRecord{
		Command: clonePauseResumePresenceCommand(command), GameDecision: gameDecision,
		GamePauseState: PauseStateActive, SeriesPauseState: PauseStateActive, NormalPauseState: PauseStateActive,
		GameClock: clonePauseResumeGameClock(*authority.GameDecision.GameClock), Graph: built.graph,
		First: clonePauseResumeParticipantResolution(built.first), Second: clonePauseResumeParticipantResolution(built.second),
		DecidedAt: decidedAt,
	}
	if built.action == PauseResumeActionResume {
		resolved, err := buildPauseResumeRecord(authority.Resume, command.Resume, decidedAt)
		if err != nil {
			if errors.Is(err, ErrPauseResumeOverflow) {
				return PauseResumePresenceRecord{}, ErrPauseResumePresenceOverflow
			}
			return PauseResumePresenceRecord{}, err
		}
		clock, err := resumePauseGameClock(*authority.GameDecision.GameClock, decidedAt)
		if err != nil {
			return PauseResumePresenceRecord{}, err
		}
		seriesDecision := newPauseResumeDecisionRecord(command.SeriesDecisionID, authority.SeriesDecision, PauseResumeActionResume, nil, nil, decidedAt)
		record.SeriesDecision = &seriesDecision
		record.GameClock = clock
		record.GamePauseState = PauseStateResumed
		record.SeriesPauseState = PauseStateResumed
		record.NormalPauseState = PauseStateResumed
		record.NormalPauseResolvedAt = pauseCloneTimePointer(&decidedAt)
		record.Graph = clonePauseGraph(resolved.Graph)
	}
	if err := validatePauseResumePresenceRecord(record); err != nil {
		return PauseResumePresenceRecord{}, err
	}
	return clonePauseResumePresenceRecord(record), nil
}

func classifyPauseResumePresence(authority PauseResumePresenceAuthority, command PauseResumePresenceCommand, decidedAt time.Time) (pauseResumePresenceBuild, error) {
	series := pauseSeriesByID(authority.Resume.Pause.Graph.Series, authority.SeriesDecision.SeriesID)
	if series == nil {
		return pauseResumePresenceBuild{}, ErrPauseResumePresenceIncomplete
	}
	firstID := series.Execution.Series.FirstParticipantID
	secondID := series.Execution.Series.SecondParticipantID
	first, err := resolvePauseResumeParticipant(authority, firstID, command.FirstInterval, decidedAt)
	if err != nil {
		return pauseResumePresenceBuild{}, err
	}
	second, err := resolvePauseResumeParticipant(authority, secondID, command.SecondInterval, decidedAt)
	if err != nil {
		return pauseResumePresenceBuild{}, err
	}
	firstWaiting := first.CurrentInterval != nil
	secondWaiting := second.CurrentInterval != nil
	action := pauseResumeAction(firstWaiting, secondWaiting)
	graph := clonePauseGraph(authority.Resume.Pause.Graph)
	if action != PauseResumeActionResume {
		if graph.Revision == math.MaxInt64 {
			return pauseResumePresenceBuild{}, ErrPauseResumePresenceOverflow
		}
		graph.Revision++
		graph.Presence = clonePausePresenceSlice(authority.Resume.Presence)
		graph.Reconnect = clonePauseReconnectSlice(authority.Resume.Reconnect)
		graph.Counters = append([]pausedomain.PauseReconnectCounter(nil), authority.Resume.Counters...)
		appendPauseResumeParticipant(&graph, first)
		appendPauseResumeParticipant(&graph, second)
		if err := validatePauseGraph(graph, true); err != nil {
			return pauseResumePresenceBuild{}, pauseResumePresenceError("invalid wait graph: %v", err)
		}
	}
	return pauseResumePresenceBuild{action: action, graph: graph, first: first, second: second}, nil
}

func pauseResumeAction(firstWaiting, secondWaiting bool) PauseResumePresenceAction {
	switch {
	case firstWaiting && secondWaiting:
		return PauseResumeActionWaitBoth
	case firstWaiting:
		return PauseResumeActionWaitFirst
	case secondWaiting:
		return PauseResumeActionWaitSecond
	default:
		return PauseResumeActionResume
	}
}

func resolvePauseResumeParticipant(authority PauseResumePresenceAuthority, participantID uuid.UUID, input *PauseResumeIntervalInput, decidedAt time.Time) (PauseResumeParticipantResolution, error) {
	evidence, ok := pauseResumeParticipantEvidenceFor(authority, participantID)
	if !ok {
		return PauseResumeParticipantResolution{}, ErrPauseResumePresenceIncomplete
	}
	resolution := PauseResumeParticipantResolution{
		ParticipantID: participantID, PresenceEpoch: evidence.live.PresenceEpoch,
		Disposition: PauseResumeParticipantConnected, Counter: evidence.counter,
	}
	if evidence.live.State == pausedomain.PresenceStateConnected {
		if input != nil {
			return PauseResumeParticipantResolution{}, pauseResumePresenceError("connected participant has interval input")
		}
		return resolution, nil
	}
	if input == nil || input.ParticipantID != participantID {
		return PauseResumeParticipantResolution{}, pauseResumePresenceError("missing participant interval input")
	}
	if reconnectIntervalByID(authority.Resume.Reconnect, input.IntervalID) != nil {
		return PauseResumeParticipantResolution{}, pauseResumePresenceError("interval identity already exists")
	}
	source := suspendedPauseResumeSource(authority, participantID, evidence.live.PresenceEpoch)
	if evidence.isContinuation(source) {
		return resolvePauseResumeContinuation(resolution, source, *input, decidedAt)
	}
	return resolvePauseResumeFresh(authority, resolution, evidence, *input, decidedAt)
}

type pauseResumeParticipantEvidence struct {
	live     pausedomain.PausePresence
	snapshot pausedomain.PausePresence
	counter  pausedomain.PauseReconnectCounter
}

func pauseResumeParticipantEvidenceFor(authority PauseResumePresenceAuthority, participantID uuid.UUID) (pauseResumeParticipantEvidence, bool) {
	live := pausePresenceByParticipant(authority.Resume.Presence, participantID)
	snapshot := pausePresenceByParticipant(authority.Resume.Pause.Graph.Presence, participantID)
	counter := pauseCounterByParticipant(authority.Resume.Counters, authority.GameDecision.PauseID, participantID)
	if live == nil || snapshot == nil || counter == nil {
		return pauseResumeParticipantEvidence{}, false
	}
	return pauseResumeParticipantEvidence{live: *live, snapshot: *snapshot, counter: *counter}, true
}

func (value pauseResumeParticipantEvidence) isContinuation(source *pausedomain.PauseReconnectInterval) bool {
	return value.snapshot.State == pausedomain.PresenceStateDisconnected && value.live.PresenceEpoch == value.snapshot.PresenceEpoch && source != nil
}

func resolvePauseResumeContinuation(
	resolution PauseResumeParticipantResolution,
	source *pausedomain.PauseReconnectInterval,
	input PauseResumeIntervalInput,
	decidedAt time.Time,
) (PauseResumeParticipantResolution, error) {
	if input.Window != 0 {
		return PauseResumeParticipantResolution{}, pauseResumePresenceError("continuation has fresh window")
	}
	current, err := newPauseResumeContinuation(*source, input.IntervalID, decidedAt)
	if err != nil {
		return PauseResumeParticipantResolution{}, err
	}
	resolution.Disposition = PauseResumeParticipantContinuation
	resolution.SourceInterval = clonePauseReconnectPointer(source)
	resolution.CurrentInterval = &current
	return resolution, nil
}

func resolvePauseResumeFresh(
	authority PauseResumePresenceAuthority,
	resolution PauseResumeParticipantResolution,
	evidence pauseResumeParticipantEvidence,
	input PauseResumeIntervalInput,
	decidedAt time.Time,
) (PauseResumeParticipantResolution, error) {
	if input.Window <= 0 || evidence.live.PresenceEpoch <= evidence.snapshot.PresenceEpoch {
		return PauseResumeParticipantResolution{}, pauseResumePresenceError("absence lacks fresh evidence")
	}
	if evidence.counter.Used >= evidence.counter.Limit {
		return PauseResumeParticipantResolution{}, ErrPauseResumePresenceIneligible
	}
	if evidence.counter.Revision == math.MaxInt64 || evidence.counter.Used == math.MaxInt {
		return PauseResumeParticipantResolution{}, ErrPauseResumePresenceOverflow
	}
	current, err := newPauseResumeFresh(authority, evidence.live, evidence.counter, input.IntervalID, input.Window, decidedAt)
	if err != nil {
		return PauseResumeParticipantResolution{}, err
	}
	resolution.Disposition = PauseResumeParticipantFresh
	resolution.Counter.Used++
	resolution.Counter.Revision++
	resolution.CurrentInterval = &current
	return resolution, nil
}

func newPauseResumeContinuation(source pausedomain.PauseReconnectInterval, id uuid.UUID, decidedAt time.Time) (pausedomain.PauseReconnectInterval, error) {
	if source.State != pausedomain.ReconnectStateCancelled || source.ClosedAt == nil || source.SuspendedByPauseID == nil ||
		source.ContinuationNumber == math.MaxInt {
		return pausedomain.PauseReconnectInterval{}, pauseResumePresenceError("invalid continuation source")
	}
	remaining := source.Deadline.Sub(*source.ClosedAt)
	deadline, err := addPauseResumeTime(decidedAt, remaining)
	if err != nil {
		return pausedomain.PauseReconnectInterval{}, err
	}
	return pausedomain.PauseReconnectInterval{
		ID: id, PauseID: source.PauseID, RosterID: source.RosterID, SeriesID: source.SeriesID, GameID: source.GameID,
		ParticipantID: source.ParticipantID, PresenceEpoch: source.PresenceEpoch, Number: source.Number,
		ContinuationNumber: source.ContinuationNumber + 1, ContinuedFromID: pauseCloneUUIDPointer(&source.ID),
		State: pausedomain.ReconnectStateOpen, OpenedAt: decidedAt, Deadline: deadline, Revision: 1, UpdatedAt: decidedAt,
	}, nil
}

func newPauseResumeFresh(authority PauseResumePresenceAuthority, presence pausedomain.PausePresence, counter pausedomain.PauseReconnectCounter, id uuid.UUID, window time.Duration, decidedAt time.Time) (pausedomain.PauseReconnectInterval, error) {
	deadline, err := addPauseResumeTime(decidedAt, window)
	if err != nil {
		return pausedomain.PauseReconnectInterval{}, err
	}
	return pausedomain.PauseReconnectInterval{
		ID: id, PauseID: authority.GameDecision.PauseID, RosterID: authority.Resume.Pause.Scope.RosterID,
		SeriesID: authority.GameDecision.SeriesID, GameID: authority.GameDecision.GameID,
		ParticipantID: presence.ParticipantID, PresenceEpoch: presence.PresenceEpoch,
		Number: counter.Used + 1, ContinuationNumber: 0, State: pausedomain.ReconnectStateOpen,
		OpenedAt: decidedAt, Deadline: deadline, Revision: 1, UpdatedAt: decidedAt,
	}, nil
}

func appendPauseResumeParticipant(graph *PauseGraph, resolution PauseResumeParticipantResolution) {
	if resolution.CurrentInterval == nil {
		return
	}
	graph.Reconnect = append(graph.Reconnect, *clonePauseReconnectPointer(resolution.CurrentInterval))
	for index := range graph.Counters {
		if graph.Counters[index].PauseID == resolution.Counter.PauseID && graph.Counters[index].ParticipantID == resolution.ParticipantID {
			graph.Counters[index] = resolution.Counter
			return
		}
	}
}

func newPauseResumeDecisionRecord(id uuid.UUID, authority PauseResumeDecisionAuthority, action PauseResumePresenceAction, first, second *pausedomain.PauseReconnectInterval, decidedAt time.Time) PauseResumeDecisionRecord {
	record := PauseResumeDecisionRecord{ID: id, PauseID: authority.PauseID, DecisionNumber: authority.DecisionNumber + 1, Action: action, DecidedAt: decidedAt}
	if first != nil {
		record.FirstReconnectIntervalID = pauseCloneUUIDPointer(&first.ID)
	}
	if second != nil {
		record.SecondReconnectIntervalID = pauseCloneUUIDPointer(&second.ID)
	}
	return record
}

func resumePauseGameClock(value pausedomain.PauseResumeGameClock, decidedAt time.Time) (pausedomain.PauseResumeGameClock, error) {
	if validatePauseResumeGameClock(value, true) != nil || value.Revision == math.MaxInt64 {
		return pausedomain.PauseResumeGameClock{}, ErrPauseResumePresenceOverflow
	}
	deadline, err := addPauseResumeTime(decidedAt, value.Remaining)
	if err != nil {
		return pausedomain.PauseResumeGameClock{}, err
	}
	value.ResumedAt = pauseCloneTimePointer(&decidedAt)
	value.ResumedDeadline = pauseCloneTimePointer(&deadline)
	value.Revision++
	return value, nil
}

func suspendedPauseResumeSource(authority PauseResumePresenceAuthority, participantID uuid.UUID, epoch int64) *pausedomain.PauseReconnectInterval {
	var result *pausedomain.PauseReconnectInterval
	for _, evidence := range authority.Resume.Pause.SuspendedReconnect {
		interval := reconnectIntervalByID(authority.Resume.Pause.Graph.Reconnect, evidence.ID)
		if interval == nil || interval.ParticipantID != participantID || interval.PresenceEpoch != epoch {
			continue
		}
		if result != nil {
			return nil
		}
		result = interval
	}
	return result
}

func pausePresenceByParticipant(values []pausedomain.PausePresence, participantID uuid.UUID) *pausedomain.PausePresence {
	for index := range values {
		if values[index].ParticipantID == participantID {
			return &values[index]
		}
	}
	return nil
}

func pauseCounterByParticipant(values []pausedomain.PauseReconnectCounter, pauseID, participantID uuid.UUID) *pausedomain.PauseReconnectCounter {
	for index := range values {
		if values[index].PauseID == pauseID && values[index].ParticipantID == participantID {
			return &values[index]
		}
	}
	return nil
}

func reconnectIntervalByID(values []pausedomain.PauseReconnectInterval, id uuid.UUID) *pausedomain.PauseReconnectInterval {
	for index := range values {
		if values[index].ID == id {
			return &values[index]
		}
	}
	return nil
}
