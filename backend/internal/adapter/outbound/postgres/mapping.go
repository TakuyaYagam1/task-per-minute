package postgres

import (
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func nullableTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	out := t.Time
	return &out
}
