package recovery

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	recoveryusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

type RecoveryPostgres struct {
	tx   *db.TxManager
	sink recoveryusecase.DeadlineArmSink
}

var (
	_ recoveryusecase.DeadlineSource  = (*RecoveryPostgres)(nil)
	_ recoveryusecase.DeadlineRearmer = (*RecoveryPostgres)(nil)
)

func NewRecoveryPostgres(tx *db.TxManager, sink recoveryusecase.DeadlineArmSink) *RecoveryPostgres {
	return &RecoveryPostgres{tx: tx, sink: sink}
}

func (repository *RecoveryPostgres) ListPendingDeadlines(
	ctx context.Context,
	after recoveryusecase.DeadlineCursor,
	limit int32,
) ([]recoveryusecase.PendingDeadline, error) {
	if ctx == nil || repository == nil || repository.tx == nil ||
		after.Validate() != nil || limit < 1 || limit > recoveryusecase.MaximumSweepBatchSize {
		return nil, domain.ErrValidation
	}
	rows, err := repository.tx.Querier(ctx).ListPendingRecoveryDeadlines(
		ctx,
		sqlc.ListPendingRecoveryDeadlinesParams{
			AfterKind: recoveryCursorKind(after.Kind),
			AfterID:   after.ID,
			BatchSize: limit,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("RecoveryPostgres - ListPendingDeadlines: %w", err)
	}
	deadlines := make([]recoveryusecase.PendingDeadline, len(rows))
	for index := range rows {
		deadlines[index], err = recoveryDeadlineFromListRow(rows[index])
		if err != nil {
			return nil, fmt.Errorf("RecoveryPostgres - ListPendingDeadlines - row %d: %w", index, err)
		}
	}
	return deadlines, nil
}

// RearmDeadline rechecks the scanned row before scheduling it. A stale item is
// a successful no-op; the handler's compare-and-set remains the terminal
// linearization boundary for changes racing this reload.
func (repository *RecoveryPostgres) RearmDeadline(
	ctx context.Context,
	deadline recoveryusecase.PendingDeadline,
) (bool, error) {
	if ctx == nil || repository == nil || repository.tx == nil || repository.sink == nil ||
		deadline.Validate() != nil {
		return false, domain.ErrValidation
	}
	row, err := repository.tx.Querier(ctx).GetPendingRecoveryDeadline(
		ctx,
		sqlc.GetPendingRecoveryDeadlineParams{
			DeadlineKind: string(deadline.Kind),
			DeadlineID:   deadline.ID,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("RecoveryPostgres - RearmDeadline - reload: %w", err)
	}
	current, err := recoveryDeadlineFromGetRow(row)
	if err != nil {
		return false, fmt.Errorf("RecoveryPostgres - RearmDeadline - map: %w", err)
	}
	if !sameRecoveryDeadline(current, deadline) {
		return false, nil
	}
	rearmed, err := repository.sink.ArmDeadline(ctx, current)
	if err != nil {
		return false, fmt.Errorf("RecoveryPostgres - RearmDeadline - arm: %w", err)
	}
	return rearmed, nil
}
