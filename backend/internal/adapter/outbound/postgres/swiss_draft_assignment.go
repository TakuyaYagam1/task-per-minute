package postgres

import (
	"context"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	swissdraft "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/swiss/draft"
)

func (r *ExactDraftBranchPlanPostgres) lockExactDraftPlanningStage(ctx context.Context, draftID uuid.UUID) (sqlc.LockExactDraftPlanningStageRow, error) {
	return swissdraft.LockExactDraftPlanningStage(ctx, r.tx, draftID)
}

func (r *ExactDraftBranchPlanPostgres) lockExactDraftPlanningParticipants(ctx context.Context, draftID uuid.UUID) ([]sqlc.LockExactDraftPlanningParticipantsRow, error) {
	return swissdraft.LockExactDraftPlanningParticipants(ctx, r.tx, draftID)
}

func (r *ExactDraftBranchPlanPostgres) lockExactDraftPlanningHistory(ctx context.Context, draftID uuid.UUID) ([]sqlc.LockExactDraftPlanningHistoryRow, error) {
	return swissdraft.LockExactDraftPlanningHistory(ctx, r.tx, draftID)
}

func (r *ExactDraftBranchPlanPostgres) ensureExactDraftPlanningHistoryHead(ctx context.Context, draftID uuid.UUID) error {
	return swissdraft.EnsureExactDraftPlanningHistoryHead(ctx, r.tx, draftID)
}

func (r *ExactDraftBranchPlanPostgres) lockExactDraftPlanningHistoryHead(ctx context.Context, draftID uuid.UUID) (sqlc.LockExactDraftPlanningHistoryHeadRow, error) {
	return swissdraft.LockExactDraftPlanningHistoryHead(ctx, r.tx, draftID)
}

func (r *ExactDraftBranchPlanPostgres) lockExactDraftPlanningReservationKeys(ctx context.Context, draftID uuid.UUID) error {
	return swissdraft.LockExactDraftPlanningReservationKeys(ctx, r.tx, draftID)
}

func (r *ExactDraftBranchPlanPostgres) lockExactDraftPlanningCandidates(ctx context.Context, params sqlc.LockExactDraftPlanningCandidatesParams) ([]sqlc.LockExactDraftPlanningCandidatesRow, error) {
	return swissdraft.LockExactDraftPlanningCandidates(ctx, r.tx, params)
}

func (r *ExactDraftBranchPlanPostgres) lockExactDraftAssignmentSource(ctx context.Context, planID uuid.UUID) (sqlc.LockExactDraftAssignmentSourceRow, error) {
	return swissdraft.LockExactDraftAssignmentSource(ctx, r.tx, planID)
}
