package result

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// ResultProjectionTargetBinding is the immutable projection revision selected
// for a result outbox event.
type ResultProjectionTargetBinding struct {
	ID       uuid.UUID
	Revision int64
}

func ResultProjectionTarget(source sqlc.LockResultSourceProjectionRow, targetID uuid.UUID) (ResultProjectionTargetBinding, bool) {
	target, ok := resultProjectionTarget(source, targetID)
	if !ok {
		return ResultProjectionTargetBinding{}, false
	}
	return ResultProjectionTargetBinding(target), true
}

// PublishResultProjectionWithFinalizer keeps the immutable projection
// publication in the result transaction and optionally joins the progression
// receipt boundary supplied by the caller.
func PublishResultProjectionWithFinalizer(
	ctx context.Context,
	tx *db.TxManager,
	in ResultSettlementInput,
	source sqlc.LockResultSourceProjectionRow,
	finalizer ProjectionFinalizer,
) error {
	return publishResultProjectionWithFinalizer(ctx, tx, in, source, finalizer)
}

// PublishCommittedResultProjection completes the publication boundary for a
// result-owned projection revision inside the caller's transaction.
func PublishCommittedResultProjection(
	ctx context.Context,
	tx *db.TxManager,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	revisionID uuid.UUID,
	at time.Time,
	finalizer ProjectionFinalizer,
) error {
	return publishCommittedResultProjection(ctx, tx, tournamentID, rosterID, revisionID, at, finalizer)
}

func LoadResultCommit(
	ctx context.Context,
	querier *sqlc.Queries,
	commit sqlc.ResultCommit,
) (*ResultCommitRecord, error) {
	return loadResultCommit(ctx, querier, commit)
}

func ValidResultScope(scope ResultScope) bool {
	return validResultScope(scope)
}

func ValidResultSettlementIDs(value ResultSettlementIDs) bool {
	return validResultSettlementIDs(value)
}

func ValidSeriesResultReason(reason string) bool {
	return validSeriesResultReason(reason)
}

func ResultAttemptParams(scope ResultScope) sqlc.LockResultAttemptParams {
	return resultAttemptParams(scope)
}

func SubmissionEventSequenceParams(scope ResultScope) sqlc.AllocateSubmissionEventSequenceParams {
	return submissionEventSequenceParams(scope)
}

func ResultEventSequenceParams(scope ResultScope) sqlc.AllocateResultEventSequenceParams {
	return resultEventSequenceParams(scope)
}

func ResultLookupError(operation string, err error) error {
	return resultLookupError(operation, err)
}

func ResultCASWriteError(operation string, err error) error {
	return resultCASWriteError(operation, err)
}

type ScoreRevisionAttemptEvidence struct {
	SlotID               uuid.UUID
	SlotPosition         int16
	GameAttemptID        uuid.UUID
	AttemptNumber        int32
	GameResultRevisionID uuid.UUID
	ResultEventID        uuid.UUID
	ResultState          string
	ResultReason         string
	WinnerID             uuid.NullUUID
	OccurredAt           pgtype.Timestamptz
	CreatedAt            pgtype.Timestamptz
}

func CopyScoreRevisionAttempts(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ResultScope,
	newScoreRevisionID uuid.UUID,
	prior []sqlc.SeriesScoreRevisionAttempt,
	excludedGameAttemptIDs []uuid.UUID,
	createdAt pgtype.Timestamptz,
) error {
	return copyScoreRevisionAttempts(ctx, querier, scope, newScoreRevisionID, prior, excludedGameAttemptIDs, createdAt)
}

func CreateScoreRevisionAttempt(
	ctx context.Context,
	querier *sqlc.Queries,
	scope ResultScope,
	scoreRevisionID uuid.UUID,
	position int16,
	evidence ScoreRevisionAttemptEvidence,
) error {
	return createScoreRevisionAttempt(ctx, querier, scope, scoreRevisionID, position, scoreRevisionAttemptEvidence(evidence))
}

func ScoreRevisionAttemptPosition(rows []sqlc.SeriesScoreRevisionAttempt, gameAttemptID uuid.UUID) (int16, bool) {
	return scoreRevisionAttemptPosition(rows, gameAttemptID)
}

func NextScoreRevisionAttemptPosition(rows []sqlc.SeriesScoreRevisionAttempt) (int16, error) {
	return nextScoreRevisionAttemptPosition(rows)
}

func ScoreSlotPosition(value int32) (int16, error) {
	return scoreSlotPosition(value)
}

func NormalNoShowScoreEvidence(
	graph []sqlc.ListRecoverySeriesGraphRow,
	seriesID uuid.UUID,
	revisions []domain.NormalNoShowGameRevision,
	resultEventID uuid.UUID,
	resolvedAt pgtype.Timestamptz,
) ([]ScoreRevisionAttemptEvidence, error) {
	evidence, err := normalNoShowScoreEvidence(graph, seriesID, revisions, resultEventID, resolvedAt)
	if err != nil {
		return nil, err
	}
	result := make([]ScoreRevisionAttemptEvidence, len(evidence))
	for index, item := range evidence {
		result[index] = ScoreRevisionAttemptEvidence(item)
	}
	return result, nil
}

func StageScoreGenesisNode(scope ResultScope, revisionID uuid.UUID, nodes []sqlc.LockStageScoreGenesisNodesRow) (uuid.UUID, error) {
	return stageScoreGenesisNode(scope, revisionID, nodes)
}

func CreateResultProjectionNode(ctx context.Context, querier *sqlc.Queries, input sqlc.CreateResultProjectionNodeParams) error {
	return createResultProjectionNode(ctx, querier, input)
}

func CreateResultProjectionDependency(
	ctx context.Context,
	querier *sqlc.Queries,
	authorityID, sourceID, derivedID uuid.UUID,
	createdAt time.Time,
) error {
	return createResultProjectionDependency(ctx, querier, authorityID, sourceID, derivedID, createdAt)
}

func DigestBytes(payload []byte) []byte {
	return digestBytes(payload)
}

func NullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return nullableUUIDValue(value)
}

func NullableUUID(value *uuid.UUID) uuid.NullUUID {
	return nullableUUID(value)
}

func TSTZ(value time.Time) pgtype.Timestamptz {
	return tstz(value)
}

func OptionalTrimmedString(value string) *string {
	return optionalTrimmedString(value)
}

func MapRepositoryWriteError(operation string, err error) error {
	return mapRepositoryWriteError(operation, err)
}
