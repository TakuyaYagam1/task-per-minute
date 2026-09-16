package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	assignmentadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrAssignmentPlanNotFound = assignmentadapter.ErrAssignmentPlanNotFound
	ErrAssignmentNotFound     = assignmentadapter.ErrAssignmentNotFound
)

// AssignmentPostgres keeps the historical root-package constructor and the
// private transaction bridge used by neighboring postgres capabilities.
type AssignmentPostgres struct {
	tx    *TxManager
	inner *assignmentadapter.AssignmentPostgres
}

type ConservativePlanInput = assignmentadapter.ConservativePlanInput
type ExactPlanInput = assignmentadapter.ExactPlanInput
type AssignmentBranchInput = assignmentadapter.AssignmentBranchInput
type AssignmentEdgeInput = assignmentadapter.AssignmentEdgeInput
type AssignmentPlanRecord = assignmentadapter.AssignmentPlanRecord
type AssignmentBranchRecord = assignmentadapter.AssignmentBranchRecord
type AssignmentEdgeRecord = assignmentadapter.AssignmentEdgeRecord
type TaskReservationRecord = assignmentadapter.TaskReservationRecord
type TaskSnapshotRecord = assignmentadapter.TaskSnapshotRecord
type AssignmentPlanAggregate = assignmentadapter.AssignmentPlanAggregate
type AssignmentCreateInput = assignmentadapter.AssignmentCreateInput
type AssignmentRecord = assignmentadapter.AssignmentRecord
type AssignmentSupersedeInput = assignmentadapter.AssignmentSupersedeInput

func NewAssignmentPostgres(tx *TxManager) *AssignmentPostgres {
	return &AssignmentPostgres{tx: tx, inner: assignmentadapter.NewAssignmentPostgres(tx)}
}

func (r *AssignmentPostgres) repository() *assignmentadapter.AssignmentPostgres {
	if r == nil {
		return nil
	}
	if r.inner == nil {
		r.inner = assignmentadapter.NewAssignmentPostgres(r.tx)
	}
	return r.inner
}

func (r *AssignmentPostgres) CreateConservativePlan(
	ctx context.Context,
	in ConservativePlanInput,
) (*AssignmentPlanAggregate, error) {
	return r.repository().CreateConservativePlan(ctx, in)
}

func (r *AssignmentPostgres) CreateExactPlan(
	ctx context.Context,
	in ExactPlanInput,
) (*AssignmentPlanAggregate, error) {
	return r.repository().CreateExactPlan(ctx, in)
}

func (r *AssignmentPostgres) CommitBranch(
	ctx context.Context,
	planID uuid.UUID,
	expectedRevisionID uuid.UUID,
	expectedRosterRevision int64,
	expectedDraftRevisionID uuid.UUID,
	branchID uuid.UUID,
	committedAt time.Time,
	releaseReason string,
) (*AssignmentPlanAggregate, bool, error) {
	return r.repository().CommitBranch(
		ctx, planID, expectedRevisionID, expectedRosterRevision,
		expectedDraftRevisionID, branchID, committedAt, releaseReason,
	)
}

func (r *AssignmentPostgres) GetPlan(
	ctx context.Context,
	planID uuid.UUID,
) (*AssignmentPlanAggregate, error) {
	return r.repository().GetPlan(ctx, planID)
}

func (r *AssignmentPostgres) CreateAssignment(
	ctx context.Context,
	in AssignmentCreateInput,
) (*AssignmentRecord, error) {
	return r.repository().CreateAssignment(ctx, in)
}

func (r *AssignmentPostgres) Deliver(
	ctx context.Context,
	assignmentID uuid.UUID,
	receiptID uuid.UUID,
	participantID uuid.UUID,
	deliveredAt time.Time,
) (domain.TaskDeliveryReceipt, bool, error) {
	return r.repository().Deliver(ctx, assignmentID, receiptID, participantID, deliveredAt)
}

func (r *AssignmentPostgres) Supersede(
	ctx context.Context,
	assignmentID uuid.UUID,
	expectedRevision int64,
	in AssignmentSupersedeInput,
) (*AssignmentRecord, bool, error) {
	return r.repository().Supersede(ctx, assignmentID, expectedRevision, in)
}

func (r *AssignmentPostgres) GetAssignment(
	ctx context.Context,
	assignmentID uuid.UUID,
) (*AssignmentRecord, error) {
	return r.repository().GetAssignment(ctx, assignmentID)
}

// createAssignmentTx preserves the root package transaction hook used by
// tournament materialization workflows. The child owns its implementation.
func (r *AssignmentPostgres) createAssignmentTx(ctx context.Context, in AssignmentCreateInput) error {
	return r.repository().CreateAssignmentTx(ctx, in)
}
