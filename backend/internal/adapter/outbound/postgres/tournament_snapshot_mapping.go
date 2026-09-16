package postgres

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// utcNullableTime remains in the root package for existing tournament readers
// while snapshot hydration moves to the child package.
func utcNullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	utc := value.Time.UTC()
	return &utc
}
