package player

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type Clock interface {
	Now() time.Time
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

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

type ActiveDuelReader interface {
	GetActiveByPlayerID(ctx context.Context, playerID uuid.UUID) (*domain.Duel, error)
}
