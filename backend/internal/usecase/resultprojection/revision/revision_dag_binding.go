package revision

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
)

func sameDerivedRevisionIDSet(
	left []domain.DerivedRevisionID,
	right []domain.DerivedRevisionID,
) bool {
	if len(left) != len(right) {
		return false
	}
	want := make(map[domain.DerivedRevisionID]struct{}, len(right))
	for _, id := range right {
		if _, duplicate := want[id]; duplicate {
			return false
		}
		want[id] = struct{}{}
	}
	for _, id := range left {
		if _, exists := want[id]; !exists {
			return false
		}
		delete(want, id)
	}
	return len(want) == 0
}

func gameHeadMatchesScoreAttempt(
	game resultusecase.OfficialResultRevisionHead,
	attempt resultusecase.SeriesScoreAttemptReference,
) bool {
	outcome := game.Outcome
	return outcome.GameState == attempt.State && outcome.GameReason == attempt.Reason &&
		uuidPointersEqual(outcome.WinnerID, attempt.WinnerID) && game.ID == attempt.CurrentGameResultRevisionID
}

func revisionDAGScopeKey(scope resultusecase.OfficialResultScope) string {
	return scope.TournamentID.String() + "/" + scope.SeriesID.String() + "/" +
		string(scope.Kind) + "/" + scope.GameID.String()
}
