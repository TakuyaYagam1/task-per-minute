package audit

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	resultActorServer   = "server"
	resultActorOperator = "operator"
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

func optionalUUIDValue(value uuid.NullUUID) uuid.UUID {
	if !value.Valid {
		return uuid.Nil
	}
	return value.UUID
}

func nullableTSTZ(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

func validServerTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func validTrimmedText(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

func optionalTrimmedString(value string) *string {
	if value == "" {
		return nil
	}
	trimmed := strings.TrimSpace(value)
	return &trimmed
}
