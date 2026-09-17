package correction

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/google/uuid"

	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
	operationusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
)

type OperatorIdentity = operationusecase.OperatorIdentity
type RevisionConflictError = operationusecase.RevisionConflictError
type CommandScope = operationusecase.CommandScope

// CorrectionWorkflowAuthority is the exact database snapshot used to plan one
// operator correction. Core contains the immutable result DAG and its current
// heads; the remaining fields bind that snapshot to the published projection.
type CorrectionWorkflowAuthority struct {
	RosterID             uuid.UUID
	ProjectionRevisionID uuid.UUID
	ProjectionRevision   int64
	Core                 correctionusecase.Authority
	Stage                correctionusecase.StageSnapshot
}

// CorrectionCommandRecord is the durable idempotency record for an already
// committed correction. Evidence is returned verbatim on an exact replay.
type CorrectionCommandRecord struct {
	CommandID                  uuid.UUID
	TournamentID               uuid.UUID
	RosterID                   uuid.UUID
	SeriesID                   uuid.UUID
	GameID                     uuid.UUID
	OperatorID                 uuid.UUID
	ExpectedProjectionRevision int64
	RequestDigest              [sha256.Size]byte
	Evidence                   CorrectionEvidence
	ExecutedAt                 time.Time
}

// CorrectionMutation carries the validated core plan and the use case
// evidence which must be persisted in the same transaction as every CAS write.
type CorrectionMutation struct {
	Command       CorrectionCommand
	Authority     CorrectionWorkflowAuthority
	RequestDigest [sha256.Size]byte
	Plan          correctionusecase.Plan
	Stage         correctionusecase.StageResult
	Evidence      CorrectionEvidence
}

type CorrectionTransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

type CorrectionWorkflowRepository interface {
	LockCorrectionAuthority(
		ctx context.Context,
		tournamentID uuid.UUID,
		seriesID uuid.UUID,
		gameID uuid.UUID,
	) (CorrectionWorkflowAuthority, error)
	FindCorrectionCommand(
		ctx context.Context,
		commandID uuid.UUID,
	) (*CorrectionCommandRecord, error)
	ReadCorrectionTime(ctx context.Context) (time.Time, error)
	CommitCorrection(
		ctx context.Context,
		mutation CorrectionMutation,
	) (CorrectionEvidence, bool, error)
}

type CorrectionWorkflowDependencies struct {
	Transactions CorrectionTransactionManager
	Repository   CorrectionWorkflowRepository
}
