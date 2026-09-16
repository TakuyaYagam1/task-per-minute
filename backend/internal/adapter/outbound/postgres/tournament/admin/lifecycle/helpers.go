package lifecycle

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentlifecycle "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
)

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func nullableTSTZ(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

func mapRepositoryWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23503", "23514", "40001":
			return domain.WrapError(err, domain.ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

//nolint:unparam // The error return preserves the lifecycle adapter's compatibility seam for callers that normalize repository errors.
func tournamentLifecycleSummaryRecord(
	row sqlc.GetTournamentSummaryRow,
) (*tournamentlifecycle.LifecycleTournamentRecord, error) {
	record := &tournamentlifecycle.LifecycleTournamentRecord{
		ID: row.ID, State: domain.TournamentState(row.State), Revision: row.Revision,
		UpdatedAt: row.UpdatedAt.Time.UTC(),
		StartedAt: lifecycleUTCNullableTime(row.StartedAt), FinishedAt: lifecycleUTCNullableTime(row.FinishedAt),
	}
	if row.PausedFromState != nil {
		state := domain.TournamentState(*row.PausedFromState)
		record.PausedFromState = &state
	}
	return record, nil
}

func lifecycleUTCNullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}
