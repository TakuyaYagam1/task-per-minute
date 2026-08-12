package websocket

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

type Matchmaking interface {
	JoinQueue(ctx context.Context, playerID uuid.UUID) (*duelusecase.MatchResult, error)
	LeaveQueue(ctx context.Context, playerID uuid.UUID) error
}

type FlagSubmitter interface {
	SubmitFlag(ctx context.Context, duelID, playerID uuid.UUID, flag string) (duelusecase.Result, error)
}

type ReconnectManager interface {
	StartDuelTimer(duel *domain.Duel)
	BeginDisconnect(ctx context.Context, duelID, playerID uuid.UUID)
	HandleDisconnect(ctx context.Context, duelID, playerID uuid.UUID)
	ConsumeReconnect(ctx context.Context, playerID uuid.UUID) (*duelusecase.ReconnectDecision, error)
	ActiveDuel(ctx context.Context, playerID uuid.UUID) (*duelusecase.ReconnectDecision, error)
	DuelPaused(duelID uuid.UUID) bool
	FinalizeDraw(ctx context.Context, duelID uuid.UUID) (*domain.Duel, error)
	FinalizePlayerForfeit(ctx context.Context, duelID, loserID uuid.UUID) (*domain.Duel, error)
	CloseDuel(duelID uuid.UUID)
	StopAll()
}

type PlayerReader interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Player, error)
	GetBySessionToken(ctx context.Context, token uuid.UUID) (*domain.Player, error)
}

type DuelTaskReader interface {
	GetPlayerTask(ctx context.Context, duelID, playerID uuid.UUID) (*domain.Task, error)
}

type SourceFileURLSigner interface {
	PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}
