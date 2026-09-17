package forfeit

import (
	"slices"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func forfeitCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func forfeitUUIDPointersEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func forfeitOfficialResultRevisionPointersEqual(first, second *domain.OfficialResultRevisionID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func forfeitCloneOfficialResultRevisionIDPointer(
	value *domain.OfficialResultRevisionID,
) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func forfeitCloneSeriesScoreRevisionIDPointer(
	value *domain.SeriesScoreRevisionID,
) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func forfeitCloneGame(game domain.Game) domain.Game {
	cloned := game
	cloned.WinnerID = forfeitCloneUUIDPointer(game.WinnerID)
	cloned.ResultRevisionID = forfeitCloneOfficialResultRevisionIDPointer(game.ResultRevisionID)
	return cloned
}

func forfeitSettlementGamesEqual(first, second domain.Game) bool {
	return first.ID == second.ID && first.SlotID == second.SlotID && first.AttemptNo == second.AttemptNo &&
		first.State == second.State && first.ResultReason == second.ResultReason &&
		forfeitUUIDPointersEqual(first.WinnerID, second.WinnerID) &&
		forfeitOfficialResultRevisionPointersEqual(first.ResultRevisionID, second.ResultRevisionID)
}

func cloneScoreRevision(revision seriesdomain.ScoreRevision) seriesdomain.ScoreRevision {
	cloned := revision
	cloned.PreviousRevisionID = forfeitCloneSeriesScoreRevisionIDPointer(revision.PreviousRevisionID)
	cloned.GameResultRevisionIDs = slices.Clone(revision.GameResultRevisionIDs)
	return cloned
}
