package game

import (
	"errors"
	"time"

	"github.com/google/uuid"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

func addPauseResumeTime(at time.Time, duration time.Duration) (time.Time, error) {
	deadline, ok := pausedomain.AddTime(at, duration)
	if !ok {
		return time.Time{}, ErrPauseResumePresenceOverflow
	}
	return deadline, nil
}

func validatePauseResumePresenceCommand(command PauseResumePresenceCommand) error {
	if err := validatePauseResumeCommand(command.Resume); err != nil {
		return pauseResumePresenceError("invalid normal resume command: %v", err)
	}
	if !validPauseResumeDecisionIDs(command) {
		return pauseResumePresenceError("invalid decision identity")
	}
	if err := validatePauseResumeDecisionExpectation(command.SeriesExpected, PauseResumeDecisionScopeSeries); err != nil {
		return err
	}
	if err := validatePauseResumeDecisionExpectation(command.GameExpected, PauseResumeDecisionScopeGameAttempt); err != nil {
		return err
	}
	if !validPauseResumeCommandTopology(command) {
		return pauseResumePresenceError("invalid pause topology")
	}
	if err := validatePauseResumeCommandBaselines(command); err != nil {
		return err
	}
	return validatePauseResumeCommandIntervals(command)
}

func validPauseResumeDecisionIDs(command PauseResumePresenceCommand) bool {
	reservedDecisionIDs := map[uuid.UUID]struct{}{
		uuid.Nil: {}, command.Resume.PauseID: {}, command.Resume.CommandID: {}, command.Resume.ActorID: {},
		command.SeriesExpected.PauseID: {}, command.GameExpected.PauseID: {},
		command.SeriesExpected.CurrentRevisionID: {}, command.GameExpected.CurrentRevisionID: {},
	}
	if _, reserved := reservedDecisionIDs[command.SeriesDecisionID]; reserved {
		return false
	}
	if _, reserved := reservedDecisionIDs[command.GameDecisionID]; reserved || command.GameDecisionID == command.SeriesDecisionID {
		return false
	}
	return true
}

func validPauseResumeCommandTopology(command PauseResumePresenceCommand) bool {
	return command.SeriesExpected.ParentPauseID == nil && command.SeriesExpected.Depth == 0 &&
		command.GameExpected.ParentPauseID != nil && *command.GameExpected.ParentPauseID == command.SeriesExpected.PauseID &&
		command.GameExpected.Depth == 1 && command.SeriesExpected.SeriesID == command.GameExpected.SeriesID &&
		command.Resume.PauseID != command.SeriesExpected.PauseID && command.Resume.PauseID != command.GameExpected.PauseID
}

func validatePauseResumeCommandBaselines(command PauseResumePresenceCommand) error {
	if !validPauseResumeFrozenBaseline(command.FrozenDeadlines) {
		return pauseResumePresenceError("invalid frozen deadline baseline")
	}
	if !validPauseResumePresenceBaseline(command.Presence) {
		return pauseResumePresenceError("invalid Presence baseline")
	}
	if !validPauseResumeReconnectBaseline(command.Reconnect) {
		return pauseResumePresenceError("invalid Reconnect baseline")
	}
	if !validPauseResumeCounterBaseline(command.Counters) {
		return pauseResumePresenceError("invalid counter baseline")
	}
	return nil
}

func validatePauseResumeCommandIntervals(command PauseResumePresenceCommand) error {
	if err := validatePauseResumeIntervalInput(command.FirstInterval); err != nil {
		return err
	}
	if err := validatePauseResumeIntervalInput(command.SecondInterval); err != nil {
		return err
	}
	if command.FirstInterval != nil && command.SecondInterval != nil &&
		command.FirstInterval.IntervalID == command.SecondInterval.IntervalID {
		return pauseResumePresenceError("duplicate interval identity")
	}
	return nil
}

func validatePauseResumeIntervalInput(input *PauseResumeIntervalInput) error {
	if input == nil {
		return nil
	}
	if input.ParticipantID == uuid.Nil || input.IntervalID == uuid.Nil || input.Window < 0 {
		return pauseResumePresenceError("invalid interval input")
	}
	return nil
}

func validatePauseResumeDecisionExpectation(value PauseResumeDecisionExpectation, scope PauseResumeDecisionScopeKind) error {
	if !validPauseResumeDecisionExpectationHeader(value, scope) {
		return pauseResumePresenceError("invalid decision expectation")
	}
	switch scope {
	case PauseResumeDecisionScopeSeries:
		if !validPauseResumeSeriesExpectation(value) {
			return pauseResumePresenceError("invalid Series expectation")
		}
	case PauseResumeDecisionScopeGameAttempt:
		if !validPauseResumeGameExpectation(value) {
			return pauseResumePresenceError("invalid Game expectation")
		}
	default:
		return pauseResumePresenceError("invalid decision scope")
	}
	return nil
}

func validPauseResumeDecisionExpectationHeader(value PauseResumeDecisionExpectation, scope PauseResumeDecisionScopeKind) bool {
	return value.PauseID != uuid.Nil && value.ScopeKind == scope && value.CurrentRevisionID != uuid.Nil &&
		value.State == PauseStateActive && value.Revision >= 1 && value.SeriesID != uuid.Nil &&
		value.DecisionNumber >= 0 && !value.StartedAt.IsZero()
}

func validPauseResumeSeriesExpectation(value PauseResumeDecisionExpectation) bool {
	return value.GameID == uuid.Nil && value.ParentPauseID == nil && value.Depth == 0 && value.GameClock == nil
}

func validPauseResumeGameExpectation(value PauseResumeDecisionExpectation) bool {
	return value.GameID != uuid.Nil && value.ParentPauseID != nil && value.Depth == 1 && value.GameClock != nil &&
		validatePauseResumeGameClockExpectation(*value.GameClock) == nil && value.GameClock.PauseID == value.PauseID &&
		value.GameClock.GameID == value.GameID
}

func validatePauseResumePresenceAuthority(authority PauseResumePresenceAuthority) error {
	if err := validatePauseResumePresenceOwnership(authority); err != nil {
		return err
	}
	if pauseResumePresenceHasExhaustedFreshAbsence(authority) {
		return ErrPauseResumePresenceIneligible
	}
	resume := pauseResumeAuthorityWithOrderedCounters(authority.Resume)
	if err := validatePauseResumeAuthority(resume); err != nil {
		if errors.Is(err, ErrPauseResumeIncomplete) {
			return ErrPauseResumePresenceIncomplete
		}
		return pauseResumePresenceError("invalid normal pause authority: %v", err)
	}
	normal := authority.Resume.Pause
	if !validPauseResumeNormalAuthority(normal) {
		return pauseResumePresenceError("normal pause is not an active Wave pause")
	}
	if err := validatePauseResumeDecisionAuthority(authority.SeriesDecision, PauseResumeDecisionScopeSeries); err != nil {
		return err
	}
	if err := validatePauseResumeDecisionAuthority(authority.GameDecision, PauseResumeDecisionScopeGameAttempt); err != nil {
		return err
	}
	series := authority.SeriesDecision
	game := authority.GameDecision
	if !validPauseResumeIndependentTopology(normal, series, game) {
		return pauseResumePresenceError("invalid independent pause topology")
	}
	graphSeries := pauseSeriesByID(normal.Graph.Series, series.SeriesID)
	graphGame := pauseGameByID(normal.Graph.Games, game.GameID)
	if graphSeries == nil || graphGame == nil || graphGame.SeriesID != series.SeriesID {
		return ErrPauseResumePresenceConflict
	}
	return nil
}

func validPauseResumeNormalAuthority(value NormalPauseRecord) bool {
	return value.ScopeKind == pausedomain.ScopeWave && value.ScopeID == value.Scope.WaveID && value.State == PauseStateActive
}

func validPauseResumeIndependentTopology(
	normal NormalPauseRecord,
	series PauseResumeDecisionAuthority,
	game PauseResumeDecisionAuthority,
) bool {
	return series.ParentPauseID == nil && series.Depth == 0 && game.ParentPauseID != nil &&
		*game.ParentPauseID == series.PauseID && game.Depth == 1 && series.SeriesID == game.SeriesID &&
		series.StartedAt.Before(normal.PausedAt) && game.StartedAt.Before(normal.PausedAt) &&
		series.PauseID != normal.PauseID && game.PauseID != normal.PauseID
}

func validatePauseResumePresenceOwnership(authority PauseResumePresenceAuthority) error {
	for _, counter := range authority.Resume.Counters {
		if counter.PauseID != authority.GameDecision.PauseID {
			return pauseResumePresenceError("counter belongs to another pause")
		}
	}
	for _, interval := range authority.Resume.Reconnect {
		if interval.PauseID != authority.GameDecision.PauseID {
			return pauseResumePresenceError("Reconnect belongs to another pause")
		}
	}
	return nil
}

func pauseResumePresenceHasExhaustedFreshAbsence(authority PauseResumePresenceAuthority) bool {
	for _, live := range authority.Resume.Presence {
		if pauseResumePresenceFreshSlotExhausted(authority, live) {
			return true
		}
	}
	return false
}

func pauseResumePresenceFreshSlotExhausted(authority PauseResumePresenceAuthority, live pausedomain.PausePresence) bool {
	snapshot := pausePresenceByParticipant(authority.Resume.Pause.Graph.Presence, live.ParticipantID)
	counter := pauseCounterByParticipant(authority.Resume.Counters, authority.GameDecision.PauseID, live.ParticipantID)
	snapshotCounter := pauseCounterByParticipant(authority.Resume.Pause.Graph.Counters, authority.GameDecision.PauseID, live.ParticipantID)
	if snapshot == nil || counter == nil || snapshotCounter == nil || validatePausePresence(live) != nil {
		return false
	}
	return pauseResumeFreshPresenceMatches(*snapshot, live, authority.Resume.Pause.PausedAt) &&
		pauseResumeFreshCounterExhausted(*counter, *snapshotCounter)
}

func pauseResumeFreshPresenceMatches(snapshot, live pausedomain.PausePresence, pausedAt time.Time) bool {
	return samePausePresenceIdentity(snapshot, live) && live.State == pausedomain.PresenceStateDisconnected &&
		live.PresenceEpoch > snapshot.PresenceEpoch &&
		live.PresenceEpoch-snapshot.PresenceEpoch == live.Revision-snapshot.Revision && !live.UpdatedAt.Before(pausedAt)
}

func pauseResumeFreshCounterExhausted(counter, snapshot pausedomain.PauseReconnectCounter) bool {
	return counter.PauseID == snapshot.PauseID && counter.RosterID == snapshot.RosterID &&
		counter.ParticipantID == snapshot.ParticipantID && counter.Used == snapshot.Used &&
		counter.Revision == snapshot.Revision && counter.Limit >= 0 && counter.Used == counter.Limit
}

func pauseResumeAuthorityWithOrderedCounters(value PauseResumeAuthority) PauseResumeAuthority {
	clone := value
	if len(clone.Counters) != len(clone.Pause.Graph.Counters) {
		return clone
	}
	ordered := make([]pausedomain.PauseReconnectCounter, 0, len(clone.Counters))
	for _, snapshot := range clone.Pause.Graph.Counters {
		counter := pauseCounterByParticipant(clone.Counters, snapshot.PauseID, snapshot.ParticipantID)
		if counter == nil {
			return clone
		}
		ordered = append(ordered, *counter)
	}
	clone.Counters = ordered
	return clone
}

func validatePauseResumeDecisionAuthority(value PauseResumeDecisionAuthority, scope PauseResumeDecisionScopeKind) error {
	expected := PauseResumeDecisionExpectationFrom(value)
	if err := validatePauseResumeDecisionExpectation(expected, scope); err != nil {
		return err
	}
	if scope == PauseResumeDecisionScopeGameAttempt && validatePauseResumeGameClock(*value.GameClock, true) != nil {
		return pauseResumePresenceError("invalid Game authority clock")
	}
	return nil
}

func validatePauseResumeGameClockExpectation(value pausedomain.PauseResumeGameClock) error {
	if value.PauseID == uuid.Nil || value.GameID == uuid.Nil || value.Remaining <= 0 || value.Revision < 1 ||
		value.OriginalDeadline.IsZero() || value.FrozenAt.IsZero() ||
		value.ResumedAt != nil || value.ResumedDeadline != nil {
		return pauseResumePresenceError("invalid Game clock expectation")
	}
	return nil
}

func validatePauseResumeGameClock(value pausedomain.PauseResumeGameClock, active bool) error {
	if !validPauseResumeGameClockHeader(value) {
		return pauseResumePresenceError("invalid Game clock")
	}
	if active {
		if value.ResumedAt != nil || value.ResumedDeadline != nil {
			return pauseResumePresenceError("active Game clock is resumed")
		}
		return nil
	}
	if !validResumedPauseResumeGameClock(value) {
		return pauseResumePresenceError("resolved Game clock lacks shifted deadline")
	}
	return nil
}

func validPauseResumeGameClockHeader(value pausedomain.PauseResumeGameClock) bool {
	return value.PauseID != uuid.Nil && value.GameID != uuid.Nil && value.Remaining > 0 && value.Revision >= 1 &&
		!value.OriginalDeadline.IsZero() && !value.FrozenAt.IsZero() && value.OriginalDeadline.After(value.FrozenAt) &&
		value.OriginalDeadline.Sub(value.FrozenAt) == value.Remaining
}

func validResumedPauseResumeGameClock(value pausedomain.PauseResumeGameClock) bool {
	return value.ResumedAt != nil && value.ResumedDeadline != nil && !value.ResumedAt.IsZero() &&
		!value.ResumedDeadline.IsZero() && value.ResumedDeadline.Sub(*value.ResumedAt) == value.Remaining
}

func pauseSeriesByID(values []PauseSeries, id uuid.UUID) *PauseSeries {
	for index := range values {
		if values[index].Execution.Series.ID == id {
			return &values[index]
		}
	}
	return nil
}

func validPauseResumeFrozenBaseline(values []PauseFrozenDeadline) bool {
	seen := make(map[pauseDeadlineIdentity]struct{}, len(values))
	for _, value := range values {
		key := pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}
		if validateFrozenDeadline(pauseResumeCanonicalFrozenDeadline(value), true) != nil {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validPauseResumePresenceBaseline(values []pausedomain.PausePresence) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if validatePausePresence(pauseResumeCanonicalPresence(value)) != nil {
			return false
		}
		if _, duplicate := seen[value.ParticipantID]; duplicate {
			return false
		}
		seen[value.ParticipantID] = struct{}{}
	}
	return true
}

func validPauseResumeReconnectBaseline(values []pausedomain.PauseReconnectInterval) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if validatePauseReconnect(pauseResumeCanonicalReconnect(value)) != nil {
			return false
		}
		if _, duplicate := seen[value.ID]; duplicate {
			return false
		}
		seen[value.ID] = struct{}{}
	}
	return true
}

func validPauseResumeCounterBaseline(values []pausedomain.PauseReconnectCounter) bool {
	seen := make(map[[3]uuid.UUID]struct{}, len(values))
	for _, value := range values {
		key := [3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}
		if value.PauseID == uuid.Nil || value.RosterID == uuid.Nil || value.ParticipantID == uuid.Nil ||
			value.Limit < 0 || value.Used < 0 || value.Used > value.Limit || value.Revision < 1 {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func pauseResumeBaselineProjectionsMatch(command PauseResumePresenceCommand) bool {
	presence := make([]PausePresenceRevision, len(command.Presence))
	for index, value := range command.Presence {
		presence[index] = PausePresenceRevision{
			ID: value.ID, TournamentID: value.TournamentID, RosterID: value.RosterID, SeriesID: value.SeriesID,
			ParticipantID: value.ParticipantID, PresenceEpoch: value.PresenceEpoch, Revision: value.Revision,
		}
	}
	reconnect := make([]PauseChildRevision, len(command.Reconnect))
	for index, value := range command.Reconnect {
		reconnect[index] = PauseChildRevision{ID: value.ID, Revision: value.Revision}
	}
	counters := make([]PauseReconnectCounterRevision, len(command.Counters))
	for index, value := range command.Counters {
		counters[index] = PauseReconnectCounterRevision{
			PauseID: value.PauseID, RosterID: value.RosterID, ParticipantID: value.ParticipantID, Revision: value.Revision,
		}
	}
	frozen := make([]PauseFrozenDeadlineRevision, len(command.FrozenDeadlines))
	for index, value := range command.FrozenDeadlines {
		frozen[index] = PauseFrozenDeadlineRevision{Kind: value.Kind, OwnerID: value.OwnerID, Revision: value.Revision}
	}
	expected := command.Resume.Expected
	return presenceRevisionMapEqual(presence, expected.Presence) && revisionMapEqual(reconnect, expected.Reconnect) &&
		counterRevisionMapEqual(counters, expected.Counters) && frozenRevisionMapEqual(frozen, expected.FrozenDeadlines)
}
