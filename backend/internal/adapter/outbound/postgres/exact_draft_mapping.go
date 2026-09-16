package postgres

import (
	exactdraft "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/exactdraft"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func exactDraftHistory(
	draft draftusecase.Execution,
	rows []sqlc.LockExactDraftPlanningHistoryRow,
) ([]capacity.TaskUse, error) {
	return exactdraft.ExactDraftPlanningHistory(draft, rows)
}

func rehydrateExactDraftHistory(
	rows []sqlc.ExactDraftAssignmentChildHistory,
	participants []assignmentusecase.ExactNormalParticipantReservation,
) ([]capacity.TaskUse, error) {
	return exactdraft.RehydrateExactDraftHistory(rows, participants)
}

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
