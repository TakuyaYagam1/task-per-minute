package recovery

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

type ActiveDuelRepository interface {
	ListActive(ctx context.Context) ([]*domain.Duel, error)
	Finish(ctx context.Context, id uuid.UUID, winnerID *uuid.UUID, finishedAt time.Time, status domain.DuelStatus) (*domain.Duel, error)
}

type DuelTaskReader interface {
	GetPlayerTask(ctx context.Context, duelID, playerID uuid.UUID) (*domain.Task, error)
}

type PlayerStatusRepository interface {
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.PlayerStatus) (*domain.Player, error)
}

type QueuedPlayerResetter interface {
	ResetQueuedToIdle(ctx context.Context) (int64, error)
}

type QueueCleaner interface {
	Clear(ctx context.Context) error
}

type DuelFinishedBroadcaster interface {
	BroadcastDuelFinished(ctx context.Context, duel *domain.Duel)
}

type ActiveDuelTimerStarter interface {
	StartDuelTimer(duel *domain.Duel)
}

type ActiveDuelHintStarter interface {
	StartDuel(duel *domain.Duel, assignments map[uuid.UUID]*domain.Task)
}
