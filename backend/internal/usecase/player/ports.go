package player

import (
	"context"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

type ManagementClock interface {
	Now() time.Time
}

type LeaderboardInvalidator interface {
	Invalidate()
}

type PlayerRepository interface {
	ListPlayers(ctx context.Context, includeDeleted bool) ([]PlayerRecord, error)
	GetPlayer(ctx context.Context, id uuid.UUID) (*PlayerRecord, error)
	GetPlayerIncludingDeleted(ctx context.Context, id uuid.UUID) (*PlayerRecord, error)
	UpdateUsername(ctx context.Context, id uuid.UUID, username string) error
	UpsertStats(ctx context.Context, id uuid.UUID, input StatsInput, updatedAt time.Time) error
	SoftDeletePlayer(ctx context.Context, id uuid.UUID, deletedUsername string, deletedAt time.Time) error
	CreatePlayerAudit(ctx context.Context, input AuditInput) error
	ListPlayerAudit(ctx context.Context, playerID uuid.UUID, limit int32) ([]AuditEvent, error)
}

type ManagementTransactionManager interface {
	Do(ctx context.Context, fn func(context.Context) error) error
}

type SessionClock interface {
	Now() time.Time
}

// Repository owns player session persistence. JoinByUsername creates a
// session for a new username or reclaims an expired session. An active
// username must return domain.ErrUsernameTaken.
type Repository interface {
	JoinByUsername(
		ctx context.Context,
		username string,
		sessionToken uuid.UUID,
		sessionExpiresAt time.Time,
	) (*domain.Player, error)
	GetBySessionToken(ctx context.Context, token uuid.UUID) (*domain.Player, error)
	UpdateSessionToken(
		ctx context.Context,
		id uuid.UUID,
		token *uuid.UUID,
		sessionExpiresAt *time.Time,
	) (*domain.Player, error)
}

type SessionTransactionManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}
