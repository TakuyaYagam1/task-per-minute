package postgres

import exactdraft "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/exactdraft"

// ExactDraftBranchPlanPostgres keeps the historical root-package name while
// the implementation lives in the assignment child package.
type ExactDraftBranchPlanPostgres = exactdraft.ExactDraftBranchPlanPostgres

func NewExactDraftBranchPlanPostgres(
	tx *TxManager,
	drafts *DraftPostgres,
) *ExactDraftBranchPlanPostgres {
	return exactdraft.NewExactDraftBranchPlanPostgres(tx, drafts)
}
