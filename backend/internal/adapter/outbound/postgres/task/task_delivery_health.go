package task

import (
	"context"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
)

// PrivateTaskAvailabilityPostgres reads only the durable receipt gap. It
// never loads private task content or participant identities into monitor health.
type PrivateTaskAvailabilityPostgres struct {
	tx *db.TxManager
}

var _ taskusecase.BacklogSource = (*PrivateTaskAvailabilityPostgres)(nil)

func NewPrivateTaskAvailabilityPostgres(tx *db.TxManager) *PrivateTaskAvailabilityPostgres {
	return &PrivateTaskAvailabilityPostgres{tx: tx}
}

func (repository *PrivateTaskAvailabilityPostgres) TaskDeliveryBacklog(
	ctx context.Context,
) (taskusecase.BacklogSnapshot, error) {
	if ctx == nil || repository == nil || repository.tx == nil {
		return taskusecase.BacklogSnapshot{}, taskusecase.ErrBacklogUnavailable
	}
	var snapshot taskusecase.BacklogSnapshot
	err := repository.tx.ReadSnapshot(ctx, func(txCtx context.Context) error {
		row, err := repository.tx.Querier(txCtx).GetPrivateTaskDeliveryBacklog(txCtx)
		if err != nil {
			return err
		}
		snapshot = receiptAvailabilityBacklogFromRow(row)
		return snapshot.Validate()
	})
	if err != nil {
		return taskusecase.BacklogSnapshot{}, fmt.Errorf("private task availability backlog: %w", err)
	}
	return snapshot, nil
}

func receiptAvailabilityBacklogFromRow(
	row sqlc.GetPrivateTaskDeliveryBacklogRow,
) taskusecase.BacklogSnapshot {
	return taskusecase.BacklogSnapshot{
		PendingCount:    row.PendingCount,
		OldestPendingAt: receiptAvailabilityOptionalTime(row.OldestPendingAt.Time, row.OldestPendingAt.Valid),
	}
}

func receiptAvailabilityOptionalTime(value time.Time, valid bool) *time.Time {
	if !valid {
		return nil
	}
	value = value.Round(0).UTC()
	return &value
}
