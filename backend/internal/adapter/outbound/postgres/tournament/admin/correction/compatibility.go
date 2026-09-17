package correction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	projectionpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	resultcorrection "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// These aliases keep the moved workflow's storage vocabulary local to the
// capability package while the lower-level result and projection owners are
// migrated independently.
type TxManager = db.TxManager

type CorrectionPostgres = resultcorrection.CorrectionPostgres
type CorrectionInput = resultcorrection.CorrectionInput
type CorrectionCutoffError = resultcorrection.CorrectionCutoffError

type ResultScope = resultpostgres.ResultScope
type ResultSettlementIDs = resultpostgres.ResultSettlementIDs

type ProjectionIDs = projectionpostgres.ProjectionIDs
type ProjectionScope = projectionpostgres.ProjectionScope
type ProjectionSource = projectionpostgres.ProjectionSource
type ProjectionMemberInput = projectionpostgres.ProjectionMemberInput
type ProjectionDependencyInput = projectionpostgres.ProjectionDependencyInput
type ProjectionArtifactInput = projectionpostgres.ProjectionArtifactInput
type ProjectionArtifactRecord = projectionpostgres.ProjectionArtifactRecord
type ProjectionPublishInput = projectionpostgres.ProjectionPublishInput
type ProjectionRecord = projectionpostgres.ProjectionRecord

const (
	projectionSourceOperatorRebuild    = "operator_rebuild"
	projectionDependencyArtifact       = "artifact"
	projectionDependencyOfficialResult = "official_result"
	projectionDependencyGoldenPosition = "golden_position"
)

func NewCorrectionPostgres(tx *db.TxManager) *CorrectionPostgres {
	return resultcorrection.NewCorrectionPostgres(tx)
}

func lockCorrectionScope(ctx context.Context, querier *sqlc.Queries, scope ResultScope) error {
	if _, err := querier.LockCorrectionTournamentScope(ctx, sqlc.LockCorrectionTournamentScopeParams{
		TournamentID: scope.TournamentID,
		RosterID:     scope.RosterID,
	}); err != nil {
		return correctionLookupError("lock Tournament scope", err)
	}
	// Keep the canonical lock prefix identical to the result correction
	// workflow: Tournament -> Roster -> Projection -> Series children.
	if _, err := querier.LockProjectionRevisionSet(ctx, sqlc.LockProjectionRevisionSetParams{
		TournamentID: scope.TournamentID,
		RosterID:     scope.RosterID,
	}); err != nil {
		return fmt.Errorf("CorrectionPostgres - lock projection revisions: %w", err)
	}
	if _, err := querier.LockCorrectionSeriesAttempts(ctx, sqlc.LockCorrectionSeriesAttemptsParams{
		SeriesID: scope.SeriesID, TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	}); err != nil {
		return fmt.Errorf("CorrectionPostgres - lock attempts: %w", err)
	}
	if _, err := querier.LockCorrectionCutoffWaves(ctx, sqlc.LockCorrectionCutoffWavesParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	}); err != nil {
		return fmt.Errorf("CorrectionPostgres - lock Waves: %w", err)
	}
	if _, err := querier.LockCorrectionAssignments(ctx, sqlc.LockCorrectionAssignmentsParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	}); err != nil {
		return fmt.Errorf("CorrectionPostgres - lock assignments: %w", err)
	}
	if _, err := querier.LockCorrectionGoldenAttempts(ctx, sqlc.LockCorrectionGoldenAttemptsParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	}); err != nil {
		return fmt.Errorf("CorrectionPostgres - lock Golden: %w", err)
	}
	return nil
}

func correctionLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", resultcorrection.ErrCorrectionNotFound, operation)
	}
	return fmt.Errorf("CorrectionPostgres - %s: %w", operation, err)
}

func correctionCutoffParams(scope ResultScope, sourceRevisionID uuid.UUID) sqlc.GetCorrectionCutoffParams {
	return sqlc.GetCorrectionCutoffParams{
		RosterID: scope.RosterID, SeriesID: scope.SeriesID,
		TournamentID: scope.TournamentID, SourceRevisionID: sourceRevisionID,
	}
}

func resultAttemptParams(scope ResultScope) sqlc.LockResultAttemptParams {
	return resultpostgres.ResultAttemptParams(scope)
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return resultpostgres.NullableUUIDValue(value)
}

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	return resultpostgres.NullableUUID(value)
}

func tstz(value time.Time) pgtype.Timestamptz {
	return resultpostgres.TSTZ(value)
}

func optionalTrimmedString(value string) *string {
	return resultpostgres.OptionalTrimmedString(value)
}

func validServerTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func validTrimmedText(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

func validSeriesResultReason(reason string) bool {
	return resultpostgres.ValidSeriesResultReason(reason)
}

func validCorrectionInput(in CorrectionInput) bool {
	return validCorrectionIdentity(in) && validCorrectionMetadata(in) &&
		validCorrectionStates(in) && validCorrectionGame(in) && validCorrectionSeries(in)
}

func validCorrectionIdentity(in CorrectionInput) bool {
	return resultpostgres.ValidResultScope(in.Scope) && resultpostgres.ValidResultSettlementIDs(in.IDs) &&
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

func zeroDigest(value []byte) bool {
	if len(value) != 32 {
		return true
	}
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func mapRepositoryWriteError(operation string, err error) error {
	return resultpostgres.MapRepositoryWriteError(operation, err)
}
