package game

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func validateReconnectAuthority(authority ReconnectAuthority) error {
	if err := validateReconnectAuthorityHeader(authority); err != nil {
		return err
	}
	stableSlotGames, err := reconnectStableSlotGames(authority)
	if err != nil {
		return err
	}
	if err := validateReconnectPresenceSet(authority); err != nil {
		return err
	}
	counters, err := validateReconnectCounterSet(authority)
	if err != nil {
		return err
	}
	if err := validateReconnectIntervalSet(authority, stableSlotGames, counters); err != nil {
		return err
	}
	if err := validateReconnectIntervalLineage(authority.Reconnect); err != nil {
		return err
	}
	return validateReconnectCurrentOutcome(authority)
}

func validateReconnectAuthorityHeader(authority ReconnectAuthority) error {
	if !validReconnectAuthorityIdentity(authority) {
		return reconnectError("incomplete authority")
	}
	if !validReconnectAuthorityTopology(authority) {
		return reconnectError("invalid Series topology")
	}
	return nil
}

func validReconnectAuthorityIdentity(authority ReconnectAuthority) bool {
	return authority.Scope.Validate() == nil && authority.Revision >= 1 && authority.PauseID != uuid.Nil &&
		authority.GameRevision >= 1 && authority.SeriesRevision >= 1 && authority.Game.ID != uuid.Nil &&
		authority.Series.ID != uuid.Nil && authority.GameClock.GameID == authority.Game.ID &&
		authority.GameClock.PauseID == authority.PauseID && len(authority.Presence) == 2 && len(authority.Counters) == 2 &&
		authority.CurrentOrdinal >= 0 && authority.CurrentProjectionRevision >= 1 &&
		seriesdomain.ValidateGameResultRevisionIDs(authority.CurrentGameResultRevisionIDs) == nil
}

func validReconnectAuthorityTopology(authority ReconnectAuthority) bool {
	return authority.Game.Validate() == nil && authority.Series.Validate() == nil &&
		authority.Series.TournamentID == authority.Scope.TournamentID && authority.Series.ID == reconnectSeriesID(authority) &&
		authority.Series.FirstParticipantID != authority.Series.SecondParticipantID && reconnectAuthorityGameMirrored(authority) &&
		validReconnectGameClock(authority)
}

func reconnectStableSlotGames(authority ReconnectAuthority) (map[uuid.UUID]struct{}, error) {
	currentSlot, ok := reconnectCurrentGameSlot(authority.Series, authority.Game.ID)
	if !ok {
		return nil, reconnectError("current Game is not the latest stable slot attempt")
	}
	games := make(map[uuid.UUID]struct{}, len(currentSlot.Attempts))
	for _, game := range currentSlot.Attempts {
		games[game.ID] = struct{}{}
	}
	return games, nil
}

func validateReconnectPresenceSet(authority ReconnectAuthority) error {
	seen := make(map[uuid.UUID]struct{}, 2)
	for _, presence := range authority.Presence {
		if err := validateReconnectPresence(authority, presence); err != nil {
			return err
		}
		if _, exists := seen[presence.ParticipantID]; exists {
			return reconnectError("duplicate Presence")
		}
		seen[presence.ParticipantID] = struct{}{}
	}
	return nil
}

func validateReconnectPresence(authority ReconnectAuthority, presence pause.PausePresence) error {
	if presence.TournamentID != authority.Scope.TournamentID || presence.RosterID != authority.Scope.RosterID ||
		presence.SeriesID != authority.Series.ID || !reconnectSeriesHasParticipant(authority.Series, presence.ParticipantID) {
		return reconnectError("foreign Presence")
	}
	if presence.Validate() != nil {
		return reconnectError("invalid Presence")
	}
	return nil
}

func reconnectSeriesHasParticipant(series domain.Series, participantID uuid.UUID) bool {
	return participantID == series.FirstParticipantID || participantID == series.SecondParticipantID
}

func validateReconnectCounterSet(authority ReconnectAuthority) (map[uuid.UUID]pause.PauseReconnectCounter, error) {
	counters := make(map[uuid.UUID]pause.PauseReconnectCounter, 2)
	for _, counter := range authority.Counters {
		if err := validateReconnectCounter(authority, counter, counters); err != nil {
			return nil, err
		}
		counters[counter.ParticipantID] = counter
	}
	if _, ok := counters[authority.Series.FirstParticipantID]; !ok {
		return nil, reconnectError("first participant counter is missing")
	}
	if _, ok := counters[authority.Series.SecondParticipantID]; !ok {
		return nil, reconnectError("second participant counter is missing")
	}
	return counters, nil
}

func validateReconnectCounter(authority ReconnectAuthority, counter pause.PauseReconnectCounter, seen map[uuid.UUID]pause.PauseReconnectCounter) error {
	if counter.Validate() != nil || counter.PauseID != authority.PauseID || counter.RosterID != authority.Scope.RosterID ||
		counter.Limit != domain.ReconnectCycleLimit || !reconnectSeriesHasParticipant(authority.Series, counter.ParticipantID) {
		return reconnectError("invalid reconnect counter")
	}
	if _, exists := seen[counter.ParticipantID]; exists {
		return reconnectError("duplicate reconnect counter")
	}
	return nil
}
