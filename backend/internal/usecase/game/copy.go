package game

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func attemptUniqueNonZeroUUIDs(values []uuid.UUID) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func attemptCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func attemptCloneOfficialResultRevisionIDPointer(
	value *domain.OfficialResultRevisionID,
) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func attemptCloneSeriesScoreRevisionIDPointer(
	value *domain.SeriesScoreRevisionID,
) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func attemptCloneGame(game domain.Game) domain.Game {
	cloned := game
	cloned.WinnerID = attemptCloneUUIDPointer(game.WinnerID)
	cloned.ResultRevisionID = attemptCloneOfficialResultRevisionIDPointer(game.ResultRevisionID)
	return cloned
}

func attemptCloneGameSlot(slot domain.GameSlot) domain.GameSlot {
	cloned := slot
	cloned.Attempts = make([]domain.Game, len(slot.Attempts))
	for index := range slot.Attempts {
		cloned.Attempts[index] = attemptCloneGame(slot.Attempts[index])
	}
	return cloned
}

func attemptSettlementGamesEqual(first, second domain.Game) bool {
	return first.ID == second.ID && first.SlotID == second.SlotID && first.AttemptNo == second.AttemptNo &&
		first.State == second.State && first.ResultReason == second.ResultReason &&
		attemptUUIDPointersEqual(first.WinnerID, second.WinnerID) &&
		attemptOfficialResultRevisionPointersEqual(first.ResultRevisionID, second.ResultRevisionID)
}

func attemptUUIDPointersEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func attemptOfficialResultRevisionPointersEqual(
	first, second *domain.OfficialResultRevisionID,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}
