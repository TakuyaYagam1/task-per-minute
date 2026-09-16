package exactdraft

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

// ExactDraftPlanningHistory keeps the planning-history mapping available to
// root-package compatibility tests while the implementation lives here.
func ExactDraftPlanningHistory(
	draft draftusecase.Execution,
	rows []sqlc.LockExactDraftPlanningHistoryRow,
) ([]capacity.TaskUse, error) {
	return exactDraftHistory(draft, rows)
}

// RehydrateExactDraftHistory keeps the child-history mapping available to
// root-package compatibility tests while the implementation lives here.
func RehydrateExactDraftHistory(
	rows []sqlc.ExactDraftAssignmentChildHistory,
	participants []assignmentusecase.ExactNormalParticipantReservation,
) ([]capacity.TaskUse, error) {
	return rehydrateExactDraftHistory(rows, participants)
}

// ExactDraftPlanHasCandidates keeps the candidate predicate available to
// root-package compatibility callers.
func ExactDraftPlanHasCandidates(
	plan assignmentusecase.ExactDraftBranchPlan,
	candidates []assignmentusecase.ExactNormalTaskVersion,
) bool {
	return exactDraftPlanHasCandidates(plan, candidates)
}

// ValidateExactDraftPlanCandidates keeps candidate validation available to
// root-package compatibility callers.
func ValidateExactDraftPlanCandidates(
	plan assignmentusecase.ExactDraftBranchPlan,
	candidates []assignmentusecase.ExactNormalTaskVersion,
	exclusions ...[]domain.TaskVersionRef,
) error {
	return validateExactDraftPlanCandidates(plan, candidates, exclusions...)
}

// ActiveExactDraftBranch keeps active-branch selection available to
// root-package compatibility callers.
func ActiveExactDraftBranch(
	plan assignmentusecase.ExactDraftBranchPlan,
) (assignmentusecase.ExactDraftBranch, bool) {
	return activeExactDraftBranch(plan)
}
