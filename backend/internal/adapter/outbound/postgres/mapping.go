package postgres

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

func createTaskToDomain(t sqlc.CreateTaskRow) *domain.Task {
	return taskValuesToDomain(
		t.ID, t.Title, t.Description, t.Category, t.Difficulty, t.TimeLimit, t.Flag,
		t.Hint1, t.Hint2, t.Hint3, t.TaskUrl, t.SourceFileUrl, t.Kind, t.Enabled,
		t.CurrentVersion, t.CreatedAt.Time,
	)
}

func updateTaskToDomain(t sqlc.UpdateTaskRow) *domain.Task {
	return taskValuesToDomain(
		t.ID, t.Title, t.Description, t.Category, t.Difficulty, t.TimeLimit, t.Flag,
		t.Hint1, t.Hint2, t.Hint3, t.TaskUrl, t.SourceFileUrl, t.Kind, t.Enabled,
		t.CurrentVersion, t.CreatedAt.Time,
	)
}

func taskValuesToDomain(
	id uuid.UUID,
	title, description, category, difficulty string,
	timeLimit int32,
	flag string,
	hint1, hint2, hint3, taskURL, sourceFileURL *string,
	kind string,
	enabled bool,
	currentVersion int32,
	createdAt time.Time,
) *domain.Task {
	return &domain.Task{
		ID:             id,
		Title:          title,
		Description:    description,
		Category:       domain.Category(category),
		Difficulty:     domain.Difficulty(difficulty),
		TimeLimit:      int(timeLimit),
		Flag:           flag,
		Hints:          taskHintsToDomain(hint1, hint2, hint3),
		TaskURL:        taskURL,
		SourceFileURL:  sourceFileURL,
		Kind:           domain.TaskKind(kind),
		Enabled:        enabled,
		CurrentVersion: int(currentVersion),
		CreatedAt:      createdAt,
	}
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
