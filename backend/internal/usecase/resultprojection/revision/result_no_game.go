package revision

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
)

type noGameOrigin uint8

const noGameSQLOrigin noGameOrigin = 1

// RestoreSQLNoGameResult accepts the immutable terminal-node layout: each
// source node has its source revision ID, with per-head SQL revision ordinals.
// The no-show event ordinals remain independent of those head ordinals.
func RestoreSQLNoGameResult(recorded RecordedNoGameResult) (RecordedNoGameResult, error) {
	recorded.restoredOrigin = noGameSQLOrigin
	if err := validateRecordedNoGameResult(recorded); err != nil {
		return RecordedNoGameResult{}, err
	}
	return cloneRecordedNoGameResult(recorded)
}

func (recorded RecordedNoGameResult) HasSQLSourceIdentity() bool {
	return recorded.restoredOrigin == noGameSQLOrigin && validateRecordedNoGameResult(recorded) == nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func validSQLNoGameIdentity(recorded RecordedNoGameResult) bool {
	if len(recorded.GameResults) != len(recorded.GameProjections) || recorded.Score.PreviousRevisionID == nil ||
		recorded.Series.PreviousRevisionID != nil {
		return false
	}
	score, result := recorded.ScoreProjection.Revision(), recorded.ResultProjection.Revision()
	previous := score.PreviousRevisionID()
	if score.ID().UUID() != recorded.Score.ID.UUID() || score.RevisionNo() != 2 || previous == nil ||
		previous.UUID() != recorded.Score.PreviousRevisionID.UUID() ||
		result.ID().UUID() != recorded.Series.ID.UUID() || result.RevisionNo() != 1 || result.PreviousRevisionID() != nil ||
		!score.CreatedAt().Equal(recorded.ResolvedAt) || !result.CreatedAt().Equal(recorded.ResolvedAt) {
		return false
	}
	for index, game := range recorded.GameResults {
		node := recorded.GameProjections[index].Revision()
		if node.ID().UUID() != game.ID.UUID() || node.RevisionNo() != 1 || node.PreviousRevisionID() != nil ||
			!node.CreatedAt().Equal(recorded.ResolvedAt) {
			return false
		}
	}
	return true
}

func deriveRecordedNoGameProjection(
	recorded RecordedNoGameResult,
) (PublicOfficialResult, OperatorOfficialResult, error) {
	if err := validateRecordedNoGameResult(recorded); err != nil {
		return PublicOfficialResult{}, OperatorOfficialResult{}, err
	}
	public := PublicOfficialResult{
		Subject:      resultusecase.OfficialResultSubjectSeries,
		TournamentID: recorded.Scope.TournamentID,
		SeriesID:     recorded.Scope.SeriesID,
		WinnerID:     cloneUUIDPointer(recorded.Series.WinnerID),
		Score:        cloneSeriesScorePointer(&recorded.Score.Score),
		ResolvedAt:   recorded.ResolvedAt,
	}
	cause := OfficialResultCauseNoShow
	if recorded.Action == domain.NormalNoShowActionReopenWave {
		public.Status = OfficialResultStatusNoShow
	} else {
		public.Status = OfficialResultStatusVoid
	}
	seriesState := recorded.Series.State
	resultSourceID := recorded.ResultProjection.Revision().ID()
	scoreSourceID := recorded.ScoreProjection.Revision().ID()
	action := recorded.Action
	operator := OperatorOfficialResult{
		Public:                     clonePublicOfficialResult(public),
		ResultRevisionID:           recorded.Series.ID,
		PreviousResultRevisionID:   cloneOfficialResultRevisionIDPointer(recorded.Series.PreviousRevisionID),
		ScoreRevisionID:            cloneSeriesScoreRevisionIDPointer(&recorded.Score.ID),
		SourceProjectionRevisionID: resultSourceID,
		ScoreProjectionRevisionID:  &scoreSourceID,
		SeriesState:                &seriesState,
		Cause:                      &cause,
		CommandID:                  recorded.CommandID,
		NoShowAction:               &action,
	}
	return public, operator, nil
}
