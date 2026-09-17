package noshow

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func noShowCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func noShowUUIDPointersEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func noShowCloneOfficialResultRevisionIDPointer(
	value *domain.OfficialResultRevisionID,
) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func noShowCloneSeriesScoreRevisionIDPointer(
	value *domain.SeriesScoreRevisionID,
) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneWave(wave domain.Wave) domain.Wave {
	cloned := wave
	cloned.Members = append([]domain.WaveMember(nil), wave.Members...)
	if wave.ReadyWindow != nil {
		window := *wave.ReadyWindow
		window.ConsumedAt = noShowCloneTimePointer(wave.ReadyWindow.ConsumedAt)
		cloned.ReadyWindow = &window
	}
	cloned.StartedAt = noShowCloneTimePointer(wave.StartedAt)
	cloned.PausedAt = noShowCloneTimePointer(wave.PausedAt)
	return cloned
}

func noShowCloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func noShowWavesEqual(first, second domain.Wave) bool {
	if first.ID != second.ID || first.TournamentID != second.TournamentID || first.RevisionID != second.RevisionID ||
		first.State != second.State || len(first.Members) != len(second.Members) ||
		!noShowTimePointersEqual(first.StartedAt, second.StartedAt) || !noShowTimePointersEqual(first.PausedAt, second.PausedAt) {
		return false
	}
	for index := range first.Members {
		if first.Members[index] != second.Members[index] {
			return false
		}
	}
	return noShowReadyWindowsEqual(first.ReadyWindow, second.ReadyWindow)
}

func noShowReadyWindowsEqual(first, second *domain.ReadyWindow) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.ID == second.ID && first.WaveID == second.WaveID && first.RevisionID == second.RevisionID &&
		first.State == second.State && first.OpenedAt.Equal(second.OpenedAt) && first.Deadline.Equal(second.Deadline) &&
		noShowTimePointersEqual(first.ConsumedAt, second.ConsumedAt)
}

func noShowTimePointersEqual(first, second *time.Time) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.Equal(*second)
}
