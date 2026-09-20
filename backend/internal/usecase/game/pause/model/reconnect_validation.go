package model

import (
	"github.com/google/uuid"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

func validatePausePresenceSet(graph PauseGraph, index pauseGraphIndex) error {
	seen := make(map[uuid.UUID]struct{}, len(graph.Presence))
	seenIDs := make(map[uuid.UUID]struct{}, len(graph.Presence))
	for _, presence := range graph.Presence {
		if validatePausePresence(presence) != nil || presence.TournamentID != graph.Scope.TournamentID || presence.RosterID != graph.Scope.RosterID {
			return normalPauseError("invalid Presence descendant")
		}
		seriesID, exists := index.participantSeries[presence.ParticipantID]
		if !exists || presence.SeriesID != seriesID {
			return ErrNormalPauseGraphIncomplete
		}
		if _, duplicate := seen[presence.ParticipantID]; duplicate {
			return normalPauseError("duplicate Presence descendant")
		}
		if _, duplicate := seenIDs[presence.ID]; duplicate {
			return normalPauseError("duplicate Presence row")
		}
		seen[presence.ParticipantID] = struct{}{}
		seenIDs[presence.ID] = struct{}{}
		index.presenceByParticipant[presence.ParticipantID] = presence
	}
	if len(seen) != len(index.participantSeries) {
		return ErrNormalPauseGraphIncomplete
	}
	return nil
}

type reconnectCounterIdentity struct {
	PauseID       uuid.UUID
	RosterID      uuid.UUID
	ParticipantID uuid.UUID
}

func validateReconnectSet(graph PauseGraph, index pauseGraphIndex) error {
	counters, err := validateReconnectCounters(graph, index)
	if err != nil {
		return err
	}
	roots, err := validateReconnectIntervals(graph, index, counters)
	if err != nil {
		return err
	}
	for key, counter := range counters {
		root := roots[key]
		if counter.Used == 0 {
			if root.count != 0 {
				return ErrNormalPauseGraphIncomplete
			}
			continue
		}
		if root.count == 0 || root.last != counter.Used || root.first != counter.Used-root.count+1 {
			return ErrNormalPauseGraphIncomplete
		}
	}
	return nil
}

func validateReconnectCounters(graph PauseGraph, index pauseGraphIndex) (map[reconnectCounterIdentity]pausedomain.PauseReconnectCounter, error) {
	counters := make(map[reconnectCounterIdentity]pausedomain.PauseReconnectCounter, len(graph.Counters))
	for _, counter := range graph.Counters {
		key := reconnectCounterIdentity{PauseID: counter.PauseID, RosterID: counter.RosterID, ParticipantID: counter.ParticipantID}
		if !validPauseReconnectCounter(counter) || counter.RosterID != graph.Scope.RosterID {
			return nil, normalPauseError("invalid reconnect counter")
		}
		if _, exists := index.participantSeries[counter.ParticipantID]; !exists {
			return nil, ErrNormalPauseGraphIncomplete
		}
		if _, duplicate := counters[key]; duplicate {
			return nil, normalPauseError("duplicate reconnect counter")
		}
		counters[key] = counter
	}
	return counters, nil
}

type reconnectLogicalSegment struct {
	number       int
	continuation int
}

type reconnectRootRange struct {
	count int
	first int
	last  int
}

func validateReconnectIntervals(graph PauseGraph, index pauseGraphIndex, counters map[reconnectCounterIdentity]pausedomain.PauseReconnectCounter) (map[reconnectCounterIdentity]reconnectRootRange, error) {
	roots := make(map[reconnectCounterIdentity]reconnectRootRange, len(counters))
	byID := make(map[uuid.UUID]pausedomain.PauseReconnectInterval, len(graph.Reconnect))
	seenSegments := make(map[reconnectCounterIdentity]map[reconnectLogicalSegment]struct{}, len(counters))
	for _, interval := range graph.Reconnect {
		key, err := validateReconnectIntervalMembership(graph, index, counters, interval)
		if err != nil {
			return nil, err
		}
		if err := recordReconnectInterval(byID, seenSegments, key, interval); err != nil {
			return nil, err
		}
		if interval.ContinuationNumber == 0 {
			root := roots[key]
			root.count++
			if root.count == 1 || interval.Number < root.first {
				root.first = interval.Number
			}
			if interval.Number > root.last {
				root.last = interval.Number
			}
			roots[key] = root
		}
	}
	if err := pausedomain.ValidateReconnectLineage(graph.Reconnect); err != nil {
		return nil, normalPauseError("invalid Reconnect continuation lineage")
	}
	return roots, nil
}

func validateReconnectIntervalMembership(
	graph PauseGraph,
	index pauseGraphIndex,
	counters map[reconnectCounterIdentity]pausedomain.PauseReconnectCounter,
	interval pausedomain.PauseReconnectInterval,
) (reconnectCounterIdentity, error) {
	key := reconnectCounterIdentity{PauseID: interval.PauseID, RosterID: interval.RosterID, ParticipantID: interval.ParticipantID}
	if validatePauseReconnect(interval) != nil {
		return key, normalPauseError("invalid Reconnect descendant")
	}
	presence, participantExists := index.presenceByParticipant[interval.ParticipantID]
	game, gameExists := index.gamesByID[interval.GameID]
	counter, counterExists := counters[key]
	if interval.RosterID != graph.Scope.RosterID || !participantExists || !gameExists || !counterExists ||
		presence.SeriesID != interval.SeriesID || presence.PresenceEpoch < interval.PresenceEpoch ||
		game.SeriesID != interval.SeriesID || interval.Number > counter.Used {
		return key, ErrNormalPauseGraphIncomplete
	}
	return key, nil
}

func recordReconnectInterval(
	byID map[uuid.UUID]pausedomain.PauseReconnectInterval,
	seen map[reconnectCounterIdentity]map[reconnectLogicalSegment]struct{},
	key reconnectCounterIdentity,
	interval pausedomain.PauseReconnectInterval,
) error {
	if _, duplicate := byID[interval.ID]; duplicate {
		return normalPauseError("duplicate Reconnect descendant")
	}
	byID[interval.ID] = interval
	if seen[key] == nil {
		seen[key] = make(map[reconnectLogicalSegment]struct{})
	}
	segment := reconnectLogicalSegment{number: interval.Number, continuation: interval.ContinuationNumber}
	if _, duplicate := seen[key][segment]; duplicate {
		return normalPauseError("duplicate Reconnect segment")
	}
	seen[key][segment] = struct{}{}
	return nil
}

func validPauseReconnectCounter(counter pausedomain.PauseReconnectCounter) bool {
	return counter.Validate() == nil
}

func validatePausePresence(value pausedomain.PausePresence) error {
	if value.Validate() != nil {
		return normalPauseError("invalid Presence")
	}
	return nil
}

func validatePauseReconnect(value pausedomain.PauseReconnectInterval) error {
	if value.Validate() != nil {
		return normalPauseError("invalid Reconnect")
	}
	return nil
}
