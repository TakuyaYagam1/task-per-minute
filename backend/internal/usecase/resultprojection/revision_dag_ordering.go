package resultprojection

import (
	"sort"

	"github.com/google/uuid"
)

func canonicalOfficialResultPlans(plans []OfficialResultProjectionPlan) {
	sort.Slice(plans, func(first, second int) bool {
		left := plans[first].Public()
		right := plans[second].Public()
		if left.TournamentID != right.TournamentID {
			return left.TournamentID.String() < right.TournamentID.String()
		}
		if left.SeriesID != right.SeriesID {
			return left.SeriesID.String() < right.SeriesID.String()
		}
		if left.Subject != right.Subject {
			return left.Subject < right.Subject
		}
		leftGame := uuid.Nil
		rightGame := uuid.Nil
		if left.GameID != nil {
			leftGame = *left.GameID
		}
		if right.GameID != nil {
			rightGame = *right.GameID
		}
		return leftGame.String() < rightGame.String()
	})
}
