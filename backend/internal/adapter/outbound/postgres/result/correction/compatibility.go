package correction

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	projectionpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	resultpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type ResultSettlementIDs = resultpostgres.ResultSettlementIDs
type ResultScope = resultpostgres.ResultScope
type ResultCommitRecord = resultpostgres.ResultCommitRecord

type ProjectionIDs = projectionpostgres.ProjectionIDs
type ProjectionScope = projectionpostgres.ProjectionScope
type ProjectionSource = projectionpostgres.ProjectionSource
type ProjectionArtifactInput = projectionpostgres.ProjectionArtifactInput
type ProjectionPublishInput = projectionpostgres.ProjectionPublishInput
type ProjectionRecord = projectionpostgres.ProjectionRecord

const (
	resultActorOperator             = "operator"
	projectionSourceOperatorRebuild = "operator_rebuild"
)

func validResultScope(scope ResultScope) bool {
	return resultpostgres.ValidResultScope(scope)
}

func validResultSettlementIDs(value ResultSettlementIDs) bool {
	return resultpostgres.ValidResultSettlementIDs(value)
}

func validSeriesResultReason(reason string) bool {
	return resultpostgres.ValidSeriesResultReason(reason)
}

func resultAttemptParams(scope ResultScope) sqlc.LockResultAttemptParams {
	return resultpostgres.ResultAttemptParams(scope)
}

type scoreRevisionAttemptEvidence = resultpostgres.ScoreRevisionAttemptEvidence

func scoreRevisionAttemptPosition(rows []sqlc.SeriesScoreRevisionAttempt, gameAttemptID uuid.UUID) (int16, bool) {
	return resultpostgres.ScoreRevisionAttemptPosition(rows, gameAttemptID)
}

func scoreSlotPosition(value int32) (int16, error) {
	return resultpostgres.ScoreSlotPosition(value)
}

func copyScoreRevisionAttempts(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ResultScope,
	newScoreRevisionID uuid.UUID,
	prior []sqlc.SeriesScoreRevisionAttempt,
	excludedGameAttemptIDs []uuid.UUID,
	createdAt pgtype.Timestamptz,
) error {
	return resultpostgres.CopyScoreRevisionAttempts(
		ctx, querier, scope, newScoreRevisionID, prior, excludedGameAttemptIDs, createdAt,
	)
}

func createScoreRevisionAttempt(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ResultScope,
	scoreRevisionID uuid.UUID,
	position int16,
	evidence scoreRevisionAttemptEvidence,
) error {
	return resultpostgres.CreateScoreRevisionAttempt(
		ctx, querier, scope, scoreRevisionID, position,
		resultpostgres.ScoreRevisionAttemptEvidence(evidence),
	)
}

func loadResultCommit(
	ctx context.Context,
	querier *sqlc.Queries,
	commit sqlc.ResultCommit,
) (*ResultCommitRecord, error) {
	return resultpostgres.LoadResultCommit(ctx, querier, commit)
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

func zeroDigest(value []byte) bool {
	return len(value) != 32 || bytes.Equal(value, make([]byte, 32))
}

func mapRepositoryWriteError(operation string, err error) error {
	return resultpostgres.MapRepositoryWriteError(operation, err)
}

func resultCASWriteError(operation string, err error) error {
	return resultpostgres.ResultCASWriteError(operation, err)
}

func marshalJSON(operation string, value any) ([]byte, error) {
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.Slice && reflected.IsNil() {
		return []byte("[]"), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%s - marshal JSON: %w", operation, err)
	}
	return data, nil
}

func createProjectionArtifact(
	ctx context.Context,
	querier *sqlc.Queries,
	in ProjectionPublishInput,
	artifact ProjectionArtifactInput,
) error {
	return projectionpostgres.CreateProjectionArtifact(ctx, querier, in, artifact)
}

func loadProjectionRecord(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ProjectionScope,
	revisionID uuid.UUID,
) (*ProjectionRecord, error) {
	return projectionpostgres.LoadProjectionRecord(ctx, querier, scope, revisionID)
}

func validProjectionArtifact(artifact ProjectionArtifactInput) bool {
	return projectionpostgres.ValidateProjectionArtifact(artifact)
}

func projectionArtifactKind(kind domain.ArtifactKind) string {
	return projectionpostgres.ProjectionArtifactKind(kind)
}

func projectionCASWriteError(operation string, err error) error {
	return projectionpostgres.ProjectionCASWriteError(operation, err)
}

// RebuildLocked is the transaction-owned correction boundary. The caller must
// already have opened the transaction and established the surrounding lock
// order before invoking it.
func (r *CorrectionPostgres) RebuildLocked(
	ctx context.Context,
	in CorrectionInput,
) (*CorrectionRecord, error) {
	return r.rebuildLocked(ctx, in)
}
