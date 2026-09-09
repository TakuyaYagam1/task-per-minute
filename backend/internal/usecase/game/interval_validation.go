package game

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

type reconnectCycle struct {
	participantID uuid.UUID
	number        int
	continuation  int
}

type reconnectRootEpoch struct {
	participantID uuid.UUID
	presenceEpoch int64
}

type reconnectIntervalValidation struct {
	seenIDs          map[uuid.UUID]struct{}
	seenSegments     map[reconnectCycle]struct{}
	openParticipants map[uuid.UUID]struct{}
	rootCounts       map[uuid.UUID]int
	rootEpochs       map[reconnectRootEpoch]struct{}
}

func newReconnectIntervalValidation(size int) reconnectIntervalValidation {
	return reconnectIntervalValidation{seenIDs: make(map[uuid.UUID]struct{}, size), seenSegments: make(map[reconnectCycle]struct{}, size),
		openParticipants: make(map[uuid.UUID]struct{}, 2), rootCounts: make(map[uuid.UUID]int, 2),
		rootEpochs: make(map[reconnectRootEpoch]struct{}, size)}
}

func validateReconnectIntervalSet(authority ReconnectAuthority, stableGames map[uuid.UUID]struct{}, counters map[uuid.UUID]pause.PauseReconnectCounter) error {
	validation := newReconnectIntervalValidation(len(authority.Reconnect))
	for _, interval := range authority.Reconnect {
		if err := validateReconnectInterval(authority, interval, stableGames, counters, &validation); err != nil {
			return err
		}
	}
	for participantID, counter := range counters {
		if validation.rootCounts[participantID] != counter.Used {
			return reconnectError("reconnect roots do not match stable counter")
		}
	}
	return nil
}

func validateReconnectInterval(authority ReconnectAuthority, interval pause.PauseReconnectInterval, stableGames map[uuid.UUID]struct{}, counters map[uuid.UUID]pause.PauseReconnectCounter, validation *reconnectIntervalValidation) error {
	if err := validateReconnectIntervalBinding(authority, interval, stableGames, counters); err != nil {
		return err
	}
	if err := validation.rememberIdentity(interval); err != nil {
		return err
	}
	if err := validation.rememberRoot(interval); err != nil {
		return err
	}
	return validation.rememberOpen(authority, interval)
}

func validateReconnectIntervalBinding(authority ReconnectAuthority, interval pause.PauseReconnectInterval, stableGames map[uuid.UUID]struct{}, counters map[uuid.UUID]pause.PauseReconnectCounter) error {
	if interval.Validate() != nil || interval.PauseID != authority.PauseID || interval.RosterID != authority.Scope.RosterID ||
		interval.SeriesID != authority.Series.ID || !reconnectSeriesHasParticipant(authority.Series, interval.ParticipantID) {
		return reconnectError("invalid reconnect interval")
	}
	if _, belongs := stableGames[interval.GameID]; !belongs {
		return reconnectError("reconnect interval belongs to a foreign Game slot")
	}
	counter, counterFound := counters[interval.ParticipantID]
	presence := reconnectPresenceByParticipant(authority.Presence, interval.ParticipantID)
	if !counterFound || presence == nil || interval.Number > counter.Used || interval.PresenceEpoch > presence.PresenceEpoch {
		return reconnectError("reconnect interval exceeds stable counter")
	}
	if interval.PresenceEpoch == presence.PresenceEpoch && reconnectIntervalSelectable(interval) && interval.GameID != authority.Game.ID {
		return reconnectError("current reconnect interval belongs to predecessor Game")
	}
	return nil
}

func reconnectIntervalSelectable(interval pause.PauseReconnectInterval) bool {
	return interval.State == pause.ReconnectStateOpen || interval.State == pause.ReconnectStateExpired
}

func (validation *reconnectIntervalValidation) rememberIdentity(interval pause.PauseReconnectInterval) error {
	if _, exists := validation.seenIDs[interval.ID]; exists {
		return reconnectError("duplicate reconnect interval")
	}
	validation.seenIDs[interval.ID] = struct{}{}
	segment := reconnectCycle{participantID: interval.ParticipantID, number: interval.Number, continuation: interval.ContinuationNumber}
	if _, exists := validation.seenSegments[segment]; exists {
		return reconnectError("duplicate reconnect cycle segment")
	}
	validation.seenSegments[segment] = struct{}{}
	return nil
}

func (validation *reconnectIntervalValidation) rememberRoot(interval pause.PauseReconnectInterval) error {
	if interval.ContinuationNumber != 0 {
		return nil
	}
	epoch := reconnectRootEpoch{participantID: interval.ParticipantID, presenceEpoch: interval.PresenceEpoch}
	if _, exists := validation.rootEpochs[epoch]; exists {
		return reconnectError("duplicate reconnect root Presence epoch")
	}
	validation.rootEpochs[epoch] = struct{}{}
	validation.rootCounts[interval.ParticipantID]++
	return nil
}

func (validation *reconnectIntervalValidation) rememberOpen(authority ReconnectAuthority, interval pause.PauseReconnectInterval) error {
	if interval.State != pause.ReconnectStateOpen {
		return nil
	}
	if _, exists := validation.openParticipants[interval.ParticipantID]; exists {
		return reconnectError("multiple open intervals for participant")
	}
	validation.openParticipants[interval.ParticipantID] = struct{}{}
	presence := reconnectPresenceByParticipant(authority.Presence, interval.ParticipantID)
	if interval.GameID != authority.Game.ID || presence == nil || presence.State != pause.PresenceStateDisconnected || interval.PresenceEpoch != presence.PresenceEpoch {
		return reconnectError("open reconnect interval does not match current Presence")
	}
	return nil
}

func validateReconnectIntervalLineage(intervals []pause.PauseReconnectInterval) error {
	if pause.ValidateReconnectLineage(intervals) != nil {
		return reconnectError("invalid reconnect lineage")
	}
	return nil
}
