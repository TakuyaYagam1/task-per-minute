package game

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func buildForfeitResolution(
	authority ForfeitAuthority,
	request forfeitRequest,
	resolvedAt time.Time,
) (ForfeitResolution, error) {
	winnerID, ok := opposingSeriesParticipant(
		authority.Series.Series,
		request.forfeitingParticipantID,
	)
	if !ok {
		return ForfeitResolution{}, ErrForfeitUnavailable
	}
	working := seriesdomain.CloneExecution(authority.Series)
	reason := request.source.ResultReason()
	game, gameRevision, err := applyForfeitGame(
		&working,
		request,
		winnerID,
		reason,
		authority.CurrentOrdinal+1,
		resolvedAt,
	)
	if err != nil {
		return ForfeitResolution{}, err
	}

	scoreBefore := working.Series.Score
	scoreAfter := forfeitWinningScore(working.Series, winnerID)
	scoreRevisionID := request.revisions.ScoreRevisionID
	seriesResultRevisionID := request.revisions.SeriesResultRevisionID
	seriesResultOrdinal := authority.CurrentSeriesResultOrdinal + 1
	terminal, changed, err := seriesdomain.ResolveCompetitive(working, seriesdomain.ResolutionCommand{
		Route: request.source.SeriesRoute(),
		Terminal: &seriesdomain.TerminalEvidence{
			Score: scoreAfter, WinnerID: &winnerID,
			ScoreRevisionID: &scoreRevisionID, ResultRevisionID: &seriesResultRevisionID,
		},
	})
	if err != nil || !changed {
		return ForfeitResolution{}, forfeitError("complete Series: %v", err)
	}
	gameResultRevisionIDs := append(
		[]domain.OfficialResultRevisionID(nil),
		authority.CurrentGameResultRevisionIDs...,
	)
	gameRevisionCount := 0
	if gameRevision != nil {
		gameResultRevisionIDs = append(gameResultRevisionIDs, gameRevision.ID)
		gameRevisionCount = 1
	}
	scoreOrdinal := authority.CurrentOrdinal + gameRevisionCount + 1
	resolution := ForfeitResolution{
		Source: request.source, Reason: reason, Scope: request.scope,
		CommandID: request.commandID, ActorID: request.actorID,
		ForfeitingParticipantID:   request.forfeitingParticipantID,
		ExpectedGame:              cloneForfeitGameExpectation(request.expectedGame),
		ExpectedAuthorityRevision: authority.Revision,
		Series:                    terminal, Game: game, GameRevision: gameRevision,
		ScoreRevision: seriesdomain.ScoreRevision{
			ID: request.revisions.ScoreRevisionID, SeriesID: request.scope.SeriesID,
			FirstParticipantID:  authority.Series.Series.FirstParticipantID,
			SecondParticipantID: authority.Series.Series.SecondParticipantID,
			PreviousRevisionID: forfeitCloneSeriesScoreRevisionIDPointer(
				authority.Series.Series.CurrentScoreRevisionID,
			),
			Ordinal: scoreOrdinal, Format: authority.Series.Series.Format,
			ScoreBefore: scoreBefore, ScoreAfter: scoreAfter,
			GameResultRevisionIDs: gameResultRevisionIDs, RecordedAt: resolvedAt,
		},
		SeriesRevision: SeriesRevision{
			Ordinal: seriesResultOrdinal, ID: request.revisions.SeriesResultRevisionID,
			SeriesID: request.scope.SeriesID,
			PreviousRevisionID: forfeitCloneOfficialResultRevisionIDPointer(
				authority.Series.Series.CurrentResultRevisionID,
			),
			State: domain.SeriesStateCompleted, WinnerID: &winnerID,
			ScoreRevisionID: request.revisions.ScoreRevisionID, Reason: reason, RecordedAt: resolvedAt,
		},
		OperatorEvidence: cloneOperatorForfeitEvidencePointer(request.operatorEvidence),
		Evidence: seriesdomain.SettlementEvidence{
			AuditEventID:             request.revisions.AuditEventID,
			OutboxEventID:            request.revisions.OutboxEventID,
			ProjectionRevisionID:     request.revisions.ProjectionRevisionID,
			SourceProjectionRevision: authority.CurrentProjectionRevision,
			ProjectionRevision:       authority.CurrentProjectionRevision + 1,
			RecordedAt:               resolvedAt,
		},
		ResolvedAt: resolvedAt,
	}
	if err := resolution.Validate(); err != nil {
		return ForfeitResolution{}, err
	}
	return cloneForfeitResolution(resolution), nil
}

func applyForfeitGame(
	series *seriesdomain.Execution,
	request forfeitRequest,
	winnerID uuid.UUID,
	reason domain.GameResultReason,
	ordinal int,
	resolvedAt time.Time,
) (*domain.Game, *GameRevision, error) {
	if request.revisions.GameResultRevisionID == nil {
		return nil, nil, nil
	}
	game, slotIndex, found := currentForfeitGame(series.Series)
	if !found || request.expectedGame == nil ||
		!forfeitGameMatchesExpectation(game, *request.expectedGame) {
		return nil, nil, ErrForfeitAuthorityConflict
	}
	resultRevisionID := *request.revisions.GameResultRevisionID
	transitioned, changed, err := gamedomain.TransitionSlotAttempt(
		series.Series.Slots[slotIndex],
		gamedomain.TransitionCommand{
			GameID: game.ID, ExpectedAttemptNo: game.AttemptNo, ExpectedState: game.State,
			NextState: domain.GameStateCompleted,
			Terminal: &gamedomain.TerminalEvidence{
				Reason: reason, WinnerID: &winnerID, ResultRevisionID: &resultRevisionID,
			},
		},
	)
	if err != nil || !changed {
		return nil, nil, forfeitError("complete Game: %v", err)
	}
	series.Series.Slots[slotIndex] = transitioned
	completed := forfeitCloneGame(transitioned.Attempts[len(transitioned.Attempts)-1])
	revision := GameRevision{
		Ordinal: ordinal, ID: resultRevisionID, GameID: completed.ID,
		WinnerID: winnerID, Reason: reason, RecordedAt: resolvedAt,
	}
	return &completed, &revision, nil
}
