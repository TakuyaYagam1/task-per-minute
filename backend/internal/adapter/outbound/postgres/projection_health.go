package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

// ProjectionHealthPostgres reads one durable, bounded publication-lag sample.
// It never uses realtime delivery state as a proxy for projection freshness.
type ProjectionHealthPostgres struct {
	tx *TxManager
}

var _ observability.ProjectionHealthSource = (*ProjectionHealthPostgres)(nil)

func NewProjectionHealthPostgres(tx *TxManager) *ProjectionHealthPostgres {
	return &ProjectionHealthPostgres{tx: tx}
}

func (repository *ProjectionHealthPostgres) ProjectionHealth(
	ctx context.Context,
) (observability.ProjectionHealthSnapshot, error) {
	if ctx == nil || repository == nil || repository.tx == nil {
		return observability.ProjectionHealthSnapshot{}, observability.ErrInvalidProjectionHealth
	}
	var snapshot observability.ProjectionHealthSnapshot
	err := repository.tx.ReadSnapshot(ctx, func(txCtx context.Context) error {
		row, err := repository.tx.Querier(txCtx).GetProjectionPublicationHealth(txCtx)
		if err != nil {
			return err
		}
		snapshot = projectionHealthFromRow(row)
		return snapshot.Validate()
	})
	if err != nil {
		return observability.ProjectionHealthSnapshot{}, fmt.Errorf("projection health: %w", err)
	}
	return snapshot, nil
}

func projectionHealthFromRow(
	row sqlc.GetProjectionPublicationHealthRow,
) observability.ProjectionHealthSnapshot {
	return projectionHealthSnapshot(
		row.PendingCount,
		row.OldestPendingAt.Time,
		row.OldestPendingAt.Valid,
		row.ObservedAt.Time,
	)
}

func projectionHealthSnapshot(
	pendingCount int64,
	oldestPendingAt time.Time,
	oldestPendingAtValid bool,
	observedAt time.Time,
) observability.ProjectionHealthSnapshot {
	return observability.ProjectionHealthSnapshot{
		PendingCount:    pendingCount,
		OldestPendingAt: projectionHealthOptionalTime(oldestPendingAt, oldestPendingAtValid),
		ObservedAt:      observedAt.Round(0).UTC(),
	}
}

func projectionHealthOptionalTime(value time.Time, valid bool) *time.Time {
	if !valid {
		return nil
	}
	value = value.Round(0).UTC()
	return &value
}
