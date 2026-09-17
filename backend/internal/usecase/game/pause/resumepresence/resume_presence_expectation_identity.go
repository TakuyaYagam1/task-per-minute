package resumepresence

import (
	"time"

	"github.com/google/uuid"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

func pauseResumePresenceExpectationEqual(first, second PauseResumePresenceExpectation) bool {
	return pauseResumeExpectationEqual(first.Resume, second.Resume) && pauseResumeDecisionExpectationEqual(first.Series, second.Series) &&
		pauseResumeDecisionExpectationEqual(first.Game, second.Game) && pauseResumePresenceSetEqual(first.Presence, second.Presence) &&
		pauseResumeReconnectSetEqual(first.Reconnect, second.Reconnect) && pauseResumeCounterSetEqual(first.Counters, second.Counters) &&
		pauseResumeFrozenBaselineEqual(first.FrozenDeadlines, second.FrozenDeadlines)
}

func clonePauseResumePresenceExpectation(value PauseResumePresenceExpectation) PauseResumePresenceExpectation {
	return PauseResumePresenceExpectation{
		Resume:          clonePauseResumeExpectationPreservingSlices(value.Resume),
		Series:          clonePauseResumeDecisionExpectation(value.Series),
		Game:            clonePauseResumeDecisionExpectation(value.Game),
		Presence:        clonePausePresenceSlice(value.Presence),
		Reconnect:       clonePauseReconnectSlice(value.Reconnect),
		Counters:        clonePauseSlice(value.Counters),
		FrozenDeadlines: clonePauseFrozenDeadlineSlice(value.FrozenDeadlines),
	}
}

func clonePauseResumeExpectationPreservingSlices(value PauseResumeExpectation) PauseResumeExpectation {
	clone := value
	clone.Games = clonePauseSlice(value.Games)
	clone.Series = clonePauseSlice(value.Series)
	clone.Presence = clonePauseSlice(value.Presence)
	clone.Reconnect = clonePauseSlice(value.Reconnect)
	clone.Counters = clonePauseSlice(value.Counters)
	clone.FrozenDeadlines = clonePauseSlice(value.FrozenDeadlines)
	if value.Draft != nil {
		draft := *value.Draft
		clone.Draft = &draft
	}
	return clone
}

func pauseResumeFrozenBaselineEqual(first, second []PauseFrozenDeadline) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[pauseDeadlineIdentity]PauseFrozenDeadline, len(first))
	for _, value := range first {
		values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}] = value
	}
	for _, value := range second {
		if !pauseResumeFrozenDeadlineEqual(values[pauseDeadlineIdentity{Kind: value.Kind, OwnerID: value.OwnerID}], value) {
			return false
		}
	}
	return true
}

func pauseResumePresenceSetEqual(first, second []pausedomain.PausePresence) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[uuid.UUID]pausedomain.PausePresence, len(first))
	for _, value := range first {
		values[value.ParticipantID] = value
	}
	for _, value := range second {
		baseline, exists := values[value.ParticipantID]
		if !exists || !pauseResumePresenceEqual(baseline, value) {
			return false
		}
	}
	return true
}

func pauseResumeReconnectSetEqual(first, second []pausedomain.PauseReconnectInterval) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[uuid.UUID]pausedomain.PauseReconnectInterval, len(first))
	for _, value := range first {
		values[value.ID] = value
	}
	for _, value := range second {
		baseline, exists := values[value.ID]
		if !exists || !pauseResumeReconnectEqual(baseline, value) {
			return false
		}
	}
	return true
}

func pauseResumeCounterSetEqual(first, second []pausedomain.PauseReconnectCounter) bool {
	if len(first) != len(second) {
		return false
	}
	values := make(map[[3]uuid.UUID]pausedomain.PauseReconnectCounter, len(first))
	for _, value := range first {
		values[[3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}] = value
	}
	for _, value := range second {
		baseline, exists := values[[3]uuid.UUID{value.PauseID, value.RosterID, value.ParticipantID}]
		if !exists || baseline != value {
			return false
		}
	}
	return true
}

func pauseResumePresenceEqual(first, second pausedomain.PausePresence) bool {
	return first.ID == second.ID && first.TournamentID == second.TournamentID && first.RosterID == second.RosterID &&
		first.SeriesID == second.SeriesID && first.ParticipantID == second.ParticipantID && first.State == second.State &&
		first.PresenceEpoch == second.PresenceEpoch && first.Revision == second.Revision && first.ConnectedAt.Equal(second.ConnectedAt) &&
		pauseResumeTimePointerEqual(first.DisconnectedAt, second.DisconnectedAt) && first.UpdatedAt.Equal(second.UpdatedAt)
}

func pauseResumeReconnectEqual(first, second pausedomain.PauseReconnectInterval) bool {
	return pauseResumeReconnectIdentityEqual(first, second) && pauseResumeReconnectLineageEqual(first, second) &&
		pauseResumeReconnectStateEqual(first, second)
}

func pauseResumeReconnectIdentityEqual(first, second pausedomain.PauseReconnectInterval) bool {
	return first.ID == second.ID && first.PauseID == second.PauseID && first.RosterID == second.RosterID &&
		first.SeriesID == second.SeriesID && first.GameID == second.GameID && first.ParticipantID == second.ParticipantID &&
		first.PresenceEpoch == second.PresenceEpoch
}

func pauseResumeReconnectLineageEqual(first, second pausedomain.PauseReconnectInterval) bool {
	return first.Number == second.Number && first.ContinuationNumber == second.ContinuationNumber &&
		pauseResumeUUIDPointerEqual(first.ContinuedFromID, second.ContinuedFromID) &&
		pauseResumeUUIDPointerEqual(first.SuspendedByPauseID, second.SuspendedByPauseID)
}

func pauseResumeReconnectStateEqual(first, second pausedomain.PauseReconnectInterval) bool {
	return first.State == second.State && first.OpenedAt.Equal(second.OpenedAt) && first.Deadline.Equal(second.Deadline) &&
		pauseResumeTimePointerEqual(first.ClosedAt, second.ClosedAt) && first.Revision == second.Revision && first.UpdatedAt.Equal(second.UpdatedAt)
}

func pauseResumeFrozenDeadlineEqual(first, second PauseFrozenDeadline) bool {
	return first.Kind == second.Kind && first.OwnerID == second.OwnerID && first.OriginalDeadline.Equal(second.OriginalDeadline) &&
		first.FrozenAt.Equal(second.FrozenAt) && first.Remaining == second.Remaining &&
		pauseResumeTimePointerEqual(first.ResumedAt, second.ResumedAt) &&
		pauseResumeTimePointerEqual(first.ResumedDeadline, second.ResumedDeadline) && first.Revision == second.Revision
}

func pauseResumeTimePointerEqual(first, second *time.Time) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.Equal(*second)
}

func pauseResumeUUIDPointerEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func pauseResumeCanonicalPresence(value pausedomain.PausePresence) pausedomain.PausePresence {
	value.ConnectedAt = value.ConnectedAt.Round(0).UTC()
	value.UpdatedAt = value.UpdatedAt.Round(0).UTC()
	if value.DisconnectedAt != nil {
		at := value.DisconnectedAt.Round(0).UTC()
		value.DisconnectedAt = &at
	}
	return value
}

func pauseResumeCanonicalReconnect(value pausedomain.PauseReconnectInterval) pausedomain.PauseReconnectInterval {
	value.OpenedAt = value.OpenedAt.Round(0).UTC()
	value.Deadline = value.Deadline.Round(0).UTC()
	value.UpdatedAt = value.UpdatedAt.Round(0).UTC()
	if value.ClosedAt != nil {
		at := value.ClosedAt.Round(0).UTC()
		value.ClosedAt = &at
	}
	return value
}

func pauseResumeCanonicalFrozenDeadline(value PauseFrozenDeadline) PauseFrozenDeadline {
	value.OriginalDeadline = value.OriginalDeadline.Round(0).UTC()
	value.FrozenAt = value.FrozenAt.Round(0).UTC()
	if value.ResumedAt != nil {
		at := value.ResumedAt.Round(0).UTC()
		value.ResumedAt = &at
	}
	if value.ResumedDeadline != nil {
		deadline := value.ResumedDeadline.Round(0).UTC()
		value.ResumedDeadline = &deadline
	}
	return value
}
