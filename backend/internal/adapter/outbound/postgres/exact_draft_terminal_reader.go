package postgres

import (
	"context"

	"github.com/google/uuid"

	exactdraft "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/exactdraft"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

var _ playoff.ExactDraftCommittedPlanReader = (*ExactDraftBranchPlanPostgres)(nil)

func (r *ExactDraftBranchPlanPostgres) LoadCommittedExactDraftPlan(
	ctx context.Context,
	planID uuid.UUID,
) (*assignmentusecase.ExactDraftBranchPlan, error) {
	return exactdraft.LoadCommittedExactDraftPlan(r.repository(), ctx, planID)
}
