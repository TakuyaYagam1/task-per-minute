package resultprojection

import "github.com/TakuyaYagam1/task-per-minute/internal/domain"

func publicOfficialResultsEqual(first, second PublicOfficialResult) bool {
	return first.Subject == second.Subject && first.TournamentID == second.TournamentID &&
		first.SeriesID == second.SeriesID && uuidPointersEqual(first.GameID, second.GameID) &&
		first.Status == second.Status && uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		seriesScorePointersEqual(first.Score, second.Score) && first.ResolvedAt.Equal(second.ResolvedAt)
}

func operatorOfficialResultsEqual(first, second OperatorOfficialResult) bool {
	return publicOfficialResultsEqual(first.Public, second.Public) &&
		first.ResultRevisionID == second.ResultRevisionID &&
		officialResultRevisionIDPointersEqual(first.PreviousResultRevisionID, second.PreviousResultRevisionID) &&
		seriesScoreRevisionPointersEqual(first.ScoreRevisionID, second.ScoreRevisionID) &&
		first.SourceProjectionRevisionID == second.SourceProjectionRevisionID &&
		derivedRevisionIDPointersEqual(first.ScoreProjectionRevisionID, second.ScoreProjectionRevisionID) &&
		gameStatePointersEqual(first.GameState, second.GameState) &&
		gameReasonPointersEqual(first.GameReason, second.GameReason) &&
		seriesStatePointersEqual(first.SeriesState, second.SeriesState) &&
		seriesReasonPointersEqual(first.SeriesReason, second.SeriesReason) &&
		officialResultCausePointersEqual(first.Cause, second.Cause) &&
		first.CommandID == second.CommandID && noShowActionPointersEqual(first.NoShowAction, second.NoShowAction)
}

func seriesScorePointersEqual(first, second *domain.SeriesScore) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func gameStatePointersEqual(first, second *domain.GameState) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func gameReasonPointersEqual(first, second *domain.GameResultReason) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func seriesStatePointersEqual(first, second *domain.SeriesState) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func seriesReasonPointersEqual(first, second *domain.SeriesResultReason) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func officialResultCausePointersEqual(first, second *OfficialResultCause) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func noShowActionPointersEqual(first, second *domain.NormalNoShowAction) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}
