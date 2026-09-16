package assignment

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (r *AssignmentPostgres) GetPlan(
	ctx context.Context,
	planID uuid.UUID,
) (*AssignmentPlanAggregate, error) {
	if planID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	plan, err := querier.GetAssignmentPlan(ctx, planID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAssignmentPlanNotFound
		}
		return nil, fmt.Errorf("AssignmentPostgres - GetPlan: %w", err)
	}
	branches, err := querier.ListAssignmentBranches(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("AssignmentPostgres - GetPlan - branches: %w", err)
	}
	edges, err := querier.ListAssignmentPlanEdges(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("AssignmentPostgres - GetPlan - edges: %w", err)
	}
	reservations, err := querier.ListAssignmentTaskVersionReservations(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("AssignmentPostgres - GetPlan - reservations: %w", err)
	}
	snapshots, err := querier.ListAssignmentTaskSnapshotsForPlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("AssignmentPostgres - GetPlan - snapshots: %w", err)
	}
	return assignmentPlanAggregate(plan, branches, edges, reservations, snapshots)
}
