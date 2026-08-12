package duel

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

type MatchmakingQueue interface {
	Enqueue(ctx context.Context, playerID uuid.UUID) error
	PopPair(ctx context.Context) (uuid.UUID, uuid.UUID, bool, error)
	Remove(ctx context.Context, playerID uuid.UUID) error
}

type MatchmakingPlayerRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Player, error)
	UpdateStatusIfCurrent(ctx context.Context, id uuid.UUID, from, to domain.PlayerStatus) (*domain.Player, bool, error)
}

type MatchmakingTaskRepository interface {
	ListByDifficulty(ctx context.Context, difficulty domain.Difficulty) ([]*domain.Task, error)
	CountByDifficulty(ctx context.Context, difficulty domain.Difficulty) (int64, error)
	CountSolvedByDifficulty(ctx context.Context, playerID uuid.UUID, difficulty domain.Difficulty) (int64, error)
}

type MatchmakingHistoryRepository interface {
	ListSolvedTaskIDs(ctx context.Context, playerID uuid.UUID) ([]uuid.UUID, error)
}

type MatchmakingDuelRepository interface {
	Create(ctx context.Context, player1ID, player2ID uuid.UUID, deadline time.Time) (*domain.Duel, error)
	CreateDuelPlayerTask(ctx context.Context, duelID, playerID, taskID uuid.UUID) error
}

type SourceFileURLSigner interface {
	PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

type FinalizationDuelRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Duel, error)
	Finish(ctx context.Context, id uuid.UUID, winnerID *uuid.UUID, finishedAt time.Time, status domain.DuelStatus) (*domain.Duel, error)
}

type FinalizationPlayerRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Player, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.PlayerStatus) (*domain.Player, error)
}

type FlagDuelRepository interface {
	FinalizationDuelRepository
	GetPlayerTask(ctx context.Context, duelID, playerID uuid.UUID) (*domain.Task, error)
	MarkSolved(ctx context.Context, duelID, playerID uuid.UUID, solvedAt time.Time) error
}

type ReconnectDuelRepository interface {
	FinalizationDuelRepository
	GetActiveByPlayerID(ctx context.Context, playerID uuid.UUID) (*domain.Duel, error)
	UpdateDeadline(ctx context.Context, id uuid.UUID, deadline time.Time) (*domain.Duel, error)
}

type ReadDuelRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Duel, error)
	GetDuelPlayerTask(ctx context.Context, duelID, playerID uuid.UUID) (*domain.DuelPlayerTask, error)
}

type SolvedHistoryWriter interface {
	AddSolved(ctx context.Context, playerID, taskID uuid.UUID, solvedAt time.Time) error
}

type LeaderboardBumper interface {
	IncrementWin(ctx context.Context, username string) error
}

type Broadcaster interface {
	BroadcastOpponentDisconnected(ctx context.Context, duelID, playerID uuid.UUID, reconnectDeadline time.Time)
	BroadcastDuelExpired(ctx context.Context, duelID uuid.UUID)
	BroadcastDuelFinished(ctx context.Context, duel *domain.Duel)
}
