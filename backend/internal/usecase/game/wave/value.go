package wave

import (
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func ValidServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}

func ValidReadyWindowSourceRevisions(revisions domain.ReadyWindowSourceRevisions) bool {
	return revisions.IsValid()
}

func Clone(wave domain.Wave) domain.Wave {
	cloned := wave
	cloned.Members = append([]domain.WaveMember(nil), wave.Members...)
	if wave.ReadyWindow != nil {
		window := *wave.ReadyWindow
		window.ConsumedAt = cloneTimePointer(wave.ReadyWindow.ConsumedAt)
		cloned.ReadyWindow = &window
	}
	cloned.StartedAt = cloneTimePointer(wave.StartedAt)
	cloned.PausedAt = cloneTimePointer(wave.PausedAt)
	return cloned
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func Equal(first, second domain.Wave) bool {
	if first.ID != second.ID || first.TournamentID != second.TournamentID || first.RevisionID != second.RevisionID ||
		first.State != second.State || len(first.Members) != len(second.Members) ||
		!timePointersEqual(first.StartedAt, second.StartedAt) || !timePointersEqual(first.PausedAt, second.PausedAt) {
		return false
	}
	for index := range first.Members {
		if first.Members[index] != second.Members[index] {
			return false
		}
	}
	return readyWindowsEqual(first.ReadyWindow, second.ReadyWindow)
}

func readyWindowsEqual(first, second *domain.ReadyWindow) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.ID == second.ID && first.WaveID == second.WaveID && first.RevisionID == second.RevisionID &&
		first.State == second.State && first.OpenedAt.Equal(second.OpenedAt) && first.Deadline.Equal(second.Deadline) &&
		timePointersEqual(first.ConsumedAt, second.ConsumedAt)
}

func timePointersEqual(first, second *time.Time) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.Equal(*second)
}
