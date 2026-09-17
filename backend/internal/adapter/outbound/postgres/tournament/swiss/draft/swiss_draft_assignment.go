package draft

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

// LockExactDraftPlanningStage loads the exact-draft stage and falls back to
// the Swiss-draft stage for the shared planning workflow.
func LockExactDraftPlanningStage(
	ctx context.Context,
	tx *db.TxManager,
	draftID uuid.UUID,
) (sqlc.LockExactDraftPlanningStageRow, error) {
	q := tx.Querier(ctx)
	row, err := q.LockExactDraftPlanningStage(ctx, draftID)
	if !errors.Is(err, pgx.ErrNoRows) {
		return row, err
	}
	swiss, err := q.LockSwissDraftPlanningStage(ctx, draftID)
	return sqlc.LockExactDraftPlanningStageRow(swiss), err
}

// LockExactDraftPlanningParticipants loads exact-draft participants and the
// Swiss fallback rows.
func LockExactDraftPlanningParticipants(
	ctx context.Context,
	tx *db.TxManager,
	draftID uuid.UUID,
) ([]sqlc.LockExactDraftPlanningParticipantsRow, error) {
	q := tx.Querier(ctx)
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

// LockExactDraftPlanningHistory loads exact-draft history and the Swiss
// fallback rows.
func LockExactDraftPlanningHistory(
	ctx context.Context,
	tx *db.TxManager,
	draftID uuid.UUID,
) ([]sqlc.LockExactDraftPlanningHistoryRow, error) {
	q := tx.Querier(ctx)
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

// EnsureExactDraftPlanningHistoryHead ensures both planning history heads.
func EnsureExactDraftPlanningHistoryHead(ctx context.Context, tx *db.TxManager, draftID uuid.UUID) error {
	q := tx.Querier(ctx)
	if err := q.EnsureExactDraftPlanningHistoryHead(ctx, draftID); err != nil {
		return err
	}
	return q.EnsureSwissDraftPlanningHistoryHead(ctx, draftID)
}

// LockExactDraftPlanningHistoryHead loads the exact-draft history head and
// falls back to the Swiss-draft history head.
func LockExactDraftPlanningHistoryHead(
	ctx context.Context,
	tx *db.TxManager,
	draftID uuid.UUID,
) (sqlc.LockExactDraftPlanningHistoryHeadRow, error) {
	q := tx.Querier(ctx)
	row, err := q.LockExactDraftPlanningHistoryHead(ctx, draftID)
	if !errors.Is(err, pgx.ErrNoRows) {
		return row, err
	}
	swiss, err := q.LockSwissDraftPlanningHistoryHead(ctx, draftID)
	return sqlc.LockExactDraftPlanningHistoryHeadRow(swiss), err
}

// LockExactDraftPlanningReservationKeys locks both reservation-key sets.
func LockExactDraftPlanningReservationKeys(ctx context.Context, tx *db.TxManager, draftID uuid.UUID) error {
	q := tx.Querier(ctx)
	if err := q.LockExactDraftPlanningReservationKeys(ctx, draftID); err != nil {
		return err
	}
	return q.LockSwissDraftPlanningReservationKeys(ctx, draftID)
}

// LockExactDraftPlanningCandidates loads exact-draft candidates and the Swiss
// fallback rows.
func LockExactDraftPlanningCandidates(
	ctx context.Context,
	tx *db.TxManager,
	params sqlc.LockExactDraftPlanningCandidatesParams,
) ([]sqlc.LockExactDraftPlanningCandidatesRow, error) {
	q := tx.Querier(ctx)
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

// LockExactDraftAssignmentSource loads the exact-draft assignment source and
// falls back to the Swiss-draft source.
func LockExactDraftAssignmentSource(
	ctx context.Context,
	tx *db.TxManager,
	planID uuid.UUID,
) (sqlc.LockExactDraftAssignmentSourceRow, error) {
	q := tx.Querier(ctx)
	row, err := q.LockExactDraftAssignmentSource(ctx, planID)
	if !errors.Is(err, pgx.ErrNoRows) {
		return row, err
	}
	swiss, err := q.LockSwissDraftAssignmentSource(ctx, planID)
	return sqlc.LockExactDraftAssignmentSourceRow(swiss), err
}
