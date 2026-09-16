package exactdraft

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

var _ playoff.ExactDraftCommittedPlanReader = (*ExactDraftBranchPlanPostgres)(nil)

// LoadCommittedExactDraftPlan exposes the exact-draft owner's already
// validated rehydration path to terminal progression. A missing plan is a
// durable final-stage inconsistency, not a retryable absence.
func LoadCommittedExactDraftPlan(
	r *ExactDraftBranchPlanPostgres,
	ctx context.Context,
	planID uuid.UUID,
) (*assignmentusecase.ExactDraftBranchPlan, error) {
	if r == nil || ctx == nil || planID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	plan, _, err := r.LoadExactDraftBranchActivation(ctx, planID)
	if errors.Is(err, assignmentusecase.ErrExactDraftBranchPlanNotFound) {
		return nil, domain.ErrConflict
	}
	if err != nil {
		return nil, fmt.Errorf("ExactDraftBranchPlanPostgres - load committed plan: %w", err)
	}
	if plan == nil {
		return nil, domain.ErrConflict
	}
	return plan, nil
}

func (r *ExactDraftBranchPlanPostgres) LoadCommittedExactDraftPlan(
	ctx context.Context,
	planID uuid.UUID,
) (*assignmentusecase.ExactDraftBranchPlan, error) {
	return LoadCommittedExactDraftPlan(r, ctx, planID)
}
