package model

import (
	"reflect"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func PauseGraphRevisionsFrom(graph PauseGraph) PauseGraphRevisions {
	revisions := PauseGraphRevisions{
		GraphRevision: graph.Revision, TournamentState: graph.Tournament.State, TournamentRevision: graph.Tournament.Revision,
		WaveRevision: graph.Wave.Revision, TerminalActionRevision: graph.TerminalActionRevision,
		Series: make([]PauseChildRevision, len(graph.Series)), Games: make([]PauseChildRevision, len(graph.Games)),
		Presence: make([]PausePresenceRevision, len(graph.Presence)), Reconnect: make([]PauseChildRevision, len(graph.Reconnect)),
		Counters: make([]PauseReconnectCounterRevision, len(graph.Counters)), FrozenDeadlines: make([]PauseFrozenDeadlineRevision, len(graph.FrozenDeadlines)),
	}
	for index := range graph.Series {
		revisions.Series[index] = PauseChildRevision{ID: graph.Series[index].Execution.Series.ID, Revision: graph.Series[index].Revision}
	}
	for index := range graph.Games {
		revisions.Games[index] = PauseChildRevision{ID: graph.Games[index].Game.ID, Revision: graph.Games[index].Revision}
	}
	if graph.Draft != nil {
		expected := draftusecase.Expectation(*graph.Draft)
		revisions.Draft = &expected
		revisions.DraftPreviousRevisionID = graph.Draft.PreviousRevisionID
	}
	for index := range graph.Presence {
		presence := graph.Presence[index]
		revisions.Presence[index] = PausePresenceRevision{ID: presence.ID, TournamentID: presence.TournamentID, RosterID: presence.RosterID, SeriesID: presence.SeriesID, ParticipantID: presence.ParticipantID, PresenceEpoch: presence.PresenceEpoch, Revision: presence.Revision}
	}
	for index := range graph.Reconnect {
		revisions.Reconnect[index] = PauseChildRevision{ID: graph.Reconnect[index].ID, Revision: graph.Reconnect[index].Revision}
	}
	for index, counter := range graph.Counters {
		revisions.Counters[index] = PauseReconnectCounterRevision{PauseID: counter.PauseID, RosterID: counter.RosterID, ParticipantID: counter.ParticipantID, Revision: counter.Revision}
	}
	for index, frozen := range graph.FrozenDeadlines {
		revisions.FrozenDeadlines[index] = PauseFrozenDeadlineRevision{Kind: frozen.Kind, OwnerID: frozen.OwnerID, Revision: frozen.Revision}
	}
	return revisions
}

func (kind PauseDeadlineKind) isValid() bool {
	return kind == PauseDeadlineReadyWindow || kind == PauseDeadlineGame || kind == PauseDeadlineDraft
}

func validatePauseGraphRevisions(value PauseGraphRevisions) error {
	if !validPauseRootRevisions(value) {
		return normalPauseError("invalid root revisions")
	}
	if !validUniqueChildRevisions(value.Series) || !validUniqueChildRevisions(value.Games) || !validUniqueChildRevisions(value.Reconnect) {
		return normalPauseError("invalid or duplicate child revision")
	}
	if err := validatePausePresenceRevisions(value.Presence); err != nil {
		return err
	}
	if !validPauseDraftRevisionContract(value.Draft, value.DraftPreviousRevisionID) {
		return normalPauseError("invalid Draft revision")
	}
	if !validCounterRevisions(value.Counters) || !validFrozenDeadlineRevisions(value.FrozenDeadlines) || value.TerminalActionRevision < 0 {
		return normalPauseError("invalid aggregate revision")
	}
	return nil
}

func validPauseRootRevisions(value PauseGraphRevisions) bool {
	return value.GraphRevision >= 1 && normalPauseTournamentState(value.TournamentState) &&
		value.TournamentRevision >= 1 && value.WaveRevision >= 1 && len(value.Series) > 0
}

func normalPauseTournamentState(value domain.TournamentState) bool {
	return value == domain.TournamentStateSwiss || value == domain.TournamentStatePlayoffs
}

func validatePausePresenceRevisions(values []PausePresenceRevision) error {
	seenPresence := make(map[uuid.UUID]struct{}, len(values))
	for _, presence := range values {
		if presence.ID == uuid.Nil || presence.TournamentID == uuid.Nil || presence.RosterID == uuid.Nil ||
			presence.SeriesID == uuid.Nil || presence.ParticipantID == uuid.Nil || presence.PresenceEpoch < 1 || presence.Revision < 1 {
			return normalPauseError("invalid Presence revision")
		}
		if _, duplicate := seenPresence[presence.ParticipantID]; duplicate {
			return normalPauseError("duplicate Presence revision")
		}
		seenPresence[presence.ParticipantID] = struct{}{}
	}
	return nil
}

func validPauseDraftRevision(value draftusecase.RevisionExpectation) bool {
	return value.RevisionID != uuid.Nil && value.Revision >= 1 && value.ServiceEpoch != uuid.Nil
}

func validPauseDraftRevisionContract(expected *draftusecase.RevisionExpectation, previousRevisionID uuid.UUID) bool {
	if expected == nil {
		return previousRevisionID == uuid.Nil
	}
	return validPauseDraftRevision(*expected) && validDraftPreviousRevision(*expected, previousRevisionID)
}

func validDraftPreviousRevision(expected draftusecase.RevisionExpectation, previousRevisionID uuid.UUID) bool {
	if expected.Revision == 1 {
		return previousRevisionID == uuid.Nil
	}
	return previousRevisionID != uuid.Nil && previousRevisionID != expected.RevisionID &&
		previousRevisionID != expected.ServiceEpoch
}

func validUniqueChildRevisions(values []PauseChildRevision) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value.ID == uuid.Nil || value.Revision < 1 {
			return false
		}
		if _, duplicate := seen[value.ID]; duplicate {
			return false
		}
		seen[value.ID] = struct{}{}
	}
	return true
}

func validCounterRevisions(values []PauseReconnectCounterRevision) bool {
	seen := make(map[[3]uuid.UUID]struct{}, len(values))
	for _, value := range values {
		key := [3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}
		if value.PauseID == uuid.Nil || value.RosterID == uuid.Nil || value.ParticipantID == uuid.Nil || value.Revision < 1 {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validFrozenDeadlineRevisions(values []PauseFrozenDeadlineRevision) bool {
	seen := make(map[pauseDeadlineIdentity]struct{}, len(values))
	for _, value := range values {
		key := pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}
		if !value.Kind.isValid() || value.OwnerID == uuid.Nil || value.Revision < 1 {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func pauseGraphRevisionsEqual(first, second PauseGraphRevisions) bool {
	return revisionMapEqual(first.Series, second.Series) && revisionMapEqual(first.Games, second.Games) &&
		revisionMapEqual(first.Reconnect, second.Reconnect) && presenceRevisionMapEqual(first.Presence, second.Presence) &&
		counterRevisionMapEqual(first.Counters, second.Counters) && frozenRevisionMapEqual(first.FrozenDeadlines, second.FrozenDeadlines) &&
		first.GraphRevision == second.GraphRevision && first.TournamentRevision == second.TournamentRevision &&
		first.TournamentState == second.TournamentState && first.WaveRevision == second.WaveRevision &&
		first.DraftPreviousRevisionID == second.DraftPreviousRevisionID &&
		first.TerminalActionRevision == second.TerminalActionRevision && reflect.DeepEqual(first.Draft, second.Draft)
}

func counterRevisionMapEqual(first, second []PauseReconnectCounterRevision) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[[3]uuid.UUID]int64, len(first))
	for _, value := range first {
		values[[3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}] = value.Revision
	}
	for _, value := range second {
		if values[[3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}] != value.Revision {
			return false
		}
	}
	return true
}

func frozenRevisionMapEqual(first, second []PauseFrozenDeadlineRevision) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[pauseDeadlineIdentity]int64, len(first))
	for _, value := range first {
		values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}] = value.Revision
	}
	for _, value := range second {
		if values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}] != value.Revision {
			return false
		}
	}
	return true
}

func revisionMapEqual(first, second []PauseChildRevision) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[uuid.UUID]int64, len(first))
	for _, value := range first {
		values[value.ID] = value.Revision
	}
	for _, value := range second {
		if values[value.ID] != value.Revision {
			return false
		}
	}
	return true
}

func presenceRevisionMapEqual(first, second []PausePresenceRevision) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[uuid.UUID]PausePresenceRevision, len(first))
	for _, value := range first {
		values[value.ParticipantID] = value
	}
	for _, value := range second {
		if values[value.ParticipantID] != value {
			return false
		}
	}
	return true
}
