package exactdraft

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func (r *ExactDraftBranchPlanPostgres) lockExactDraftPlanningStage(ctx context.Context, draftID uuid.UUID) (sqlc.LockExactDraftPlanningStageRow, error) {
	q := r.tx.Querier(ctx)
	row, err := q.LockExactDraftPlanningStage(ctx, draftID)
	if !errors.Is(err, pgx.ErrNoRows) {
		return row, err
	}
	swiss, err := q.LockSwissDraftPlanningStage(ctx, draftID)
	return sqlc.LockExactDraftPlanningStageRow(swiss), err
}

func (r *ExactDraftBranchPlanPostgres) lockExactDraftPlanningParticipants(ctx context.Context, draftID uuid.UUID) ([]sqlc.LockExactDraftPlanningParticipantsRow, error) {
	q := r.tx.Querier(ctx)
	rows, err := q.LockExactDraftPlanningParticipants(ctx, draftID)
	if err != nil || len(rows) != 0 {
		return rows, err
	}
	swiss, err := q.LockSwissDraftPlanningParticipants(ctx, draftID)
	for _, row := range swiss {
		rows = append(rows, sqlc.LockExactDraftPlanningParticipantsRow(row))
	}
	return rows, err
}

func (r *ExactDraftBranchPlanPostgres) lockExactDraftPlanningHistory(ctx context.Context, draftID uuid.UUID) ([]sqlc.LockExactDraftPlanningHistoryRow, error) {
	q := r.tx.Querier(ctx)
	rows, err := q.LockExactDraftPlanningHistory(ctx, draftID)
	if err != nil || len(rows) != 0 {
		return rows, err
	}
	swiss, err := q.LockSwissDraftPlanningHistory(ctx, draftID)
	for _, row := range swiss {
		rows = append(rows, sqlc.LockExactDraftPlanningHistoryRow(row))
	}
	return rows, err
}

func (r *ExactDraftBranchPlanPostgres) ensureExactDraftPlanningHistoryHead(ctx context.Context, draftID uuid.UUID) error {
	q := r.tx.Querier(ctx)
	if err := q.EnsureExactDraftPlanningHistoryHead(ctx, draftID); err != nil {
		return err
	}
	return q.EnsureSwissDraftPlanningHistoryHead(ctx, draftID)
}

func (r *ExactDraftBranchPlanPostgres) lockExactDraftPlanningHistoryHead(ctx context.Context, draftID uuid.UUID) (sqlc.LockExactDraftPlanningHistoryHeadRow, error) {
	q := r.tx.Querier(ctx)
	row, err := q.LockExactDraftPlanningHistoryHead(ctx, draftID)
	if !errors.Is(err, pgx.ErrNoRows) {
		return row, err
	}
	swiss, err := q.LockSwissDraftPlanningHistoryHead(ctx, draftID)
	return sqlc.LockExactDraftPlanningHistoryHeadRow(swiss), err
}

func (r *ExactDraftBranchPlanPostgres) lockExactDraftPlanningReservationKeys(ctx context.Context, draftID uuid.UUID) error {
	q := r.tx.Querier(ctx)
	if err := q.LockExactDraftPlanningReservationKeys(ctx, draftID); err != nil {
		return err
	}
	return q.LockSwissDraftPlanningReservationKeys(ctx, draftID)
}

func (r *ExactDraftBranchPlanPostgres) lockExactDraftPlanningCandidates(ctx context.Context, params sqlc.LockExactDraftPlanningCandidatesParams) ([]sqlc.LockExactDraftPlanningCandidatesRow, error) {
	q := r.tx.Querier(ctx)
	rows, err := q.LockExactDraftPlanningCandidates(ctx, params)
	if err != nil || len(rows) != 0 {
		return rows, err
	}
	swiss, err := q.LockSwissDraftPlanningCandidates(ctx, sqlc.LockSwissDraftPlanningCandidatesParams(params))
	for _, row := range swiss {
		rows = append(rows, sqlc.LockExactDraftPlanningCandidatesRow(row))
	}
	return rows, err
}

func (r *ExactDraftBranchPlanPostgres) lockExactDraftAssignmentSource(ctx context.Context, planID uuid.UUID) (sqlc.LockExactDraftAssignmentSourceRow, error) {
	q := r.tx.Querier(ctx)
	row, err := q.LockExactDraftAssignmentSource(ctx, planID)
	if !errors.Is(err, pgx.ErrNoRows) {
		return row, err
	}
	swiss, err := q.LockSwissDraftAssignmentSource(ctx, planID)
	return sqlc.LockExactDraftAssignmentSourceRow(swiss), err
}
