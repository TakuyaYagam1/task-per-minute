package resultprojection

import (
	"math"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
)

func ordinaryOfficialResultInputIsZero(input OfficialResultProjectionInput) bool {
	result := input.Result
	return result.Scope == (resultusecase.OfficialResultScope{}) && result.ID.IsZero() &&
		result.PreviousRevisionID == nil && result.Ordinal == 0 && result.CommandID == uuid.Nil &&
		result.Actor == (domain.ResultActor{}) && result.Outcome == (resultusecase.OfficialResultOutcome{}) &&
		result.SourceProjection.ID().IsZero() && result.RecordedAt.IsZero() &&
		input.Score == nil && input.ScoreProjection == nil &&
		input.ResultProjection.Revision().ID().IsZero() && len(input.ResultProjection.Payload()) == 0
}

func deriveRevisionHeadProjection(
	input OfficialResultProjectionInput,
) (PublicOfficialResult, OperatorOfficialResult, error) {
	if input.Result.Ordinal == math.MaxInt || input.Result.Validate() != nil {
		return PublicOfficialResult{}, OperatorOfficialResult{}, invalidOfficialResultProjection("invalid current result head")
	}
	if err := validateExactProjection(input.Result.SourceProjection, input.ResultProjection); err != nil {
		return PublicOfficialResult{}, OperatorOfficialResult{}, err
	}

	scope := input.Result.Scope
	outcome := input.Result.Outcome
	public := PublicOfficialResult{
		Subject: scope.Kind, TournamentID: scope.TournamentID, SeriesID: scope.SeriesID,
		WinnerID: cloneUUIDPointer(outcome.WinnerID), ResolvedAt: input.Result.RecordedAt,
	}
	operator := OperatorOfficialResult{
		ResultRevisionID:           input.Result.ID,
		PreviousResultRevisionID:   cloneOfficialResultRevisionIDPointer(input.Result.PreviousRevisionID),
		SourceProjectionRevisionID: input.Result.SourceProjection.ID(),
		CommandID:                  input.Result.CommandID,
	}

	switch scope.Kind {
	case resultusecase.OfficialResultSubjectGame:
		if input.Score != nil || input.ScoreProjection != nil {
			return PublicOfficialResult{}, OperatorOfficialResult{}, invalidOfficialResultProjection("Game projection has Series score evidence")
		}
		status, err := projectGameResultStatus(outcome.GameState, outcome.GameReason)
		if err != nil {
			return PublicOfficialResult{}, OperatorOfficialResult{}, err
		}
		public.GameID = cloneUUIDPointer(&scope.GameID)
		public.Status = status
		operator.GameState = cloneGameStatePointer(&outcome.GameState)
		operator.GameReason = cloneGameReasonPointer(&outcome.GameReason)
	case resultusecase.OfficialResultSubjectSeries:
		if input.Score == nil || input.ScoreProjection == nil {
			return PublicOfficialResult{}, OperatorOfficialResult{}, invalidOfficialResultProjection("Series projection is missing current score evidence")
		}
		if err := validateSeriesProjectionBinding(input.Result, *input.Score, *input.ScoreProjection); err != nil {
			return PublicOfficialResult{}, OperatorOfficialResult{}, err
		}
		public.Status = OfficialResultStatusCompleted
		if outcome.SeriesState == domain.SeriesStateCancelled {
			public.Status = OfficialResultStatusVoid
		}
		score := input.Score.Score
		public.Score = &score
		operator.ScoreRevisionID = cloneSeriesScoreRevisionIDPointer(&input.Score.ID)
		scoreSourceID := input.Score.SourceProjection.ID()
		operator.ScoreProjectionRevisionID = &scoreSourceID
		operator.SeriesState = cloneSeriesStatePointer(&outcome.SeriesState)
		operator.SeriesReason = cloneSeriesReasonPointer(&outcome.SeriesReason)
	default:
		return PublicOfficialResult{}, OperatorOfficialResult{}, invalidOfficialResultProjection("unknown result subject")
	}
	operator.Public = clonePublicOfficialResult(public)
	return public, operator, nil
}

func validateSeriesProjectionBinding(
	result resultusecase.OfficialResultRevisionHead,
	score resultusecase.SeriesScoreRevisionHead,
	scoreProjection domain.ProjectionRevision,
) error {
	if score.Ordinal == math.MaxInt || score.Validate() != nil {
		return invalidOfficialResultProjection("invalid current score head")
	}
	if err := validateExactProjection(score.SourceProjection, scoreProjection); err != nil {
		return err
	}
	if score.Scope != (resultusecase.SeriesScoreRevisionScope{
		TournamentID: result.Scope.TournamentID,
		SeriesID:     result.Scope.SeriesID,
	}) || result.Outcome.ScoreRevisionID == nil || *result.Outcome.ScoreRevisionID != score.ID {
		return invalidOfficialResultProjection("Series result and score heads do not match")
	}
	if result.RecordedAt.Before(score.RecordedAt) ||
		result.SourceProjection.CreatedAt().Before(score.SourceProjection.CreatedAt()) {
		return invalidOfficialResultProjection("Series result predates its current score")
	}
	if result.Outcome.SeriesState == domain.SeriesStateCompleted {
		winner := score.Score.Winner(score.FirstParticipantID, score.SecondParticipantID, score.Format)
		if winner == nil || result.Outcome.WinnerID == nil || *winner != *result.Outcome.WinnerID {
			return invalidOfficialResultProjection("Series winner does not match current score")
		}
	}
	return nil
}

func validateExactProjection(
	want domain.DerivedRevision,
	projection domain.ProjectionRevision,
) error {
	if projection.Validate() != nil || !derivedRevisionsEqual(want, projection.Revision()) {
		return invalidOfficialResultProjection("projection payload does not match current revision")
	}
	return nil
}

func projectGameResultStatus(
	state domain.GameState,
	reason domain.GameResultReason,
) (OfficialResultStatus, error) {
	switch state {
	case domain.GameStateCompleted:
		if reason == domain.GameResultReasonSolved {
			return OfficialResultStatusSolved, nil
		}
		return OfficialResultStatusCompleted, nil
	case domain.GameStateVoid:
		return OfficialResultStatusVoid, nil
	case domain.GameStateCancelled:
		return OfficialResultStatusCancelled, nil
	case domain.GameStateSuperseded:
		return OfficialResultStatusSuperseded, nil
	case domain.GameStatePlanned, domain.GameStateReady,
		domain.GameStateActive, domain.GameStatePaused:
		return "", invalidOfficialResultProjection("non-terminal Game status")
	default:
		return "", invalidOfficialResultProjection("unknown Game status")
	}
}
