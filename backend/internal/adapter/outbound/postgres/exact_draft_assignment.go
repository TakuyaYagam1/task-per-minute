package postgres

import (
	"context"

	"github.com/google/uuid"

	exactdraft "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/exactdraft"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

// ExactDraftBranchPlanPostgres keeps the historical root-package constructor
// while the exact-draft implementation lives in the assignment child package.
type ExactDraftBranchPlanPostgres struct {
	tx     *TxManager
	drafts *DraftPostgres
	inner  *exactdraft.ExactDraftBranchPlanPostgres
}

var _ assignmentusecase.ExactDraftBranchPlanRepository = (*ExactDraftBranchPlanPostgres)(nil)
var _ playoff.ExactDraftPlanAuthorityReader = (*ExactDraftBranchPlanPostgres)(nil)

func NewExactDraftBranchPlanPostgres(
	tx *TxManager,
	drafts *DraftPostgres,
) *ExactDraftBranchPlanPostgres {
	return &ExactDraftBranchPlanPostgres{
		tx: tx, drafts: drafts, inner: exactdraft.NewExactDraftBranchPlanPostgres(tx, drafts),
	}
}

func (r *ExactDraftBranchPlanPostgres) repository() *exactdraft.ExactDraftBranchPlanPostgres {
	if r == nil {
		return nil
	}
	if r.inner == nil {
		r.inner = exactdraft.NewExactDraftBranchPlanPostgres(r.tx, r.drafts)
	}
	return r.inner
}

func (r *ExactDraftBranchPlanPostgres) LoadExactDraftBranchPlanAuthority(
	ctx context.Context,
	draftID uuid.UUID,
) (assignmentusecase.ExactDraftBranchPlanAuthority, error) {
	return r.repository().LoadExactDraftBranchPlanAuthority(ctx, draftID)
}

func (r *ExactDraftBranchPlanPostgres) CommitExactDraftBranchPlan(
	ctx context.Context,
	plan assignmentusecase.ExactDraftBranchPlan,
) (*assignmentusecase.ExactDraftBranchPlan, bool, error) {
	return r.repository().CommitExactDraftBranchPlan(ctx, plan)
}

func (r *ExactDraftBranchPlanPostgres) LoadExactDraftBranchActivation(
	ctx context.Context,
	planID uuid.UUID,
) (*assignmentusecase.ExactDraftBranchPlan, *draftusecase.Execution, error) {
	return r.repository().LoadExactDraftBranchActivation(ctx, planID)
}

func (r *ExactDraftBranchPlanPostgres) CommitExactDraftBranchActivation(
	ctx context.Context,
	next assignmentusecase.ExactDraftBranchPlan,
) (*assignmentusecase.ExactDraftBranchPlan, bool, error) {
	return r.repository().CommitExactDraftBranchActivation(ctx, next)
}
