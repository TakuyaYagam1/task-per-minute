package swiss

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const pgUniqueViolation = "23505"

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func nullableTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	out := value.Time
	return &out
}

func validServerTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == constraint
}

func marshalJSON(operation string, value any) ([]byte, error) {
	reflected := reflect.ValueOf(value)
	if reflected.IsValid() && reflected.Kind() == reflect.Slice && reflected.IsNil() {
		return []byte("[]"), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%s - marshal JSON: %w", operation, err)
	}
	return data, nil
}
