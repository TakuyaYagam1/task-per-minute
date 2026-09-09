package game

import (
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func waveValidServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}

func validReadyWindowSourceRevisions(revisions domain.ReadyWindowSourceRevisions) bool {
	return revisions.IsValid()
}

func waveCloneWaveExecution(wave domain.Wave) domain.Wave {
	cloned := wave
	cloned.Members = append([]domain.WaveMember(nil), wave.Members...)
	if wave.ReadyWindow != nil {
		window := *wave.ReadyWindow
		window.ConsumedAt = waveCloneTimePointer(wave.ReadyWindow.ConsumedAt)
		cloned.ReadyWindow = &window
	}
	cloned.StartedAt = waveCloneTimePointer(wave.StartedAt)
	cloned.PausedAt = waveCloneTimePointer(wave.PausedAt)
	return cloned
}

func waveCloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func waveWavesEqual(first, second domain.Wave) bool {
	if first.ID != second.ID || first.TournamentID != second.TournamentID || first.RevisionID != second.RevisionID ||
		first.State != second.State || len(first.Members) != len(second.Members) ||
		!waveTimePointersEqual(first.StartedAt, second.StartedAt) || !waveTimePointersEqual(first.PausedAt, second.PausedAt) {
		return false
	}
	for index := range first.Members {
		if first.Members[index] != second.Members[index] {
			return false
		}
	}
	return waveReadyWindowsEqual(first.ReadyWindow, second.ReadyWindow)
}

func waveReadyWindowsEqual(first, second *domain.ReadyWindow) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.ID == second.ID && first.WaveID == second.WaveID && first.RevisionID == second.RevisionID &&
		first.State == second.State && first.OpenedAt.Equal(second.OpenedAt) && first.Deadline.Equal(second.Deadline) &&
		waveTimePointersEqual(first.ConsumedAt, second.ConsumedAt)
}

func waveTimePointersEqual(first, second *time.Time) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.Equal(*second)
}
