package replay

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func taskHintsToDomain(hint1, hint2, hint3 *string) []string {
	hints, _ := domain.NormalizeTaskHints([]string{
		stringValue(hint1),
		stringValue(hint2),
		stringValue(hint3),
	})
	return hints
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
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
