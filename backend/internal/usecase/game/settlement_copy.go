package game

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func settlementOfficialResultRevisionPointersEqual(
	first, second *domain.OfficialResultRevisionID,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func settlementCloneOfficialResultRevisionIDPointer(
	value *domain.OfficialResultRevisionID,
) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func settlementCloneSeriesScoreRevisionIDPointer(
	value *domain.SeriesScoreRevisionID,
) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func settlementUUIDPointersEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func settlementSettlementGamesEqual(first, second domain.Game) bool {
	return first.ID == second.ID && first.SlotID == second.SlotID && first.AttemptNo == second.AttemptNo &&
		first.State == second.State && first.ResultReason == second.ResultReason &&
		settlementUUIDPointersEqual(first.WinnerID, second.WinnerID) &&
		settlementOfficialResultRevisionPointersEqual(first.ResultRevisionID, second.ResultRevisionID)
}

func settlementCloneGame(game domain.Game) domain.Game {
	cloned := game
	cloned.WinnerID = settlementCloneUUIDPointer(game.WinnerID)
	cloned.ResultRevisionID = settlementCloneOfficialResultRevisionIDPointer(game.ResultRevisionID)
	return cloned
}

func settlementCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
