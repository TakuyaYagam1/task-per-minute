package postgres

import assignmentadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"

var (
	ErrAssignmentPlanNotFound = assignmentadapter.ErrAssignmentPlanNotFound
	ErrAssignmentNotFound     = assignmentadapter.ErrAssignmentNotFound
)

// AssignmentPostgres keeps the historical root-package name while the
// implementation lives in the assignment capability package.
type AssignmentPostgres = assignmentadapter.AssignmentPostgres

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
	return assignmentadapter.NewAssignmentPostgres(tx)
}
