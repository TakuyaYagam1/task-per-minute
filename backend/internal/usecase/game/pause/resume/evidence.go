package resume

import (
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func validatePauseResumeRecord(record PauseResumeRecord) error {
	if !validPauseResumeRecordIdentity(record) || !validPauseResumeRecordGraph(record) ||
		!validDraftResultRevisionIdentity(record.Expected.Draft, record.Expected.DraftPreviousRevisionID,
			record.DraftResultRevisionID, record.CommandID, record.PauseID, record.ActorID) {
		return pauseResumeError("invalid resolved record identity")
	}
	if err := validatePauseGraph(record.Graph, false); err != nil {
		return err
	}
	if !resolvedGraphMatchesExpectation(record.Graph, record.Expected, record.ResumedAt,
		record.DraftResultRevisionID, record.CommandID, record.ActorID) {
		return pauseResumeError("resolved graph does not match expectation")
	}
	return nil
}

func validPauseResumeRecordIdentity(record PauseResumeRecord) bool {
	return validPauseGraphScope(record.Scope) && record.PauseID != uuid.Nil && record.CommandID != uuid.Nil &&
		record.ActorID != uuid.Nil && record.State == PauseStateResumed && record.Expected.PauseRevision < math.MaxInt64 &&
		record.Revision == record.Expected.PauseRevision+1 && pauseValidServerTime(record.ResumedAt)
}

func validPauseResumeRecordGraph(record PauseResumeRecord) bool {
	return record.Graph.Scope == record.Scope && record.Graph.ActivePauseID == uuid.Nil && record.Graph.PausedAt == nil &&
		!record.Graph.DeadlinesSuppressed && resolvedGraphEvidenceAt(record.Graph, record.ResumedAt)
}

func resolvedGraphEvidenceAt(graph PauseGraph, resumedAt time.Time) bool {
	if !pauseTimeCoversGraphHistory(graph, resumedAt) {
		return false
	}
	for _, presence := range graph.Presence {
		if presence.State != pausedomain.PresenceStateConnected {
			return false
		}
	}
	for _, interval := range graph.Reconnect {
		if interval.State == pausedomain.ReconnectStateOpen {
			return false
		}
	}
	for _, frozen := range graph.FrozenDeadlines {
		if frozen.ResumedAt == nil || !frozen.ResumedAt.Equal(resumedAt) {
			return false
		}
	}
	return true
}

func resolvedGraphMatchesExpectation(
	graph PauseGraph,
	expected PauseResumeExpectation,
	resumedAt time.Time,
	draftResultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
) bool {
	if !resolvedRootMatchesExpectation(graph, expected, resumedAt) {
		return false
	}
	current := PauseGraphRevisionsFrom(graph)
	return resolvedMutableChildrenMatch(graph, expected, resumedAt, draftResultRevisionID, commandID, actorID) &&
		presenceRevisionMapEqual(current.Presence, expected.Presence) &&
		revisionMapEqual(current.Reconnect, expected.Reconnect) && counterRevisionMapEqual(current.Counters, expected.Counters) &&
		frozenRevisionsAdvanced(current.FrozenDeadlines, expected.FrozenDeadlines) &&
		current.TerminalActionRevision == expected.TerminalActionRevision
}

func resolvedRootMatchesExpectation(graph PauseGraph, expected PauseResumeExpectation, resumedAt time.Time) bool {
	return validatePauseResumeExpectation(expected) == nil && expected.GraphRevision < math.MaxInt64 &&
		expected.TournamentRevision < math.MaxInt64 && graph.Revision == expected.GraphRevision+1 &&
		graph.Tournament.Revision == expected.TournamentRevision+1 && graph.Tournament.PausedFromState == nil &&
		graph.Tournament.UpdatedAt.Equal(resumedAt) && graph.Tournament.State == expected.TournamentState &&
		resolvedWaveRevisionMatches(graph.Wave, expected.WaveRevision)
}

func resolvedMutableChildrenMatch(
	graph PauseGraph,
	expected PauseResumeExpectation,
	resumedAt time.Time,
	draftResultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
) bool {
	return resolvedSeriesRevisionsMatch(graph.Series, expected.Series) &&
		resolvedGameRevisionsMatch(graph.Games, expected.Games) &&
		resolvedDraftRevisionMatches(graph.Draft, expected.Draft, expected.DraftPreviousRevisionID,
			draftResultRevisionID, commandID, actorID, resumedAt)
}

func resolvedWaveRevisionMatches(current PauseWave, expected int64) bool {
	switch current.Wave.State {
	case domain.WaveStateActive, domain.WaveStateReadyWindowOpen, domain.WaveStateReady:
		return nextRevisionMatches(current.Revision, expected)
	case domain.WaveStatePlanned, domain.WaveStateCompleted,
		domain.WaveStateReadyWindowExpired, domain.WaveStateSuperseded:
		return current.Revision == expected
	case domain.WaveStatePaused:
		return false
	default:
		return false
	}
}

func resolvedSeriesRevisionsMatch(current []PauseSeries, expected []PauseChildRevision) bool {
	if len(current) != len(expected) {
		return false
	}
	for _, series := range current {
		revision, ok := childRevision(expected, series.Execution.Series.ID)
		if !ok || !resolvedSeriesRevisionMatches(series, revision) {
			return false
		}
	}
	return true
}

func resolvedSeriesRevisionMatches(current PauseSeries, expected int64) bool {
	switch current.Execution.Series.State {
	case domain.SeriesStateDraft, domain.SeriesStateReady,
		domain.SeriesStateActive, domain.SeriesStateReplayRequired:
		return nextRevisionMatches(current.Revision, expected)
	case domain.SeriesStatePlanned, domain.SeriesStateLocked,
		domain.SeriesStateCompleted, domain.SeriesStateCancelled:
		return current.Revision == expected
	case domain.SeriesStateTechnicalPause:
		return false
	default:
		return false
	}
}

func resolvedGameRevisionsMatch(current []PauseGame, expected []PauseChildRevision) bool {
	if len(current) != len(expected) {
		return false
	}
	for _, game := range current {
		revision, ok := childRevision(expected, game.Game.ID)
		if !ok || !resolvedGameRevisionMatches(game, revision) {
			return false
		}
	}
	return true
}

func resolvedGameRevisionMatches(current PauseGame, expected int64) bool {
	switch current.Game.State {
	case domain.GameStateActive:
		return nextRevisionMatches(current.Revision, expected)
	case domain.GameStatePlanned, domain.GameStateReady, domain.GameStateCompleted,
		domain.GameStateVoid, domain.GameStateCancelled, domain.GameStateSuperseded:
		return current.Revision == expected
	case domain.GameStatePaused:
		return false
	default:
		return false
	}
}

func resolvedDraftRevisionMatches(
	current *draftusecase.Execution,
	expected *draftusecase.RevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	resumedAt time.Time,
) bool {
	if current == nil || expected == nil {
		return absentDraftRevisionMatches(current, expected, resultRevisionID)
	}
	if current.ServiceEpoch != expected.ServiceEpoch {
		return false
	}
	switch current.State {
	case draftusecase.ExecutionStateActive:
		return resolvedDraftMutationMatches(current, expected, expectedPreviousRevisionID,
			resultRevisionID, commandID, actorID, resumedAt)
	case draftusecase.ExecutionStateRecoveryRequired, draftusecase.ExecutionStateCompleted, draftusecase.ExecutionStateSuperseded:
		return unchangedDraftRevisionMatches(current, expected, expectedPreviousRevisionID, resultRevisionID)
	case draftusecase.ExecutionStatePaused:
		return false
	default:
		return false
	}
}

func resolvedDraftMutationMatches(
	current *draftusecase.Execution,
	expected *draftusecase.RevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	resumedAt time.Time,
) bool {
	lineageMatches := nextRevisionMatches(current.Revision, expected.Revision) &&
		current.PreviousRevisionID == expected.RevisionID && current.RevisionID == resultRevisionID
	identityMatches := current.CommandID == commandID && current.ID != resultRevisionID &&
		resultRevisionID != expectedPreviousRevisionID
	return lineageMatches && identityMatches && draftTransitionMatches(current.Transition,
		draftusecase.TransitionResume, actorID, "pause resumed", resumedAt)
}

func frozenRevisionsAdvanced(current, expected []PauseFrozenDeadlineRevision) bool {
	if len(current) != len(expected) {
		return false
	}
	values := make(map[pauseDeadlineIdentity]int64, len(expected))
	for _, value := range expected {
		values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}] = value.Revision
	}
	for _, value := range current {
		expectedRevision, exists := values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}]
		if !exists || expectedRevision == math.MaxInt64 || value.Revision != expectedRevision+1 {
			return false
		}
	}
	return true
}

func reconcilePauseResume(record PauseResumeRecord, command PauseResumeCommand) (*PauseResumeRecord, error) {
	if validatePauseResumeRecord(record) != nil || record.Scope != command.Scope || record.PauseID != command.PauseID ||
		record.CommandID != command.CommandID || record.ActorID != command.ActorID ||
		record.DraftResultRevisionID != command.DraftResultRevisionID ||
		!pauseResumeExpectationEqual(record.Expected, command.Expected) {
		return nil, ErrPauseResumeCommandReuse
	}
	clone := clonePauseResumeRecord(record)
	return &clone, nil
}

func PauseResumeExpectationFrom(authority PauseResumeAuthority) PauseResumeExpectation {
	expected := PauseResumeExpectation{
		PauseID: authority.Pause.PauseID, GraphRevision: authority.Pause.Graph.Revision,
		PauseRevision: authority.Pause.Revision, Authority: authority.Pause.Scope.Authority,
		TournamentRevision: authority.Pause.Graph.Tournament.Revision, WaveRevision: authority.Pause.Graph.Wave.Revision,
		TerminalActionRevision: authority.TerminalActionRevision,
		Series:                 make([]PauseChildRevision, len(authority.Pause.Graph.Series)),
		Games:                  make([]PauseChildRevision, len(authority.Pause.Graph.Games)),
		Presence:               make([]PausePresenceRevision, len(authority.Presence)),
		Reconnect:              make([]PauseChildRevision, len(authority.Reconnect)),
		Counters:               make([]PauseReconnectCounterRevision, len(authority.Counters)),
		FrozenDeadlines:        make([]PauseFrozenDeadlineRevision, len(authority.FrozenDeadlines)),
	}
	for index, series := range authority.Pause.Graph.Series {
		expected.Series[index] = PauseChildRevision{ID: series.Execution.Series.ID, Revision: series.Revision}
	}
	for index, game := range authority.Pause.Graph.Games {
		expected.Games[index] = PauseChildRevision{ID: game.Game.ID, Revision: game.Revision}
	}
	for index, presence := range authority.Presence {
		expected.Presence[index] = PausePresenceRevision{ID: presence.ID, TournamentID: presence.TournamentID, RosterID: presence.RosterID, SeriesID: presence.SeriesID, ParticipantID: presence.ParticipantID, PresenceEpoch: presence.PresenceEpoch, Revision: presence.Revision}
	}
	for index, interval := range authority.Reconnect {
		expected.Reconnect[index] = PauseChildRevision{ID: interval.ID, Revision: interval.Revision}
	}
	for index, counter := range authority.Counters {
		expected.Counters[index] = PauseReconnectCounterRevision{PauseID: counter.PauseID, RosterID: counter.RosterID, ParticipantID: counter.ParticipantID, Revision: counter.Revision}
	}
	for index, frozen := range authority.FrozenDeadlines {
		expected.FrozenDeadlines[index] = PauseFrozenDeadlineRevision{Kind: frozen.Kind, OwnerID: frozen.OwnerID, Revision: frozen.Revision}
	}
	if authority.Pause.Graph.Draft != nil {
		draft := draftusecase.Expectation(*authority.Pause.Graph.Draft)
		expected.Draft = &draft
		expected.DraftPreviousRevisionID = authority.Pause.Graph.Draft.PreviousRevisionID
	}
	if authority.Pause.Graph.Tournament.PausedFromState != nil {
		expected.TournamentState = *authority.Pause.Graph.Tournament.PausedFromState
	}
	return expected
}

func validatePauseResumeExpectation(value PauseResumeExpectation) error {
	if !validPauseResumeRootExpectation(value) || !validPauseResumeChildExpectation(value) {
		return pauseResumeError("invalid expected revision")
	}
	if !validPauseDraftRevisionContract(value.Draft, value.DraftPreviousRevisionID) {
		return pauseResumeError("invalid expected Draft")
	}
	if err := validatePausePresenceRevisions(value.Presence); err != nil {
		return pauseResumeError("invalid expected Presence: %v", err)
	}
	return nil
}

func validPauseResumeRootExpectation(value PauseResumeExpectation) bool {
	return value.PauseID != uuid.Nil && value.GraphRevision >= 1 && value.PauseRevision >= 1 &&
		value.Authority.Validate() == nil && normalPauseTournamentState(value.TournamentState) &&
		value.TournamentRevision >= 1 && value.WaveRevision >= 1 &&
		value.TerminalActionRevision >= 0
}

func validPauseResumeChildExpectation(value PauseResumeExpectation) bool {
	return validUniqueChildRevisions(value.Series) && validUniqueChildRevisions(value.Games) &&
		validUniqueChildRevisions(value.Reconnect) && validCounterRevisions(value.Counters) &&
		validFrozenDeadlineRevisions(value.FrozenDeadlines)
}

func pauseResumeExpectationEqual(first, second PauseResumeExpectation) bool {
	return pauseResumeRootExpectationEqual(first, second) && pauseResumeChildExpectationEqual(first, second)
}

func pauseResumeRootExpectationEqual(first, second PauseResumeExpectation) bool {
	return first.PauseID == second.PauseID && first.GraphRevision == second.GraphRevision &&
		first.PauseRevision == second.PauseRevision && first.Authority == second.Authority &&
		first.TournamentState == second.TournamentState && first.TournamentRevision == second.TournamentRevision &&
		first.WaveRevision == second.WaveRevision && first.DraftPreviousRevisionID == second.DraftPreviousRevisionID &&
		first.TerminalActionRevision == second.TerminalActionRevision && reflect.DeepEqual(first.Draft, second.Draft)
}

func pauseResumeChildExpectationEqual(first, second PauseResumeExpectation) bool {
	return revisionMapEqual(first.Series, second.Series) && revisionMapEqual(first.Games, second.Games) &&
		revisionMapEqual(first.Reconnect, second.Reconnect) && presenceRevisionMapEqual(first.Presence, second.Presence) &&
		counterRevisionMapEqual(first.Counters, second.Counters) && frozenRevisionMapEqual(first.FrozenDeadlines, second.FrozenDeadlines)
}

func clonePauseResumeExpectation(value PauseResumeExpectation) PauseResumeExpectation {
	clone := value
	clone.Games = append([]PauseChildRevision(nil), value.Games...)
	clone.Series = append([]PauseChildRevision(nil), value.Series...)
	clone.Presence = append([]PausePresenceRevision(nil), value.Presence...)
	clone.Reconnect = append([]PauseChildRevision(nil), value.Reconnect...)
	clone.Counters = append([]PauseReconnectCounterRevision(nil), value.Counters...)
	clone.FrozenDeadlines = append([]PauseFrozenDeadlineRevision(nil), value.FrozenDeadlines...)
	if value.Draft != nil {
		draft := *value.Draft
		clone.Draft = &draft
	}
	return clone
}

func clonePauseResumeRecord(value PauseResumeRecord) PauseResumeRecord {
	clone := value
	clone.Expected = clonePauseResumeExpectation(value.Expected)
	clone.Graph = clonePauseGraph(value.Graph)
	return clone
}
