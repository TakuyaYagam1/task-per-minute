package wave

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

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

func mapRepositoryWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation, pgForeignKeyViolation, "23514", "40001":
			return domain.WrapError(err, domain.ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func digestBytes(payload []byte) []byte {
	digest := sha256.Sum256(payload)
	return append([]byte(nil), digest[:]...)
}

func createResultProjectionNode(
	ctx context.Context,
	querier *sqlc.Queries,
	input sqlc.CreateResultProjectionNodeParams,
) error {
	if err := querier.CreateResultProjectionNode(ctx, input); err != nil {
		return mapRepositoryWriteError("ResultPostgres - Settle - create projection node", err)
	}
	return nil
}
