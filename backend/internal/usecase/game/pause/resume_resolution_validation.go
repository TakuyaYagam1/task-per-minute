package pause

import (
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

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
	presence *pausedomain.PausePresence,
	counter *pausedomain.PauseReconnectCounter,
	expectedCounter *pausedomain.PauseReconnectCounter,
	value PauseResumeParticipantResolution,
) bool {
	return presence != nil && counter != nil && expectedCounter != nil &&
		presence.PresenceEpoch == value.PresenceEpoch && *counter == value.Counter
}

func pauseResumeConnectedResolutionMatches(
	presence pausedomain.PausePresence,
	expectedCounter pausedomain.PauseReconnectCounter,
	value PauseResumeParticipantResolution,
	input *PauseResumeIntervalInput,
) bool {
	return presence.State == pausedomain.PresenceStateConnected && value.Disposition == PauseResumeParticipantConnected &&
		value.SourceInterval == nil && input == nil && value.Counter == expectedCounter
}

func pauseResumeCurrentIntervalMatches(
	command PauseResumePresenceCommand,
	value PauseResumeParticipantResolution,
	input *PauseResumeIntervalInput,
	presence *pausedomain.PausePresence,
	current *pausedomain.PauseReconnectInterval,
	decidedAt time.Time,
) bool {
	if input == nil || presence == nil || current == nil || input.ParticipantID != value.ParticipantID ||
		input.IntervalID != value.CurrentInterval.ID || presence.State != pausedomain.PresenceStateDisconnected ||
		!pauseResumeReconnectEqual(*current, *value.CurrentInterval) {
		return false
	}
	return pauseResumeCurrentIntervalIdentityMatches(command, value, *current) &&
		pauseResumeOpenIntervalStateMatches(*current, decidedAt)
}

func pauseResumeCurrentIntervalIdentityMatches(
	command PauseResumePresenceCommand,
	value PauseResumeParticipantResolution,
	current pausedomain.PauseReconnectInterval,
) bool {
	return current.PauseID == command.GameExpected.PauseID && current.RosterID == command.Resume.Scope.RosterID &&
		current.SeriesID == command.GameExpected.SeriesID && current.GameID == command.GameExpected.GameID &&
		current.ParticipantID == value.ParticipantID && current.PresenceEpoch == value.PresenceEpoch
}

func pauseResumeOpenIntervalStateMatches(current pausedomain.PauseReconnectInterval, decidedAt time.Time) bool {
	return current.State == pausedomain.ReconnectStateOpen && current.ClosedAt == nil && current.SuspendedByPauseID == nil &&
		current.Revision == 1 && current.OpenedAt.Equal(decidedAt) && current.UpdatedAt.Equal(decidedAt)
}

func pauseResumeContinuationResolutionMatches(
	graph PauseGraph,
	command PauseResumePresenceCommand,
	value PauseResumeParticipantResolution,
	input PauseResumeIntervalInput,
	current pausedomain.PauseReconnectInterval,
	expectedCounter pausedomain.PauseReconnectCounter,
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
	expectedDeadline, ok := pausedomain.AddTime(current.OpenedAt, baselineSource.Deadline.Sub(*baselineSource.ClosedAt))
	return ok && current.Deadline.Equal(expectedDeadline)
}

func pauseResumeContinuationSourceMatches(
	source *pausedomain.PauseReconnectInterval,
	baseline *pausedomain.PauseReconnectInterval,
	resolutionSource *pausedomain.PauseReconnectInterval,
	pauseID uuid.UUID,
	current pausedomain.PauseReconnectInterval,
) bool {
	return source != nil && baseline != nil && resolutionSource != nil && baseline.ClosedAt != nil &&
		pauseResumeReconnectEqual(*source, *baseline) && pauseResumeReconnectEqual(*resolutionSource, *baseline) &&
		source.SuspendedByPauseID != nil && *source.SuspendedByPauseID == pauseID &&
		current.Number == baseline.Number && current.ContinuationNumber == baseline.ContinuationNumber+1
}

func pauseResumeFreshResolutionMatches(
	value PauseResumeParticipantResolution,
	input PauseResumeIntervalInput,
	current pausedomain.PauseReconnectInterval,
	expectedCounter pausedomain.PauseReconnectCounter,
) bool {
	expectedDeadline, ok := pausedomain.AddTime(current.OpenedAt, input.Window)
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

func pauseResumeBaselineCounter(values []pausedomain.PauseReconnectCounter, pauseID, participantID uuid.UUID) *pausedomain.PauseReconnectCounter {
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
		deadline, ok := pausedomain.AddTime(decidedAt, expected.Remaining)
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
	values []pausedomain.PauseReconnectInterval,
	baseline []pausedomain.PauseReconnectInterval,
	first *pausedomain.PauseReconnectInterval,
	second *pausedomain.PauseReconnectInterval,
) bool {
	existing := make(map[uuid.UUID]pausedomain.PauseReconnectInterval, len(baseline))
	for _, value := range baseline {
		if _, duplicate := existing[value.ID]; duplicate {
			return false
		}
		existing[value.ID] = value
	}
	created := make(map[uuid.UUID]struct{}, 2)
	for _, value := range []*pausedomain.PauseReconnectInterval{first, second} {
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
	current []pausedomain.PauseReconnectCounter,
	baseline []pausedomain.PauseReconnectCounter,
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

func decisionIntervalMatches(id *uuid.UUID, interval *pausedomain.PauseReconnectInterval) bool {
	if id == nil || interval == nil {
		return id == nil && interval == nil
	}
	return *id == interval.ID
}
