package top4

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func cloneTerminalSeries(value domain.Series) domain.Series {
	clone := value
	clone.WinnerID = cloneTerminalSeriesUUIDPointer(value.WinnerID)
	clone.CurrentScoreRevisionID = cloneTerminalSeriesScoreRevisionID(value.CurrentScoreRevisionID)
	clone.CurrentResultRevisionID = cloneTerminalSeriesResultRevisionID(value.CurrentResultRevisionID)
	clone.Slots = make([]domain.GameSlot, len(value.Slots))
	for index := range value.Slots {
		clone.Slots[index] = cloneTerminalSeriesGameSlot(value.Slots[index])
	}
	return clone
}

func cloneTerminalSeriesGameSlot(value domain.GameSlot) domain.GameSlot {
	clone := value
	clone.Attempts = make([]domain.Game, len(value.Attempts))
	for index, attempt := range value.Attempts {
		clone.Attempts[index] = attempt
		clone.Attempts[index].WinnerID = cloneTerminalSeriesUUIDPointer(attempt.WinnerID)
		clone.Attempts[index].ResultRevisionID = cloneTerminalSeriesResultRevisionID(attempt.ResultRevisionID)
	}
	return clone
}

func cloneTerminalSeriesUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneTerminalSeriesScoreRevisionID(
	value *domain.SeriesScoreRevisionID,
) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneTerminalSeriesResultRevisionID(
	value *domain.OfficialResultRevisionID,
) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
