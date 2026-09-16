package pause

import (
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

func pausedGraphMatchesExpected(
	graph PauseGraph,
	expected PauseGraphRevisions,
	draftResultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	reason PauseReason,
	pausedAt time.Time,
	pauseID uuid.UUID,
	suspended []PauseChildRevision,
) bool {
	if !pausedRootRevisionsMatch(graph, expected) {
		return false
	}
	current := PauseGraphRevisionsFrom(graph)
	if !pausedChildRevisionsMatch(graph, current, expected, draftResultRevisionID, commandID, actorID, reason, pausedAt, pauseID, suspended) {
		return false
	}
	for _, frozen := range graph.FrozenDeadlines {
		if frozen.Revision != 1 {
			return false
		}
	}
	return true
}

func pausedRootRevisionsMatch(graph PauseGraph, expected PauseGraphRevisions) bool {
	if validatePauseGraphRevisions(expected) != nil || expected.GraphRevision == math.MaxInt64 ||
		expected.TournamentRevision == math.MaxInt64 || graph.Revision != expected.GraphRevision+1 ||
		graph.Tournament.Revision != expected.TournamentRevision+1 || graph.PausedAt == nil ||
		graph.Tournament.State != domain.TournamentStateTechnicalPause || graph.Tournament.PausedFromState == nil ||
		!graph.Tournament.UpdatedAt.Equal(*graph.PausedAt) {
		return false
	}
	if *graph.Tournament.PausedFromState != expected.TournamentState {
		return false
	}
	return pausedWaveRevisionMatches(graph.Wave, expected.WaveRevision)
}

func pausedChildRevisionsMatch(
	graph PauseGraph,
	current PauseGraphRevisions,
	expected PauseGraphRevisions,
	draftResultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	reason PauseReason,
	pausedAt time.Time,
	pauseID uuid.UUID,
	suspended []PauseChildRevision,
) bool {
	return pausedSeriesRevisionsMatch(graph.Series, expected.Series) && pausedGameRevisionsMatch(graph.Games, expected.Games) &&
		presenceRevisionMapEqual(current.Presence, expected.Presence) && pausedReconnectRevisionsMatch(graph.Reconnect, expected.Reconnect, suspended, pauseID, pausedAt) &&
		counterRevisionMapEqual(current.Counters, expected.Counters) && current.TerminalActionRevision == expected.TerminalActionRevision &&
		pausedDraftRevisionMatches(graph.Draft, expected.Draft, expected.DraftPreviousRevisionID,
			draftResultRevisionID, commandID, actorID, reason, pausedAt) && len(expected.FrozenDeadlines) == 0
}

func pausedWaveRevisionMatches(current PauseWave, expected int64) bool {
	switch current.Wave.State {
	case domain.WaveStatePaused, domain.WaveStateReadyWindowOpen, domain.WaveStateReady:
		return nextRevisionMatches(current.Revision, expected)
	case domain.WaveStatePlanned, domain.WaveStateCompleted,
		domain.WaveStateReadyWindowExpired, domain.WaveStateSuperseded:
		return current.Revision == expected
	case domain.WaveStateActive:
		return false
	default:
		return false
	}
}

func pausedSeriesRevisionsMatch(current []PauseSeries, expected []PauseChildRevision) bool {
	if len(current) != len(expected) {
		return false
	}
	for _, series := range current {
		revision, ok := childRevision(expected, series.Execution.Series.ID)
		if !ok || !pausedSeriesRevisionMatches(series, revision) {
			return false
		}
	}
	return true
}

func pausedSeriesRevisionMatches(current PauseSeries, expected int64) bool {
	switch current.Execution.Series.State {
	case domain.SeriesStateTechnicalPause:
		return nextRevisionMatches(current.Revision, expected)
	case domain.SeriesStatePlanned, domain.SeriesStateLocked,
		domain.SeriesStateCompleted, domain.SeriesStateCancelled:
		return current.Revision == expected
	case domain.SeriesStateDraft, domain.SeriesStateReady,
		domain.SeriesStateActive, domain.SeriesStateReplayRequired:
		return false
	default:
		return false
	}
}

func pausedGameRevisionsMatch(current []PauseGame, expected []PauseChildRevision) bool {
	if len(current) != len(expected) {
		return false
	}
	for _, game := range current {
		revision, ok := childRevision(expected, game.Game.ID)
		if !ok || !pausedGameRevisionMatches(game, revision) {
			return false
		}
	}
	return true
}

func pausedGameRevisionMatches(current PauseGame, expected int64) bool {
	switch current.Game.State {
	case domain.GameStatePaused:
		return nextRevisionMatches(current.Revision, expected)
	case domain.GameStatePlanned, domain.GameStateReady, domain.GameStateCompleted,
		domain.GameStateVoid, domain.GameStateCancelled, domain.GameStateSuperseded:
		return current.Revision == expected
	case domain.GameStateActive:
		return false
	default:
		return false
	}
}

func pausedReconnectRevisionsMatch(
	current []pausedomain.PauseReconnectInterval,
	expected []PauseChildRevision,
	suspended []PauseChildRevision,
	pauseID uuid.UUID,
	pausedAt time.Time,
) bool {
	if len(current) != len(expected) {
		return false
	}
	expectedByID, ok := pauseChildRevisionMap(expected)
	if !ok {
		return false
	}
	suspendedByID, ok := suspendedReconnectRevisionMap(suspended, expectedByID)
	if !ok {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(current))
	for _, interval := range current {
		revision, exists := expectedByID[interval.ID]
		if !exists || pauseUUIDSeen(seen, interval.ID) {
			return false
		}
		seen[interval.ID] = struct{}{}
		if sourceRevision, wasSuspended := suspendedByID[interval.ID]; wasSuspended {
			if !pausedSuspendedReconnectMatches(interval, sourceRevision, pauseID, pausedAt) {
				return false
			}
		} else if !pausedUnchangedReconnectMatches(interval, revision, pauseID) {
			return false
		}
	}
	return len(seen) == len(expectedByID)
}

func pauseChildRevisionMap(values []PauseChildRevision) (map[uuid.UUID]int64, bool) {
	result := make(map[uuid.UUID]int64, len(values))
	for _, value := range values {
		if value.ID == uuid.Nil || pauseUUIDSeenRevision(result, value.ID) {
			return nil, false
		}
		result[value.ID] = value.Revision
	}
	return result, true
}

func suspendedReconnectRevisionMap(values []PauseChildRevision, expected map[uuid.UUID]int64) (map[uuid.UUID]int64, bool) {
	result := make(map[uuid.UUID]int64, len(values))
	for _, value := range values {
		revision, exists := expected[value.ID]
		if !exists || revision != value.Revision || pauseUUIDSeenRevision(result, value.ID) {
			return nil, false
		}
		result[value.ID] = value.Revision
	}
	return result, true
}

func pauseUUIDSeen(values map[uuid.UUID]struct{}, id uuid.UUID) bool {
	_, exists := values[id]
	return exists
}

func pauseUUIDSeenRevision(values map[uuid.UUID]int64, id uuid.UUID) bool {
	_, exists := values[id]
	return exists
}

func pausedSuspendedReconnectMatches(interval pausedomain.PauseReconnectInterval, sourceRevision int64, pauseID uuid.UUID, pausedAt time.Time) bool {
	return sourceRevision < math.MaxInt64 && interval.Revision == sourceRevision+1 &&
		interval.State == pausedomain.ReconnectStateCancelled && interval.ClosedAt != nil && interval.ClosedAt.Equal(pausedAt) &&
		interval.OpenedAt.Before(pausedAt) && interval.Deadline.After(pausedAt) && interval.UpdatedAt.Equal(pausedAt) &&
		interval.SuspendedByPauseID != nil && *interval.SuspendedByPauseID == pauseID
}

func pausedUnchangedReconnectMatches(interval pausedomain.PauseReconnectInterval, revision int64, pauseID uuid.UUID) bool {
	return interval.Revision == revision &&
		(interval.SuspendedByPauseID == nil || *interval.SuspendedByPauseID != pauseID)
}
