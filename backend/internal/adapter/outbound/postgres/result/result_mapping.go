package result

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func loadResultCommit(
	ctx context.Context,
	querier *sqlc.Queries,
	commit sqlc.ResultCommit,
) (*ResultCommitRecord, error) {
	event, err := querier.GetResultEventByID(ctx, commit.ResultEventID)
	if err != nil {
		return nil, fmt.Errorf("ResultPostgres - load commit - event: %w", err)
	}
	gameRevision, err := querier.GetOfficialResultRevisionByID(ctx, commit.GameResultRevisionID)
	if err != nil {
		return nil, fmt.Errorf("ResultPostgres - load commit - Game revision: %w", err)
	}
	scoreRevision, err := querier.GetSeriesScoreRevisionByID(ctx, commit.SeriesScoreRevisionID)
	if err != nil {
		return nil, fmt.Errorf("ResultPostgres - load commit - score revision: %w", err)
	}
	var seriesRevision *sqlc.OfficialResultRevision
	if commit.SeriesResultRevisionID.Valid {
		row, loadErr := querier.GetOfficialResultRevisionByID(ctx, commit.SeriesResultRevisionID.UUID)
		if loadErr != nil {
			return nil, fmt.Errorf("ResultPostgres - load commit - Series revision: %w", loadErr)
		}
		seriesRevision = &row
	}
	audit, err := querier.GetResultAuditEventByID(ctx, commit.AuditEventID)
	if err != nil {
		return nil, fmt.Errorf("ResultPostgres - load commit - audit: %w", err)
	}
	outbox, err := querier.GetResultOutboxEventByID(ctx, commit.OutboxEventID)
	if err != nil {
		return nil, fmt.Errorf("ResultPostgres - load commit - outbox: %w", err)
	}
	projection, err := querier.GetResultProjectionEvidenceByID(ctx, commit.ProjectionEvidenceID)
	if err != nil {
		return nil, fmt.Errorf("ResultPostgres - load commit - projection: %w", err)
	}
	return &ResultCommitRecord{
		Commit: commit, Event: event, GameRevision: gameRevision, ScoreRevision: scoreRevision,
		SeriesRevision: seriesRevision, Audit: audit, Outbox: outbox, ProjectionEvidence: projection,
	}, nil
}

func validateSettlementAgainstRows(
	in ResultSettlementInput,
	attempt sqlc.LockResultAttemptRow,
	series sqlc.LockResultSeriesRow,
) error {
	if series.Revision != in.ExpectedSeriesRevision || series.State != string(in.ExpectedSeriesState) ||
		!series.CurrentScoreRevisionID.Valid || series.CurrentScoreRevisionID.UUID != series.ScoreHeadRevisionID {
		return domain.ErrConflict
	}
	if err := in.Score.Validate(domain.SeriesFormat(series.Format)); err != nil {
		return domain.WrapError(err, domain.ErrValidation)
	}
	game := domain.Game{
		ID: attempt.ID, SlotID: attempt.SlotID, AttemptNo: int(attempt.AttemptNumber),
		State: in.GameState, ResultReason: in.GameReason, WinnerID: in.GameWinnerID,
	}
	resultRevisionID := domain.OfficialResultRevisionID(in.IDs.GameResultRevisionID)
	game.ResultRevisionID = &resultRevisionID
	if err := game.Validate(); err != nil {
		return domain.WrapError(err, domain.ErrValidation)
	}
	if in.GameWinnerID != nil && *in.GameWinnerID != series.FirstParticipantID &&
		*in.GameWinnerID != series.SecondParticipantID {
		return domain.ErrValidation
	}
	return validateSeriesSettlement(in, series)
}

func validateSeriesSettlement(in ResultSettlementInput, series sqlc.LockResultSeriesRow) error {
	if in.NextSeriesState.IsTerminal() {
		if in.IDs.SeriesResultRevisionID == uuid.Nil || !validSeriesResultReason(in.SeriesResultReason) {
			return domain.ErrValidation
		}
		if in.NextSeriesState == domain.SeriesStateCompleted {
			expected := in.Score.Winner(series.FirstParticipantID, series.SecondParticipantID, domain.SeriesFormat(series.Format))
			if expected == nil || in.SeriesWinnerID == nil || *expected != *in.SeriesWinnerID {
				return domain.ErrValidation
			}
		} else if in.SeriesWinnerID != nil && *in.SeriesWinnerID != series.FirstParticipantID &&
			*in.SeriesWinnerID != series.SecondParticipantID {
			return domain.ErrValidation
		}
		return nil
	}
	if in.IDs.SeriesResultRevisionID != uuid.Nil || in.SeriesResultReason != "" || in.SeriesWinnerID != nil {
		return domain.ErrValidation
	}
	return nil
}

func validResultSettlementInput(in ResultSettlementInput) bool {
	if !validResultSettlementIDs(in.IDs) || !validResultSettlementMetadata(in) {
		return false
	}
	if in.GameReason == domain.GameResultReasonSolved && in.SubmissionEventID == uuid.Nil {
		return false
	}
	if in.ActorKind == resultActorServer {
		return in.ActorID == nil
	}
	return in.ActorKind == resultActorOperator && in.ActorID != nil && *in.ActorID != uuid.Nil
}

func validResultSettlementIDs(value ResultSettlementIDs) bool {
	required := []uuid.UUID{
		value.CommitID, value.ResultEventID, value.ResultEventIdempotencyKey,
		value.GameResultRevisionID, value.SeriesScoreRevisionID, value.AuditEventID,
		value.OutboxEventID, value.OutboxIdempotencyKey, value.ProjectionEvidenceID,
		value.CommitIdempotencyKey,
	}
	for _, id := range required {
		if id == uuid.Nil {
			return false
		}
	}
	return true
}

func validResultSettlementMetadata(in ResultSettlementInput) bool {
	if !validResultScope(in.Scope) || !validServerTime(in.SettledAt) ||
		in.ExpectedAttemptRevision < 1 || in.ExpectedSeriesRevision < 1 ||
		!in.ExpectedAttemptState.IsValid() || in.ExpectedAttemptState.IsTerminal() ||
		!in.ExpectedSeriesState.IsValid() || in.ExpectedSeriesState.IsTerminal() ||
		!in.GameState.IsTerminal() || !in.NextSeriesState.IsValid() ||
		zeroDigest(in.ProjectionPayloadDigest[:]) || !validArtifactKinds(in.ProjectionArtifactKinds) {
		return false
	}
	switch in.ProjectionPublication {
	case resultProjectionPublicationImmediate, resultProjectionPublicationCallerOwned:
		return true
	default:
		return false
	}
}

func validSubmissionInput(in SubmissionInput) bool {
	if !validSubmissionMetadata(in) {
		return false
	}
	switch in.Status {
	case submissionStatusAccepted:
		return in.DecisionReason == ""
	case submissionStatusRejected:
		return validTrimmedText(in.DecisionReason)
	default:
		return false
	}
}

func validSubmissionMetadata(in SubmissionInput) bool {
	return validResultScope(in.Scope) && in.ID != uuid.Nil && in.AssignmentID != uuid.Nil &&
		in.ParticipantID != uuid.Nil && in.IdempotencyKey != uuid.Nil &&
		in.ExpectedAttemptRevision >= 1 && in.ExpectedAttemptState.IsValid() &&
		!in.ExpectedAttemptState.IsTerminal() && !zeroDigest(in.PayloadDigest[:]) &&
		!zeroDigest(in.IntentDigest[:]) &&
		validServerTime(in.SubmittedAt) && validServerTime(in.ReceivedAt) && validServerTime(in.CreatedAt) &&
		!in.SubmittedAt.After(in.ReceivedAt) && !in.ReceivedAt.After(in.CreatedAt)
}

func validResultScope(scope ResultScope) bool {
	return scope.TournamentID != uuid.Nil && scope.RosterID != uuid.Nil &&
		scope.SeriesID != uuid.Nil && scope.AttemptID != uuid.Nil
}

func validArtifactKinds(kinds []domain.ArtifactKind) bool {
	if len(kinds) == 0 {
		return false
	}
	seen := make(map[domain.ArtifactKind]struct{}, len(kinds))
	for _, kind := range kinds {
		if !kind.IsValid() {
			return false
		}
		if _, exists := seen[kind]; exists {
			return false
		}
		seen[kind] = struct{}{}
	}
	return true
}

func validSeriesResultReason(reason string) bool {
	switch reason {
	case "score_complete", "operator_correction", "series_cancelled", "tournament_cancelled":
		return true
	default:
		return false
	}
}

func resultAttemptParams(scope ResultScope) sqlc.LockResultAttemptParams {
	return sqlc.LockResultAttemptParams{
		AttemptID: scope.AttemptID, SeriesID: scope.SeriesID,
		RosterID: scope.RosterID, TournamentID: scope.TournamentID,
	}
}

func submissionEventSequenceParams(scope ResultScope) sqlc.AllocateSubmissionEventSequenceParams {
	return sqlc.AllocateSubmissionEventSequenceParams{
		AttemptID:    scope.AttemptID,
		SeriesID:     scope.SeriesID,
		RosterID:     scope.RosterID,
		TournamentID: scope.TournamentID,
	}
}

func resultEventSequenceParams(scope ResultScope) sqlc.AllocateResultEventSequenceParams {
	return sqlc.AllocateResultEventSequenceParams{
		AttemptID:    scope.AttemptID,
		SeriesID:     scope.SeriesID,
		RosterID:     scope.RosterID,
		TournamentID: scope.TournamentID,
	}
}

func submissionMatchesInput(row sqlc.SubmissionEvent, in SubmissionInput) bool {
	return row.ID == in.ID && row.TournamentID == in.Scope.TournamentID && row.RosterID == in.Scope.RosterID &&
		row.SeriesID == in.Scope.SeriesID && row.AttemptID == in.Scope.AttemptID &&
		row.AssignmentID == in.AssignmentID && row.ParticipantID == in.ParticipantID &&
		row.Status == in.Status &&
		stringValue(row.DecisionReason) == in.DecisionReason && bytes.Equal(row.PayloadDigest, in.PayloadDigest[:]) &&
		bytes.Equal(row.IntentDigest, in.IntentDigest[:]) &&
		row.SubmittedAt.Time.Equal(in.SubmittedAt) && row.ReceivedAt.Time.Equal(in.ReceivedAt) &&
		row.CreatedAt.Time.Equal(in.CreatedAt)
}

func submissionRecord(row sqlc.SubmissionEvent) *SubmissionRecord {
	record := &SubmissionRecord{
		ID: row.ID,
		Scope: ResultScope{
			TournamentID: row.TournamentID, RosterID: row.RosterID,
			SeriesID: row.SeriesID, AttemptID: row.AttemptID,
		},
		AssignmentID: row.AssignmentID, ParticipantID: row.ParticipantID,
		ServerSequence: row.ServerSequence, IdempotencyKey: row.IdempotencyKey,
		Status: row.Status, DecisionReason: stringValue(row.DecisionReason),
		SubmittedAt: row.SubmittedAt.Time, ReceivedAt: row.ReceivedAt.Time, CreatedAt: row.CreatedAt.Time,
	}
	copy(record.PayloadDigest[:], row.PayloadDigest)
	copy(record.IntentDigest[:], row.IntentDigest)
	return record
}

func resultHistoryRecord(row sqlc.ListResultHistoryRow) ResultHistoryRecord {
	record := ResultHistoryRecord{
		CommitID: row.CommitID,
		Scope: ResultScope{
			TournamentID: row.TournamentID, RosterID: row.RosterID,
			SeriesID: row.SeriesID, AttemptID: row.AttemptID,
		},
		ResultEventID: row.ResultEventID, ServerSequence: row.ServerSequence,
		GameState: domain.GameState(row.ResultState), GameReason: domain.GameResultReason(row.ResultReason),
		GameRevisionID: row.GameRevisionID, GameRevisionNumber: row.GameRevisionNumber,
		ScoreRevisionID: row.ScoreRevisionID, ScoreRevisionNumber: row.ScoreRevisionNumber,
		Score: domain.SeriesScore{
			FirstParticipantWins:  int(row.FirstParticipantWins),
			SecondParticipantWins: int(row.SecondParticipantWins),
		},
		CreatedAt: row.CreatedAt.Time,
	}
	if row.WinnerID.Valid {
		winnerID := row.WinnerID.UUID
		record.WinnerID = &winnerID
	}
	if row.SeriesResultRevisionID.Valid {
		resultID := row.SeriesResultRevisionID.UUID
		record.SeriesResultRevisionID = &resultID
	}
	return record
}

func resultLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrResultNotFound
	}
	return fmt.Errorf("ResultPostgres - %s: %w", operation, err)
}

func resultCASWriteError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return mapRepositoryWriteError("ResultPostgres - Settle - "+operation, err)
}
