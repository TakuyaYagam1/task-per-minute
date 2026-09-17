package replay

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func replayValidServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}

func replayCloneGame(game domain.Game) domain.Game {
	clone := game
	if game.WinnerID != nil {
		winnerID := *game.WinnerID
		clone.WinnerID = &winnerID
	}
	if game.ResultRevisionID != nil {
		revisionID := *game.ResultRevisionID
		clone.ResultRevisionID = &revisionID
	}
	return clone
}

func replayCloneGameSlot(slot domain.GameSlot) domain.GameSlot {
	clone := slot
	clone.Attempts = make([]domain.Game, len(slot.Attempts))
	for index := range slot.Attempts {
		clone.Attempts[index] = replayCloneGame(slot.Attempts[index])
	}
	return clone
}

func replayCloneWaveExecution(wave domain.Wave) domain.Wave {
	clone := wave
	clone.Members = append([]domain.WaveMember(nil), wave.Members...)
	if wave.ReadyWindow != nil {
		window := *wave.ReadyWindow
		window.ConsumedAt = replayCloneTimePointer(wave.ReadyWindow.ConsumedAt)
		clone.ReadyWindow = &window
	}
	clone.StartedAt = replayCloneTimePointer(wave.StartedAt)
	clone.PausedAt = replayCloneTimePointer(wave.PausedAt)
	return clone
}

func cloneSeriesExecution(seriesExecution seriesdomain.Execution) seriesdomain.Execution {
	clone := seriesExecution
	if seriesExecution.ResumeState != nil {
		resumeState := *seriesExecution.ResumeState
		clone.ResumeState = &resumeState
	}
	clone.Series = cloneSeries(seriesExecution.Series)
	return clone
}

func cloneSeries(series domain.Series) domain.Series {
	clone := series
	if series.WinnerID != nil {
		winnerID := *series.WinnerID
		clone.WinnerID = &winnerID
	}
	if series.CurrentScoreRevisionID != nil {
		revisionID := *series.CurrentScoreRevisionID
		clone.CurrentScoreRevisionID = &revisionID
	}
	if series.CurrentResultRevisionID != nil {
		revisionID := *series.CurrentResultRevisionID
		clone.CurrentResultRevisionID = &revisionID
	}
	clone.Slots = make([]domain.GameSlot, len(series.Slots))
	for index := range series.Slots {
		clone.Slots[index] = replayCloneGameSlot(series.Slots[index])
	}
	return clone
}

func replayCloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func replayUniqueNonZeroUUIDs(values []uuid.UUID) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func replayWavesEqual(first, second domain.Wave) bool {
	if first.ID != second.ID || first.TournamentID != second.TournamentID ||
		first.RevisionID != second.RevisionID || first.State != second.State ||
		len(first.Members) != len(second.Members) || !replayTimePointersEqual(first.StartedAt, second.StartedAt) ||
		!replayTimePointersEqual(first.PausedAt, second.PausedAt) {
		return false
	}
	for index := range first.Members {
		if first.Members[index] != second.Members[index] {
			return false
		}
	}
	return replayReadyWindowsEqual(first.ReadyWindow, second.ReadyWindow)
}

func replayReadyWindowsEqual(first, second *domain.ReadyWindow) bool {
	if first == nil || second == nil {
		return first == second
	}
	return first.ID == second.ID && first.WaveID == second.WaveID &&
		first.RevisionID == second.RevisionID && first.State == second.State &&
		first.OpenedAt.Equal(second.OpenedAt) && first.Deadline.Equal(second.Deadline) &&
		replayTimePointersEqual(first.ConsumedAt, second.ConsumedAt)
}

func replayTimePointersEqual(first, second *time.Time) bool {
	if first == nil || second == nil {
		return first == second
	}
	return first.Equal(*second)
}
