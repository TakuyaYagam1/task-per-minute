package postgres

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validCorrectionInput(in CorrectionInput) bool {
	if !validCorrectionIdentity(in) || !validCorrectionMetadata(in) ||
		!validCorrectionStates(in) || !validCorrectionGame(in) {
		return false
	}
	return validCorrectionSeries(in)
}

func validCorrectionIdentity(in CorrectionInput) bool {
	return validResultScope(in.Scope) && validResultSettlementIDs(in.IDs) &&
		in.ProjectionIDs.RevisionID != uuid.Nil && in.ProjectionIDs.CutoffID != uuid.Nil &&
		in.SourceRevisionID != uuid.Nil && in.ExpectedProjectionRevisionID != uuid.Nil &&
		in.ExpectedScoreRevisionID != uuid.Nil && in.OperatorID != uuid.Nil
}

func validCorrectionMetadata(in CorrectionInput) bool {
	return in.ExpectedAttemptRevision >= 1 && in.ExpectedScoreRevision >= 1 && in.ExpectedSeriesRevision >= 1 &&
		validServerTime(in.CorrectedAt) && validTrimmedText(in.Reason) &&
		len(in.Reason) <= 512 && !zeroDigest(in.ProjectionPayloadDigest[:]) && len(in.ProjectionArtifacts) > 0
}

func validCorrectionStates(in CorrectionInput) bool {
	return in.ExpectedAttemptState.IsTerminal() && in.GameState.IsTerminal() &&
		in.GameReason.IsLegalFor(in.GameState) && in.ExpectedSeriesState.IsValid() &&
		in.NextSeriesState.IsValid() &&
		in.ExpectedSeriesState.IsTerminal() == in.NextSeriesState.IsTerminal()
}

func validCorrectionGame(in CorrectionInput) bool {
	if in.GameState == domain.GameStateCompleted {
		if in.GameWinnerID == nil || *in.GameWinnerID == uuid.Nil {
			return false
		}
	} else if in.GameWinnerID != nil {
		return false
	}
	if in.GameReason == domain.GameResultReasonSolved && in.SubmissionEventID == uuid.Nil {
		return false
	}
	return true
}

func validCorrectionSeries(in CorrectionInput) bool {
	if in.ExpectedSeriesState.IsTerminal() {
		return in.ExpectedSeriesResultRevisionID != nil && *in.ExpectedSeriesResultRevisionID != uuid.Nil &&
			in.IDs.SeriesResultRevisionID != uuid.Nil && validSeriesResultReason(in.SeriesResultReason)
	}
	return in.ExpectedSeriesResultRevisionID == nil && in.IDs.SeriesResultRevisionID == uuid.Nil &&
		in.SeriesResultReason == "" && in.SeriesWinnerID == nil
}

func correctionSeriesWinnerMatches(
	in CorrectionInput,
	series sqlc.LockResultSeriesRow,
) bool {
	if in.NextSeriesState == domain.SeriesStateCancelled {
		return in.SeriesWinnerID == nil
	}
	if !in.NextSeriesState.IsTerminal() {
		return in.SeriesWinnerID == nil
	}
	winner := in.Score.Winner(series.FirstParticipantID, series.SecondParticipantID, domain.SeriesFormat(series.Format))
	return winner != nil && in.SeriesWinnerID != nil && *winner == *in.SeriesWinnerID
}

func correctionHeads(
	heads []sqlc.OfficialResultHead,
	scope ResultScope,
) (*sqlc.OfficialResultHead, *sqlc.OfficialResultHead) {
	var gameHead *sqlc.OfficialResultHead
	var seriesHead *sqlc.OfficialResultHead
	for i := range heads {
		head := &heads[i]
		switch {
		case head.EntityKind == "game_attempt" && head.EntityID == scope.AttemptID:
			gameHead = head
		case head.EntityKind == "series" && head.EntityID == scope.SeriesID:
			seriesHead = head
		}
	}
	return gameHead, seriesHead
}

func correctionHasNewSource(in CorrectionInput) bool {
	for _, artifact := range in.ProjectionArtifacts {
		for _, dependency := range artifact.Dependencies {
			if dependency.OfficialResultRevisionID != nil &&
				(*dependency.OfficialResultRevisionID == in.IDs.GameResultRevisionID ||
					*dependency.OfficialResultRevisionID == in.IDs.SeriesResultRevisionID) {
				return true
			}
		}
	}
	return false
}

func correctionSourceParams(
	scope ResultScope,
	sourceRevisionID uuid.UUID,
) sqlc.GetCorrectionSourceParams {
	return sqlc.GetCorrectionSourceParams{
		SourceRevisionID: sourceRevisionID, TournamentID: scope.TournamentID,
		RosterID: scope.RosterID, AttemptID: scope.AttemptID, SeriesID: scope.SeriesID,
	}
}

func correctionCutoffParams(
	scope ResultScope,
	sourceRevisionID uuid.UUID,
) sqlc.GetCorrectionCutoffParams {
	return sqlc.GetCorrectionCutoffParams{
		RosterID: scope.RosterID, SeriesID: scope.SeriesID,
		TournamentID: scope.TournamentID, SourceRevisionID: sourceRevisionID,
	}
}

func correctionDescendant(
	artifactID uuid.UUID,
	artifactKind string,
	producedByRevisionID uuid.UUID,
	revisionID uuid.UUID,
	revisionNumber int64,
	revisionState string,
) CorrectionDescendant {
	return CorrectionDescendant{
		ArtifactID: artifactID, ArtifactKind: artifactKind,
		ProducedByRevisionID: producedByRevisionID, RevisionID: revisionID,
		RevisionNumber: revisionNumber, RevisionState: revisionState,
	}
}

func correctionLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrCorrectionNotFound, operation)
	}
	return fmt.Errorf("CorrectionPostgres - %s: %w", operation, err)
}
