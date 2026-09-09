package readiness

import "github.com/TakuyaYagam1/task-per-minute/internal/domain"

func cloneWave(wave domain.Wave) domain.Wave {
	clone := wave
	clone.Members = append([]domain.WaveMember(nil), wave.Members...)
	if wave.ReadyWindow != nil {
		window := *wave.ReadyWindow
		if wave.ReadyWindow.ConsumedAt != nil {
			consumedAt := *wave.ReadyWindow.ConsumedAt
			window.ConsumedAt = &consumedAt
		}
		clone.ReadyWindow = &window
	}
	if wave.StartedAt != nil {
		startedAt := *wave.StartedAt
		clone.StartedAt = &startedAt
	}
	if wave.PausedAt != nil {
		pausedAt := *wave.PausedAt
		clone.PausedAt = &pausedAt
	}
	return clone
}
