package persistent

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent/sqlc"
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
		Status:    domain.PlayerStatus(p.Status),
		CreatedAt: p.CreatedAt.Time,
	}
	if p.SessionToken.Valid {
		token := p.SessionToken.UUID
		out.SessionToken = &token
	}
	return out
}

func taskToDomain(t sqlc.Task) *domain.Task {
	return &domain.Task{
		ID:            t.ID,
		Title:         t.Title,
		Description:   t.Description,
		Category:      domain.Category(t.Category),
		Difficulty:    domain.Difficulty(t.Difficulty),
		TimeLimit:     int(t.TimeLimit),
		Flag:          t.Flag,
		Hints:         taskHintsToDomain(t.Hint1, t.Hint2, t.Hint3),
		TaskURL:       t.TaskUrl,
		SourceFileURL: t.SourceFileUrl,
		CreatedAt:     t.CreatedAt.Time,
	}
}

func taskHintsToDomain(hint1, hint2, hint3 *string) []string {
	return []string{
		stringValue(hint1),
		stringValue(hint2),
		stringValue(hint3),
	}
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

func duelToDomain(d sqlc.Duel) *domain.Duel {
	out := &domain.Duel{
		ID:        d.ID,
		Player1ID: d.Player1ID,
		Player2ID: d.Player2ID,
		Status:    domain.DuelStatus(d.Status),
		Deadline:  d.Deadline.Time,
		StartedAt: d.StartedAt.Time,
	}
	if d.WinnerID.Valid {
		w := d.WinnerID.UUID
		out.WinnerID = &w
	}
	if d.FinishedAt.Valid {
		t := d.FinishedAt.Time
		out.FinishedAt = &t
	}
	return out
}

func duelPlayerTaskToDomain(dpt sqlc.DuelPlayerTask) *domain.DuelPlayerTask {
	out := &domain.DuelPlayerTask{
		DuelID:   dpt.DuelID,
		PlayerID: dpt.PlayerID,
		TaskID:   dpt.TaskID,
		Solved:   dpt.Solved,
	}
	if dpt.SolvedAt.Valid {
		t := dpt.SolvedAt.Time
		out.SolvedAt = &t
	}
	return out
}

func tstz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
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
