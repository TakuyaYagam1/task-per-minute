package semifinal

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func cloneSemifinalSeries(value domain.Series) domain.Series {
	clone := value
	clone.WinnerID = cloneSemifinalSeriesUUIDPointer(value.WinnerID)
	clone.CurrentScoreRevisionID = cloneSemifinalSeriesScoreRevisionID(value.CurrentScoreRevisionID)
	clone.CurrentResultRevisionID = cloneSemifinalSeriesResultRevisionID(value.CurrentResultRevisionID)
	clone.Slots = make([]domain.GameSlot, len(value.Slots))
	for index := range value.Slots {
		clone.Slots[index] = cloneSemifinalSeriesGameSlot(value.Slots[index])
	}
	return clone
}

func cloneSemifinalSeriesGameSlot(value domain.GameSlot) domain.GameSlot {
	clone := value
	clone.Attempts = make([]domain.Game, len(value.Attempts))
	for index, attempt := range value.Attempts {
		clone.Attempts[index] = attempt
		clone.Attempts[index].WinnerID = cloneSemifinalSeriesUUIDPointer(attempt.WinnerID)
		clone.Attempts[index].ResultRevisionID = cloneSemifinalSeriesResultRevisionID(attempt.ResultRevisionID)
	}
	return clone
}

func cloneSemifinalSeriesUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSemifinalSeriesScoreRevisionID(value *domain.SeriesScoreRevisionID) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSemifinalSeriesResultRevisionID(value *domain.OfficialResultRevisionID) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
