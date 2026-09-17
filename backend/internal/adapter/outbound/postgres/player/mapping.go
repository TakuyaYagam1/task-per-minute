package player

import (
	"errors"
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
	pgRestrictViolation   = "23001"
)

func playerToDomain(p sqlc.Player) *domain.Player {
	out := &domain.Player{
		ID:        p.ID,
		Username:  p.Username,
		CreatedAt: p.CreatedAt.Time,
	}
	if p.SessionToken.Valid {
		token := p.SessionToken.UUID
		out.SessionToken = &token
	}
	if p.SessionExpiresAt.Valid {
		expiresAt := p.SessionExpiresAt.Time
		out.SessionExpiresAt = &expiresAt
	}
	return out
}

func nullableUUID(p *uuid.UUID) uuid.NullUUID {
	if p == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *p, Valid: true}
}

func tstz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func nullableTSTZ(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func nullableTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	out := t.Time
	return &out
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == constraint
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == pgForeignKeyViolation || pgErr.Code == pgRestrictViolation
}
