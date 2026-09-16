package postgres

import (
	exactdraft "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/exactdraft"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

// ExactDraftBranchPlanPostgres keeps the historical root-package name while
// embedding the implementation owned by the assignment child package. The
// root transaction fields remain for the legacy private helper methods.
type ExactDraftBranchPlanPostgres struct {
	*exactdraft.ExactDraftBranchPlanPostgres
	tx     *TxManager
	drafts *DraftPostgres
}

var _ assignmentusecase.ExactDraftBranchPlanRepository = (*ExactDraftBranchPlanPostgres)(nil)
var _ playoff.ExactDraftPlanAuthorityReader = (*ExactDraftBranchPlanPostgres)(nil)

func NewExactDraftBranchPlanPostgres(
	tx *TxManager,
	drafts *DraftPostgres,
) *ExactDraftBranchPlanPostgres {
	inner := exactdraft.NewExactDraftBranchPlanPostgres(tx, drafts)
	return &ExactDraftBranchPlanPostgres{
		ExactDraftBranchPlanPostgres: inner,
		tx:                           tx,
		drafts:                       drafts,
	}
}

func (r *ExactDraftBranchPlanPostgres) repository() *exactdraft.ExactDraftBranchPlanPostgres {
	if r == nil {
		return nil
	}
	if r.ExactDraftBranchPlanPostgres == nil {
		r.ExactDraftBranchPlanPostgres = exactdraft.NewExactDraftBranchPlanPostgres(r.tx, r.drafts)
	}
	return r.ExactDraftBranchPlanPostgres
}
