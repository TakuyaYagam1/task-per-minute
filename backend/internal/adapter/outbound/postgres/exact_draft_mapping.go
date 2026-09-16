package postgres

import (
	exactdraft "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/exactdraft"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
)

func exactDraftPlanHasCandidates(
	plan assignmentusecase.ExactDraftBranchPlan,
	candidates []assignmentusecase.ExactNormalTaskVersion,
) bool {
	return exactdraft.ExactDraftPlanHasCandidates(plan, candidates)
}

func validateExactDraftPlanCandidates(
	plan assignmentusecase.ExactDraftBranchPlan,
	candidates []assignmentusecase.ExactNormalTaskVersion,
	exclusions ...[]domain.TaskVersionRef,
) error {
	return exactdraft.ValidateExactDraftPlanCandidates(plan, candidates, exclusions...)
}

func activeExactDraftBranch(
	plan assignmentusecase.ExactDraftBranchPlan,
) (assignmentusecase.ExactDraftBranch, bool) {
	return exactdraft.ActiveExactDraftBranch(plan)
}
