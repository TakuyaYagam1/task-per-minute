package arena

import (
	"errors"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"
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
		record.NormalPauseResolvedAt = cloneTimePointer(&decidedAt)
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
		graph.Counters = append([]PauseReconnectCounter(nil), authority.Resume.Counters...)
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
	if evidence.live.State == PresenceStateConnected {
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
	live     PausePresence
	snapshot PausePresence
	counter  PauseReconnectCounter
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

func (value pauseResumeParticipantEvidence) isContinuation(source *PauseReconnectInterval) bool {
	return value.snapshot.State == PresenceStateDisconnected && value.live.PresenceEpoch == value.snapshot.PresenceEpoch && source != nil
}

func resolvePauseResumeContinuation(
	resolution PauseResumeParticipantResolution,
	source *PauseReconnectInterval,
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

func newPauseResumeContinuation(source PauseReconnectInterval, id uuid.UUID, decidedAt time.Time) (PauseReconnectInterval, error) {
	if source.State != ReconnectStateCancelled || source.ClosedAt == nil || source.SuspendedByPauseID == nil ||
		source.ContinuationNumber == math.MaxInt {
		return PauseReconnectInterval{}, pauseResumePresenceError("invalid continuation source")
	}
	remaining := source.Deadline.Sub(*source.ClosedAt)
	deadline, err := addPauseResumeTime(decidedAt, remaining)
	if err != nil {
		return PauseReconnectInterval{}, err
	}
	return PauseReconnectInterval{
		ID: id, PauseID: source.PauseID, RosterID: source.RosterID, SeriesID: source.SeriesID, GameID: source.GameID,
		ParticipantID: source.ParticipantID, PresenceEpoch: source.PresenceEpoch, Number: source.Number,
		ContinuationNumber: source.ContinuationNumber + 1, ContinuedFromID: cloneUUIDPointer(&source.ID),
		State: ReconnectStateOpen, OpenedAt: decidedAt, Deadline: deadline, Revision: 1, UpdatedAt: decidedAt,
	}, nil
}

func newPauseResumeFresh(authority PauseResumePresenceAuthority, presence PausePresence, counter PauseReconnectCounter, id uuid.UUID, window time.Duration, decidedAt time.Time) (PauseReconnectInterval, error) {
	deadline, err := addPauseResumeTime(decidedAt, window)
	if err != nil {
		return PauseReconnectInterval{}, err
	}
	return PauseReconnectInterval{
		ID: id, PauseID: authority.GameDecision.PauseID, RosterID: authority.Resume.Pause.Scope.RosterID,
		SeriesID: authority.GameDecision.SeriesID, GameID: authority.GameDecision.GameID,
		ParticipantID: presence.ParticipantID, PresenceEpoch: presence.PresenceEpoch,
		Number: counter.Used + 1, ContinuationNumber: 0, State: ReconnectStateOpen,
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

func newPauseResumeDecisionRecord(id uuid.UUID, authority PauseResumeDecisionAuthority, action PauseResumePresenceAction, first, second *PauseReconnectInterval, decidedAt time.Time) PauseResumeDecisionRecord {
	record := PauseResumeDecisionRecord{ID: id, PauseID: authority.PauseID, DecisionNumber: authority.DecisionNumber + 1, Action: action, DecidedAt: decidedAt}
	if first != nil {
		record.FirstReconnectIntervalID = cloneUUIDPointer(&first.ID)
	}
	if second != nil {
		record.SecondReconnectIntervalID = cloneUUIDPointer(&second.ID)
	}
	return record
}

func resumePauseGameClock(value PauseResumeGameClock, decidedAt time.Time) (PauseResumeGameClock, error) {
	if validatePauseResumeGameClock(value, true) != nil || value.Revision == math.MaxInt64 {
		return PauseResumeGameClock{}, ErrPauseResumePresenceOverflow
	}
	deadline, err := addPauseResumeTime(decidedAt, value.Remaining)
	if err != nil {
		return PauseResumeGameClock{}, err
	}
	value.ResumedAt = cloneTimePointer(&decidedAt)
	value.ResumedDeadline = cloneTimePointer(&deadline)
	value.Revision++
	return value, nil
}

func suspendedPauseResumeSource(authority PauseResumePresenceAuthority, participantID uuid.UUID, epoch int64) *PauseReconnectInterval {
	var result *PauseReconnectInterval
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

func pausePresenceByParticipant(values []PausePresence, participantID uuid.UUID) *PausePresence {
	for index := range values {
		if values[index].ParticipantID == participantID {
			return &values[index]
		}
	}
	return nil
}

func pauseCounterByParticipant(values []PauseReconnectCounter, pauseID, participantID uuid.UUID) *PauseReconnectCounter {
	for index := range values {
		if values[index].PauseID == pauseID && values[index].ParticipantID == participantID {
			return &values[index]
		}
	}
	return nil
}

func reconnectIntervalByID(values []PauseReconnectInterval, id uuid.UUID) *PauseReconnectInterval {
	for index := range values {
		if values[index].ID == id {
			return &values[index]
		}
	}
	return nil
}

func validatePauseResumePresenceRecord(record PauseResumePresenceRecord) error {
	if err := validatePauseResumePresenceRecordHeader(record); err != nil {
		return err
	}
	if err := validatePauseResumeParticipantOrder(record); err != nil {
		return err
	}
	action := pauseResumeAction(record.First.CurrentInterval != nil, record.Second.CurrentInterval != nil)
	if !pauseResumeDecisionWiringMatches(record, action) {
		return pauseResumePresenceError("decision does not match participant intervals")
	}
	if action == PauseResumeActionResume {
		return validatePauseResumeCompleteRecord(record)
	}
	if err := validatePauseResumeWaitRecord(record); err != nil {
		return err
	}
	return validatePauseResumeParticipantRecords(record)
}

func validatePauseResumePresenceRecordHeader(record PauseResumePresenceRecord) error {
	if err := validatePauseResumePresenceCommand(record.Command); err != nil {
		return err
	}
	if !pauseResumeBaselineProjectionsMatch(record.Command) {
		return pauseResumePresenceError("command baselines do not match revision projections")
	}
	decision := record.GameDecision
	if record.DecidedAt.IsZero() || decision.ID != record.Command.GameDecisionID ||
		decision.PauseID != record.Command.GameExpected.PauseID ||
		decision.DecisionNumber != record.Command.GameExpected.DecisionNumber+1 ||
		!decision.DecidedAt.Equal(record.DecidedAt) || !validPauseResumePresenceAction(decision.Action) {
		return pauseResumePresenceError("invalid Game decision record")
	}
	return nil
}

func validatePauseResumeParticipantOrder(record PauseResumePresenceRecord) error {
	if record.First.ParticipantID == uuid.Nil || record.Second.ParticipantID == uuid.Nil || record.First.ParticipantID == record.Second.ParticipantID {
		return pauseResumePresenceError("invalid participant resolution order")
	}
	series := pauseSeriesByID(record.Graph.Series, record.Command.SeriesExpected.SeriesID)
	if series == nil || record.First.ParticipantID != series.Execution.Series.FirstParticipantID ||
		record.Second.ParticipantID != series.Execution.Series.SecondParticipantID {
		return pauseResumePresenceError("participant resolution order does not match Series")
	}
	return nil
}

func pauseResumeDecisionWiringMatches(record PauseResumePresenceRecord, action PauseResumePresenceAction) bool {
	return action == record.GameDecision.Action &&
		decisionIntervalMatches(record.GameDecision.FirstReconnectIntervalID, record.First.CurrentInterval) &&
		decisionIntervalMatches(record.GameDecision.SecondReconnectIntervalID, record.Second.CurrentInterval)
}

func validatePauseResumeCompleteRecord(record PauseResumePresenceRecord) error {
	if !pauseResumeSeriesDecisionMatches(record) || !pauseResumeResolvedStatesMatch(record) {
		return pauseResumePresenceError("invalid complete resume record")
	}
	base := pauseResumeBaseRecord(record)
	expectedClock, clockErr := resumePauseGameClock(*record.Command.GameExpected.GameClock, record.DecidedAt)
	if validatePauseResumeRecord(base) != nil || clockErr != nil || !pauseResumeGameClockEqual(record.GameClock, expectedClock) ||
		!pauseResumeResolvedBaselineMatches(record) ||
		!pauseResumeResolvedFrozenMatches(record.Graph.FrozenDeadlines, record.Command.FrozenDeadlines, record.DecidedAt) {
		return pauseResumePresenceError("invalid resolved graph or Game clock")
	}
	return validatePauseResumeParticipantRecords(record)
}

func pauseResumeSeriesDecisionMatches(record PauseResumePresenceRecord) bool {
	decision := record.SeriesDecision
	return decision != nil && decision.ID == record.Command.SeriesDecisionID &&
		decision.PauseID == record.Command.SeriesExpected.PauseID &&
		decision.DecisionNumber == record.Command.SeriesExpected.DecisionNumber+1 &&
		decision.Action == PauseResumeActionResume && decision.DecidedAt.Equal(record.DecidedAt) &&
		decision.FirstReconnectIntervalID == nil && decision.SecondReconnectIntervalID == nil
}

func pauseResumeResolvedStatesMatch(record PauseResumePresenceRecord) bool {
	return record.GamePauseState == PauseStateResumed && record.SeriesPauseState == PauseStateResumed &&
		record.NormalPauseState == PauseStateResumed && record.NormalPauseResolvedAt != nil &&
		record.NormalPauseResolvedAt.Equal(record.DecidedAt)
}

func pauseResumeBaseRecord(record PauseResumePresenceRecord) PauseResumeRecord {
	return PauseResumeRecord{
		Scope: record.Command.Resume.Scope, PauseID: record.Command.Resume.PauseID, CommandID: record.Command.Resume.CommandID,
		ActorID: record.Command.Resume.ActorID, DraftResultRevisionID: record.Command.Resume.DraftResultRevisionID,
		State: PauseStateResumed, Revision: record.Command.Resume.Expected.PauseRevision + 1,
		Expected: clonePauseResumeExpectation(record.Command.Resume.Expected), Graph: clonePauseGraph(record.Graph), ResumedAt: record.DecidedAt,
	}
}

func validatePauseResumeWaitRecord(record PauseResumePresenceRecord) error {
	if record.SeriesDecision != nil || !pauseResumeWaitStatesMatch(record) ||
		record.Command.GameExpected.GameClock == nil || !pauseResumeGameClockEqual(record.GameClock, *record.Command.GameExpected.GameClock) ||
		record.Graph.Revision != record.Command.Resume.Expected.GraphRevision+1 || !pauseResumeWaitGraphHeaderMatches(record) ||
		validatePauseGraph(record.Graph, true) != nil || !validatePauseResumeWaitGraph(record) {
		return pauseResumePresenceError("invalid wait record")
	}
	return nil
}

func pauseResumeWaitStatesMatch(record PauseResumePresenceRecord) bool {
	return record.GamePauseState == PauseStateActive && record.SeriesPauseState == PauseStateActive &&
		record.NormalPauseState == PauseStateActive && record.NormalPauseResolvedAt == nil
}

func pauseResumeWaitGraphHeaderMatches(record PauseResumePresenceRecord) bool {
	return record.Graph.ActivePauseID == record.Command.Resume.PauseID && record.Graph.PausedAt != nil &&
		record.Graph.PausedAt.Before(record.DecidedAt)
}

func validatePauseResumeParticipantRecords(record PauseResumePresenceRecord) error {
	if !validatePauseResumeParticipantResolution(record.Graph, record.Command, record.First, record.Command.FirstInterval, record.DecidedAt) ||
		!validatePauseResumeParticipantResolution(record.Graph, record.Command, record.Second, record.Command.SecondInterval, record.DecidedAt) {
		return pauseResumePresenceError("invalid participant resolution")
	}
	return nil
}

func validatePauseResumeParticipantResolution(
	graph PauseGraph,
	command PauseResumePresenceCommand,
	value PauseResumeParticipantResolution,
	input *PauseResumeIntervalInput,
	decidedAt time.Time,
) bool {
	presence := pausePresenceByParticipant(graph.Presence, value.ParticipantID)
	counter := pauseCounterByParticipant(graph.Counters, value.Counter.PauseID, value.ParticipantID)
	expectedCounter := pauseResumeBaselineCounter(command.Counters, value.Counter.PauseID, value.ParticipantID)
	if !pauseResumeResolutionBaselineMatches(presence, counter, expectedCounter, value) {
		return false
	}
	if value.CurrentInterval == nil {
		return pauseResumeConnectedResolutionMatches(*presence, *expectedCounter, value, input)
	}
	current := reconnectIntervalByID(graph.Reconnect, value.CurrentInterval.ID)
	if !pauseResumeCurrentIntervalMatches(command, value, input, presence, current, decidedAt) {
		return false
	}
	if value.Disposition == PauseResumeParticipantContinuation {
		return pauseResumeContinuationResolutionMatches(graph, command, value, *input, *current, *expectedCounter)
	}
	return pauseResumeFreshResolutionMatches(value, *input, *current, *expectedCounter)
}

func pauseResumeResolutionBaselineMatches(
	presence *PausePresence,
	counter *PauseReconnectCounter,
	expectedCounter *PauseReconnectCounter,
	value PauseResumeParticipantResolution,
) bool {
	return presence != nil && counter != nil && expectedCounter != nil &&
		presence.PresenceEpoch == value.PresenceEpoch && *counter == value.Counter
}

func pauseResumeConnectedResolutionMatches(
	presence PausePresence,
	expectedCounter PauseReconnectCounter,
	value PauseResumeParticipantResolution,
	input *PauseResumeIntervalInput,
) bool {
	return presence.State == PresenceStateConnected && value.Disposition == PauseResumeParticipantConnected &&
		value.SourceInterval == nil && input == nil && value.Counter == expectedCounter
}

func pauseResumeCurrentIntervalMatches(
	command PauseResumePresenceCommand,
	value PauseResumeParticipantResolution,
	input *PauseResumeIntervalInput,
	presence *PausePresence,
	current *PauseReconnectInterval,
	decidedAt time.Time,
) bool {
	if input == nil || presence == nil || current == nil || input.ParticipantID != value.ParticipantID ||
		input.IntervalID != value.CurrentInterval.ID || presence.State != PresenceStateDisconnected ||
		!pauseResumeReconnectEqual(*current, *value.CurrentInterval) {
		return false
	}
	return pauseResumeCurrentIntervalIdentityMatches(command, value, *current) &&
		pauseResumeOpenIntervalStateMatches(*current, decidedAt)
}

func pauseResumeCurrentIntervalIdentityMatches(
	command PauseResumePresenceCommand,
	value PauseResumeParticipantResolution,
	current PauseReconnectInterval,
) bool {
	return current.PauseID == command.GameExpected.PauseID && current.RosterID == command.Resume.Scope.RosterID &&
		current.SeriesID == command.GameExpected.SeriesID && current.GameID == command.GameExpected.GameID &&
		current.ParticipantID == value.ParticipantID && current.PresenceEpoch == value.PresenceEpoch
}

func pauseResumeOpenIntervalStateMatches(current PauseReconnectInterval, decidedAt time.Time) bool {
	return current.State == ReconnectStateOpen && current.ClosedAt == nil && current.SuspendedByPauseID == nil &&
		current.Revision == 1 && current.OpenedAt.Equal(decidedAt) && current.UpdatedAt.Equal(decidedAt)
}

func pauseResumeContinuationResolutionMatches(
	graph PauseGraph,
	command PauseResumePresenceCommand,
	value PauseResumeParticipantResolution,
	input PauseResumeIntervalInput,
	current PauseReconnectInterval,
	expectedCounter PauseReconnectCounter,
) bool {
	if input.Window != 0 || value.SourceInterval == nil || current.ContinuedFromID == nil ||
		*current.ContinuedFromID != value.SourceInterval.ID || value.Counter != expectedCounter {
		return false
	}
	source := reconnectIntervalByID(graph.Reconnect, value.SourceInterval.ID)
	baselineSource := reconnectIntervalByID(command.Reconnect, value.SourceInterval.ID)
	if !pauseResumeContinuationSourceMatches(source, baselineSource, value.SourceInterval, command.Resume.PauseID, current) {
		return false
	}
	expectedDeadline, ok := safePauseTimeAdd(current.OpenedAt, baselineSource.Deadline.Sub(*baselineSource.ClosedAt))
	return ok && current.Deadline.Equal(expectedDeadline)
}

func pauseResumeContinuationSourceMatches(
	source *PauseReconnectInterval,
	baseline *PauseReconnectInterval,
	resolutionSource *PauseReconnectInterval,
	pauseID uuid.UUID,
	current PauseReconnectInterval,
) bool {
	return source != nil && baseline != nil && resolutionSource != nil && baseline.ClosedAt != nil &&
		pauseResumeReconnectEqual(*source, *baseline) && pauseResumeReconnectEqual(*resolutionSource, *baseline) &&
		source.SuspendedByPauseID != nil && *source.SuspendedByPauseID == pauseID &&
		current.Number == baseline.Number && current.ContinuationNumber == baseline.ContinuationNumber+1
}

func pauseResumeFreshResolutionMatches(
	value PauseResumeParticipantResolution,
	input PauseResumeIntervalInput,
	current PauseReconnectInterval,
	expectedCounter PauseReconnectCounter,
) bool {
	expectedDeadline, ok := safePauseTimeAdd(current.OpenedAt, input.Window)
	expectedFreshCounter := expectedCounter
	if expectedCounter.Used == math.MaxInt || expectedCounter.Revision == math.MaxInt64 {
		return false
	}
	expectedFreshCounter.Used++
	expectedFreshCounter.Revision++
	return value.Disposition == PauseResumeParticipantFresh && value.SourceInterval == nil && input.Window > 0 &&
		current.Number == value.Counter.Used && current.ContinuationNumber == 0 && current.ContinuedFromID == nil &&
		value.Counter == expectedFreshCounter &&
		ok && current.Deadline.Equal(expectedDeadline)
}

func pauseResumeBaselineCounter(values []PauseReconnectCounter, pauseID, participantID uuid.UUID) *PauseReconnectCounter {
	for index := range values {
		if values[index].PauseID == pauseID && values[index].ParticipantID == participantID {
			return &values[index]
		}
	}
	return nil
}

func validatePauseResumeWaitGraph(record PauseResumePresenceRecord) bool {
	expected := record.Command.Resume.Expected
	current := PauseGraphRevisionsFrom(record.Graph)
	if record.Graph.Tournament.Revision != expected.TournamentRevision ||
		record.Graph.Tournament.PausedFromState == nil || *record.Graph.Tournament.PausedFromState != expected.TournamentState ||
		current.WaveRevision != expected.WaveRevision || !revisionMapEqual(current.Series, expected.Series) ||
		!revisionMapEqual(current.Games, expected.Games) || !reflect.DeepEqual(current.Draft, expected.Draft) ||
		current.DraftPreviousRevisionID != expected.DraftPreviousRevisionID ||
		!pauseResumePresenceSetEqual(record.Graph.Presence, record.Command.Presence) ||
		!pauseResumeFrozenBaselineEqual(record.Graph.FrozenDeadlines, record.Command.FrozenDeadlines) ||
		current.TerminalActionRevision != expected.TerminalActionRevision {
		return false
	}
	if !pauseResumeWaitReconnectBaseline(record.Graph.Reconnect, record.Command.Reconnect, record.First.CurrentInterval, record.Second.CurrentInterval) {
		return false
	}
	return pauseResumeWaitCounterBaseline(record.Graph.Counters, record.Command.Counters, record.First, record.Second)
}

func pauseResumeResolvedBaselineMatches(record PauseResumePresenceRecord) bool {
	return pauseResumePresenceSetEqual(record.Graph.Presence, record.Command.Presence) &&
		pauseResumeReconnectSetEqual(record.Graph.Reconnect, record.Command.Reconnect) &&
		pauseResumeCounterSetEqual(record.Graph.Counters, record.Command.Counters)
}

func pauseResumeResolvedFrozenMatches(current, baseline []PauseFrozenDeadline, decidedAt time.Time) bool {
	if len(current) != len(baseline) {
		return false
	}
	values := make(map[pauseDeadlineIdentity]PauseFrozenDeadline, len(current))
	for _, value := range current {
		values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}] = value
	}
	for _, expected := range baseline {
		value, exists := values[pauseDeadlineIdentity{Kind: expected.Kind, OwnerID: expected.OwnerID}]
		deadline, ok := safePauseTimeAdd(decidedAt, expected.Remaining)
		if !exists || !ok || expected.Revision == math.MaxInt64 || value.Revision != expected.Revision+1 ||
			!value.OriginalDeadline.Equal(expected.OriginalDeadline) || !value.FrozenAt.Equal(expected.FrozenAt) ||
			value.Remaining != expected.Remaining || value.ResumedAt == nil || !value.ResumedAt.Equal(decidedAt) ||
			value.ResumedDeadline == nil || !value.ResumedDeadline.Equal(deadline) {
			return false
		}
	}
	return true
}

func pauseResumeWaitReconnectBaseline(
	values []PauseReconnectInterval,
	baseline []PauseReconnectInterval,
	first *PauseReconnectInterval,
	second *PauseReconnectInterval,
) bool {
	existing := make(map[uuid.UUID]PauseReconnectInterval, len(baseline))
	for _, value := range baseline {
		if _, duplicate := existing[value.ID]; duplicate {
			return false
		}
		existing[value.ID] = value
	}
	created := make(map[uuid.UUID]struct{}, 2)
	for _, value := range []*PauseReconnectInterval{first, second} {
		if value == nil {
			continue
		}
		if _, exists := existing[value.ID]; exists {
			return false
		}
		if _, duplicate := created[value.ID]; duplicate {
			return false
		}
		created[value.ID] = struct{}{}
	}
	if len(values) != len(existing)+len(created) {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if _, duplicate := seen[value.ID]; duplicate {
			return false
		}
		seen[value.ID] = struct{}{}
		if baselineValue, ok := existing[value.ID]; ok {
			if !pauseResumeReconnectEqual(value, baselineValue) {
				return false
			}
			continue
		}
		if _, ok := created[value.ID]; !ok || value.Revision != 1 {
			return false
		}
	}
	return true
}

func pauseResumeWaitCounterBaseline(
	current []PauseReconnectCounter,
	baseline []PauseReconnectCounter,
	first PauseResumeParticipantResolution,
	second PauseResumeParticipantResolution,
) bool {
	if len(current) != len(baseline) {
		return false
	}
	dispositions := map[uuid.UUID]PauseResumeParticipantDisposition{
		first.ParticipantID:  first.Disposition,
		second.ParticipantID: second.Disposition,
	}
	seen := make(map[[3]uuid.UUID]struct{}, len(current))
	for _, value := range current {
		key := [3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
		expected := pauseResumeBaselineCounter(baseline, value.PauseID, value.ParticipantID)
		if expected == nil {
			return false
		}
		want := *expected
		if dispositions[value.ParticipantID] == PauseResumeParticipantFresh {
			if want.Used == math.MaxInt || want.Revision == math.MaxInt64 {
				return false
			}
			want.Used++
			want.Revision++
		}
		if value != want {
			return false
		}
	}
	return true
}

func validPauseResumePresenceAction(value PauseResumePresenceAction) bool {
	return value == PauseResumeActionResume || value == PauseResumeActionWaitFirst || value == PauseResumeActionWaitSecond || value == PauseResumeActionWaitBoth
}

func decisionIntervalMatches(id *uuid.UUID, interval *PauseReconnectInterval) bool {
	if id == nil || interval == nil {
		return id == nil && interval == nil
	}
	return *id == interval.ID
}

func reconcilePauseResumePresence(record PauseResumePresenceRecord, command PauseResumePresenceCommand) (*PauseResumePresenceRecord, error) {
	if validatePauseResumePresenceRecord(record) != nil || !pauseResumePresenceCommandEqual(record.Command, command) {
		return nil, ErrPauseResumePresenceCommandReuse
	}
	clone := clonePauseResumePresenceRecord(record)
	return &clone, nil
}

func clonePauseResumePresenceCommand(value PauseResumePresenceCommand) PauseResumePresenceCommand {
	clone := value
	clone.Resume.Expected = clonePauseResumeExpectation(value.Resume.Expected)
	clone.SeriesExpected = clonePauseResumeDecisionExpectation(value.SeriesExpected)
	clone.GameExpected = clonePauseResumeDecisionExpectation(value.GameExpected)
	clone.Presence = clonePausePresenceSlice(value.Presence)
	clone.Reconnect = clonePauseReconnectSlice(value.Reconnect)
	clone.Counters = clonePauseSlice(value.Counters)
	clone.FrozenDeadlines = clonePauseFrozenDeadlineSlice(value.FrozenDeadlines)
	clone.FirstInterval = clonePauseResumeIntervalInput(value.FirstInterval)
	clone.SecondInterval = clonePauseResumeIntervalInput(value.SecondInterval)
	return clone
}

func clonePauseResumeIntervalInput(value *PauseResumeIntervalInput) *PauseResumeIntervalInput {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func clonePauseResumePresenceRecord(value PauseResumePresenceRecord) PauseResumePresenceRecord {
	clone := value
	clone.Command = clonePauseResumePresenceCommand(value.Command)
	clone.GameDecision = clonePauseResumeDecisionRecord(value.GameDecision)
	if value.SeriesDecision != nil {
		decision := clonePauseResumeDecisionRecord(*value.SeriesDecision)
		clone.SeriesDecision = &decision
	}
	clone.GameClock = clonePauseResumeGameClock(value.GameClock)
	clone.NormalPauseResolvedAt = cloneTimePointer(value.NormalPauseResolvedAt)
	clone.Graph = clonePauseGraph(value.Graph)
	clone.First = clonePauseResumeParticipantResolution(value.First)
	clone.Second = clonePauseResumeParticipantResolution(value.Second)
	return clone
}

func clonePauseResumeDecisionRecord(value PauseResumeDecisionRecord) PauseResumeDecisionRecord {
	clone := value
	clone.FirstReconnectIntervalID = cloneUUIDPointer(value.FirstReconnectIntervalID)
	clone.SecondReconnectIntervalID = cloneUUIDPointer(value.SecondReconnectIntervalID)
	return clone
}

func clonePauseResumeParticipantResolution(value PauseResumeParticipantResolution) PauseResumeParticipantResolution {
	clone := value
	clone.SourceInterval = clonePauseReconnectPointer(value.SourceInterval)
	clone.CurrentInterval = clonePauseReconnectPointer(value.CurrentInterval)
	return clone
}

func clonePauseReconnectPointer(value *PauseReconnectInterval) *PauseReconnectInterval {
	if value == nil {
		return nil
	}
	clone := clonePauseReconnectSlice([]PauseReconnectInterval{*value})[0]
	return &clone
}
