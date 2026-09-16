package pause

import (
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func pauseValidServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}

func pauseCloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func pauseTimeCoversGraphHistory(graph PauseGraph, at time.Time) bool {
	return timeCoversPauseRootHistory(at, graph) && timeCoversReadyWindowHistory(at, graph.Wave.Wave.ReadyWindow) &&
		timeCoversPresenceSetHistory(at, graph.Presence) && timeCoversReconnectSetHistory(at, graph.Reconnect) &&
		timeCoversOptionalDraftHistory(at, graph.Draft) && timeCoversFrozenDeadlineHistory(at, graph.FrozenDeadlines)
}

func timeCoversPauseRootHistory(at time.Time, graph PauseGraph) bool {
	return pausedomain.TimeAtOrBefore(at, graph.Tournament.CreatedAt) && pausedomain.TimeAtOrBefore(at, graph.Tournament.UpdatedAt) &&
		pausedomain.TimePointerAtOrBefore(at, graph.Tournament.StartedAt) && pausedomain.TimePointerAtOrBefore(at, graph.Tournament.FinishedAt) &&
		pausedomain.TimePointerAtOrBefore(at, graph.Wave.Wave.StartedAt) && pausedomain.TimePointerAtOrBefore(at, graph.Wave.Wave.PausedAt)
}

func timeCoversReadyWindowHistory(at time.Time, window *domain.ReadyWindow) bool {
	return window == nil || (pausedomain.TimeAtOrBefore(at, window.OpenedAt) && pausedomain.TimePointerAtOrBefore(at, window.ConsumedAt))
}

func timeCoversPresenceSetHistory(at time.Time, values []pausedomain.PausePresence) bool {
	for _, presence := range values {
		if !pausedomain.TimeCoversPresenceHistory(at, presence) {
			return false
		}
	}
	return true
}

func timeCoversReconnectSetHistory(at time.Time, values []pausedomain.PauseReconnectInterval) bool {
	for _, interval := range values {
		if !pausedomain.TimeCoversReconnectHistory(at, interval) {
			return false
		}
	}
	return true
}

func timeCoversOptionalDraftHistory(at time.Time, draft *draftusecase.Execution) bool {
	return draft == nil || timeCoversDraftHistory(at, *draft)
}

func timeCoversFrozenDeadlineHistory(at time.Time, values []PauseFrozenDeadline) bool {
	for _, frozen := range values {
		if !pausedomain.TimeAtOrBefore(at, frozen.FrozenAt) || !pausedomain.TimePointerAtOrBefore(at, frozen.ResumedAt) {
			return false
		}
	}
	return true
}

func timeCoversDraftHistory(at time.Time, draft draftusecase.Execution) bool {
	if !pausedomain.TimeAtOrBefore(at, draft.FirstActorDecision.DecidedAt) {
		return false
	}
	for _, action := range draft.Actions {
		if !pausedomain.TimeAtOrBefore(at, action.OccurredAt) ||
			(action.DecisionEvidence != nil && !pausedomain.TimeAtOrBefore(at, action.DecisionEvidence.DecidedAt)) {
			return false
		}
	}
	if draft.Recovery != nil && !pausedomain.TimeAtOrBefore(at, draft.Recovery.RecordedAt) {
		return false
	}
	return draft.Transition == nil || pausedomain.TimeAtOrBefore(at, draft.Transition.OccurredAt)
}

func safePauseTimeAddPointer(at *time.Time, duration time.Duration) (time.Time, bool) {
	if at == nil {
		return time.Time{}, false
	}
	return pausedomain.AddTime(*at, duration)
}

func eligiblePauseDeadlines(graph PauseGraph, paused bool) map[pauseDeadlineIdentity]time.Time {
	expected := make(map[pauseDeadlineIdentity]time.Time)
	addEligibleWaveDeadline(expected, graph)
	addEligibleGameDeadlines(expected, graph.Games, paused)
	addEligibleDraftDeadline(expected, graph.Draft, paused)
	return expected
}

func addEligibleWaveDeadline(expected map[pauseDeadlineIdentity]time.Time, graph PauseGraph) {
	if graph.Wave.Wave.ReadyWindow != nil &&
		(graph.Wave.Wave.State == domain.WaveStateReadyWindowOpen || graph.Wave.Wave.State == domain.WaveStateReady) {
		window := graph.Wave.Wave.ReadyWindow
		expected[pauseDeadlineIdentity{Kind: PauseDeadlineReadyWindow, OwnerID: window.ID}] = window.Deadline
	}
}

func addEligibleGameDeadlines(expected map[pauseDeadlineIdentity]time.Time, games []PauseGame, paused bool) {
	for _, game := range games {
		if paused && game.Game.State == domain.GameStatePaused && game.ResumeState != nil && *game.ResumeState == domain.GameStateActive {
			expected[pauseDeadlineIdentity{Kind: PauseDeadlineGame, OwnerID: game.Game.ID}] = time.Time{}
		}
		if !paused && game.Game.State == domain.GameStateActive && game.Deadline != nil {
			expected[pauseDeadlineIdentity{Kind: PauseDeadlineGame, OwnerID: game.Game.ID}] = *game.Deadline
		}
	}
}

func addEligibleDraftDeadline(expected map[pauseDeadlineIdentity]time.Time, draft *draftusecase.Execution, paused bool) {
	if draft != nil {
		if paused && draft.State == draftusecase.ExecutionStatePaused && draft.Recovery != nil {
			expected[pauseDeadlineIdentity{Kind: PauseDeadlineDraft, OwnerID: draft.ID}] = draft.Recovery.PreviousDeadline
		}
		if !paused && draft.State == draftusecase.ExecutionStateActive && draft.AbsoluteDeadline != nil {
			expected[pauseDeadlineIdentity{Kind: PauseDeadlineDraft, OwnerID: draft.ID}] = *draft.AbsoluteDeadline
		}
	}
}
