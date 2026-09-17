package revision

import "github.com/TakuyaYagam1/task-per-minute/internal/domain"

func cloneOfficialResultProjectionInput(
	input OfficialResultProjectionInput,
) (OfficialResultProjectionInput, error) {
	clone := input
	clone.Result = input.Result.Clone()
	var err error
	clone.ResultProjection, err = cloneOfficialProjectionRevision(input.ResultProjection)
	if input.NoGame == nil && err != nil {
		return OfficialResultProjectionInput{}, err
	}
	if input.Score != nil {
		score := input.Score.Clone()
		clone.Score = &score
	}
	if input.ScoreProjection != nil {
		projection, projectionErr := cloneOfficialProjectionRevision(*input.ScoreProjection)
		if projectionErr != nil {
			return OfficialResultProjectionInput{}, projectionErr
		}
		clone.ScoreProjection = &projection
	}
	if input.NoGame != nil {
		noGame, noGameErr := cloneRecordedNoGameResult(*input.NoGame)
		if noGameErr != nil {
			return OfficialResultProjectionInput{}, noGameErr
		}
		clone.NoGame = &noGame
	}
	return clone, nil
}

func cloneRecordedNoGameResult(recorded RecordedNoGameResult) (RecordedNoGameResult, error) {
	clone := recorded
	clone.GameResults = append([]domain.NormalNoShowGameRevision(nil), recorded.GameResults...)
	clone.Topology = append([]RecordedNoGameAttempt(nil), recorded.Topology...)
	clone.ReadyParticipantID = cloneUUIDPointer(recorded.ReadyParticipantID)
	for index := range clone.GameResults {
		clone.GameResults[index].PreviousRevisionID = cloneOfficialResultRevisionIDPointer(recorded.GameResults[index].PreviousRevisionID)
	}
	clone.Score.PreviousRevisionID = cloneSeriesScoreRevisionIDPointer(recorded.Score.PreviousRevisionID)
	clone.Score.GameResultRevisionIDs = append([]domain.OfficialResultRevisionID(nil), recorded.Score.GameResultRevisionIDs...)
	clone.Series.PreviousRevisionID = cloneOfficialResultRevisionIDPointer(recorded.Series.PreviousRevisionID)
	clone.Series.WinnerID = cloneUUIDPointer(recorded.Series.WinnerID)
	clone.GameSourceRevisions = append([]domain.DerivedRevision(nil), recorded.GameSourceRevisions...)
	clone.GameDependencies = append([]domain.RevisionDependency(nil), recorded.GameDependencies...)
	clone.GameProjections = make([]domain.ProjectionRevision, len(recorded.GameProjections))
	for index := range recorded.GameProjections {
		projection, err := cloneOfficialProjectionRevision(recorded.GameProjections[index])
		if err != nil {
			return RecordedNoGameResult{}, err
		}
		clone.GameProjections[index] = projection
	}
	var err error
	clone.ScoreProjection, err = cloneOfficialProjectionRevision(recorded.ScoreProjection)
	if err != nil {
		return RecordedNoGameResult{}, err
	}
	clone.ResultProjection, err = cloneOfficialProjectionRevision(recorded.ResultProjection)
	if err != nil {
		return RecordedNoGameResult{}, err
	}
	return clone, nil
}

func cloneOfficialProjectionRevision(
	projection domain.ProjectionRevision,
) (domain.ProjectionRevision, error) {
	revision := projection.Revision()
	return domain.NewProjectionRevision(
		revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
		revision.PreviousRevisionID(), revision.CreatedAt(), projection.Payload(),
	)
}

func clonePublicOfficialResult(value PublicOfficialResult) PublicOfficialResult {
	clone := value
	clone.GameID = cloneUUIDPointer(value.GameID)
	clone.WinnerID = cloneUUIDPointer(value.WinnerID)
	clone.Score = cloneSeriesScorePointer(value.Score)
	return clone
}

func cloneOperatorOfficialResult(value OperatorOfficialResult) OperatorOfficialResult {
	clone := value
	clone.Public = clonePublicOfficialResult(value.Public)
	clone.PreviousResultRevisionID = cloneOfficialResultRevisionIDPointer(value.PreviousResultRevisionID)
	clone.ScoreRevisionID = cloneSeriesScoreRevisionIDPointer(value.ScoreRevisionID)
	clone.ScoreProjectionRevisionID = cloneDerivedRevisionIDPointer(value.ScoreProjectionRevisionID)
	clone.GameState = cloneGameStatePointer(value.GameState)
	clone.GameReason = cloneGameReasonPointer(value.GameReason)
	clone.SeriesState = cloneSeriesStatePointer(value.SeriesState)
	clone.SeriesReason = cloneSeriesReasonPointer(value.SeriesReason)
	clone.Cause = cloneOfficialResultCausePointer(value.Cause)
	clone.NoShowAction = cloneNoShowActionPointer(value.NoShowAction)
	return clone
}

func cloneSeriesScorePointer(value *domain.SeriesScore) *domain.SeriesScore {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneDerivedRevisionIDPointer(value *domain.DerivedRevisionID) *domain.DerivedRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneGameStatePointer(value *domain.GameState) *domain.GameState {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneGameReasonPointer(value *domain.GameResultReason) *domain.GameResultReason {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSeriesStatePointer(value *domain.SeriesState) *domain.SeriesState {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneSeriesReasonPointer(value *domain.SeriesResultReason) *domain.SeriesResultReason {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneOfficialResultCausePointer(value *OfficialResultCause) *OfficialResultCause {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneNoShowActionPointer(value *domain.NormalNoShowAction) *domain.NormalNoShowAction {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
