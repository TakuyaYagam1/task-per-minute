package postgres

import (
	"context"

	"github.com/google/uuid"

	assignmentadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func createReplayReserveAuthorityTx(
	ctx context.Context,
	querier *sqlc.Queries,
	assignmentID uuid.UUID,
) error {
	return assignmentadapter.CreateReplayReserveAuthorityTx(ctx, querier, assignmentID)
}

func matchesAssignmentPlan(plan sqlc.LockAssignmentPlanRow, in AssignmentCreateInput) bool {
	return assignmentadapter.MatchesAssignmentPlan(plan, in)
}

func matchesExactDraftAssignmentPlan(
	plan sqlc.LockAssignmentPlanRow,
	in AssignmentCreateInput,
	children []sqlc.LockAssignmentDraftChildScopeRow,
) bool {
	return assignmentadapter.MatchesExactDraftAssignmentPlan(plan, in, children)
}
