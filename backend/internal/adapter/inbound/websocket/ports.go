package websocket

import (
	"context"
	"time"

	"github.com/google/uuid"

	arenaws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/arena"
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

type ArenaParticipantConnectionRequest struct {
	Principal    arenaws.ParticipantRealtimePrincipal
	TournamentID uuid.UUID
	Cursor       *arenaws.RealtimeCursor
}

type ArenaPublicConnectionRequest struct {
	TournamentID uuid.UUID
	Cursor       *arenaws.RealtimeCursor
}

type ArenaOperatorConnectionRequest struct {
	Principal    arenaws.OperatorRealtimePrincipal
	TournamentID uuid.UUID
	Cursor       *arenaws.RealtimeCursor
}

type ArenaParticipantConnectionFlow interface {
	// ctx covers the full WebSocket connection lifetime.
	OpenArenaParticipant(ctx context.Context, request ArenaParticipantConnectionRequest) (ArenaParticipantPayload, error)
}

type ArenaPublicConnectionFlow interface {
	// ctx covers the full WebSocket connection lifetime and releases public capacity when canceled.
	OpenArenaPublic(ctx context.Context, request ArenaPublicConnectionRequest) (ArenaPublicPayload, error)
}

type ArenaOperatorConnectionFlow interface {
	// ctx covers the full WebSocket connection lifetime.
	OpenArenaOperator(ctx context.Context, request ArenaOperatorConnectionRequest) (ArenaOperatorPayload, error)
}

type ArenaTerminalSubscriptionRequest struct {
	Role          ArenaRole
	Authenticated bool
	TournamentID  uuid.UUID
	ParticipantID uuid.UUID
}

type ArenaTerminalSubscription interface {
	Deliveries() <-chan arenaws.CancellationDelivery
	// Close is idempotent.
	Close()
}

type ArenaTerminalSubscriptionFlow interface {
	// ctx covers the full WebSocket connection lifetime.
	SubscribeArenaTerminal(ctx context.Context, request ArenaTerminalSubscriptionRequest) (ArenaTerminalSubscription, error)
}
