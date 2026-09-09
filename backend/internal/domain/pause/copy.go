package pause

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func CloneSeries(series domain.Series) domain.Series {
	cloned := series
	cloned.WinnerID = cloneUUIDPointer(series.WinnerID)
	cloned.CurrentScoreRevisionID = cloneSeriesScoreRevisionIDPointer(series.CurrentScoreRevisionID)
	cloned.CurrentResultRevisionID = cloneOfficialResultRevisionIDPointer(series.CurrentResultRevisionID)
	cloned.Slots = make([]domain.GameSlot, len(series.Slots))
	for index := range series.Slots {
		cloned.Slots[index] = cloneGameSlot(series.Slots[index])
	}
	return cloned
}

func CloneGame(game domain.Game) domain.Game {
	cloned := game
	cloned.WinnerID = cloneUUIDPointer(game.WinnerID)
	cloned.ResultRevisionID = cloneOfficialResultRevisionIDPointer(game.ResultRevisionID)
	return cloned
}

func cloneGameSlot(slot domain.GameSlot) domain.GameSlot {
	cloned := slot
	cloned.Attempts = make([]domain.Game, len(slot.Attempts))
	for index := range slot.Attempts {
		cloned.Attempts[index] = CloneGame(slot.Attempts[index])
	}
	return cloned
}

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSeriesScoreRevisionIDPointer(value *domain.SeriesScoreRevisionID) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneOfficialResultRevisionIDPointer(value *domain.OfficialResultRevisionID) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
