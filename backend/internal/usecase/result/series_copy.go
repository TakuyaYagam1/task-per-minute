package result

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSeriesScoreRevisionIDPointer(
	value *domain.SeriesScoreRevisionID,
) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneOfficialResultRevisionIDPointer(
	value *domain.OfficialResultRevisionID,
) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSeries(series domain.Series) domain.Series {
	clone := series
	clone.WinnerID = cloneUUIDPointer(series.WinnerID)
	clone.CurrentScoreRevisionID = cloneSeriesScoreRevisionIDPointer(series.CurrentScoreRevisionID)
	clone.CurrentResultRevisionID = cloneOfficialResultRevisionIDPointer(series.CurrentResultRevisionID)
	clone.Slots = make([]domain.GameSlot, len(series.Slots))
	for index := range series.Slots {
		clone.Slots[index] = cloneGameSlot(series.Slots[index])
	}
	return clone
}

func cloneGameSlot(slot domain.GameSlot) domain.GameSlot {
	clone := slot
	clone.Attempts = make([]domain.Game, len(slot.Attempts))
	for index := range slot.Attempts {
		clone.Attempts[index] = cloneGame(slot.Attempts[index])
	}
	return clone
}

func cloneGame(game domain.Game) domain.Game {
	clone := game
	clone.WinnerID = cloneUUIDPointer(game.WinnerID)
	clone.ResultRevisionID = cloneOfficialResultRevisionIDPointer(game.ResultRevisionID)
	return clone
}

func uuidPointersEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}

func seriesScoreRevisionPointersEqual(
	first *domain.SeriesScoreRevisionID,
	second *domain.SeriesScoreRevisionID,
) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}

func officialResultRevisionPointersEqual(
	first *domain.OfficialResultRevisionID,
	second *domain.OfficialResultRevisionID,
) bool {
	if first == nil || second == nil {
		return first == second
	}
	return *first == *second
}
