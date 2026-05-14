package websocket

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

// Broadcaster bridges the duel usecase to WebSocket transport. Implements the
// usecase.DuelBroadcaster port.
//
// Per-connection events (duel_resume, opponent_reconnected) are NOT here -
// those are sent by the controller directly to specific *client values, since
// the usecase has no notion of individual connections.
type Broadcaster struct {
	ctx        context.Context
	hubs       *HubRegistry
	players    usecase.PlayerRepo
	clients    func(uuid.UUID) (*client, bool)
	closeDelay time.Duration
}

//nolint:contextcheck // server lifecycle ctx, not request scope.
func newBroadcaster(
	ctx context.Context,
	hubs *HubRegistry,
	players usecase.PlayerRepo,
	clients func(uuid.UUID) (*client, bool),
	closeDelay time.Duration,
) *Broadcaster {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Broadcaster{
		ctx:        ctx,
		hubs:       hubs,
		players:    players,
		clients:    clients,
		closeDelay: closeDelay,
	}
}

var _ usecase.DuelBroadcaster = (*Broadcaster)(nil)

func (b *Broadcaster) BroadcastOpponentDisconnected(
	ctx context.Context,
	duelID, playerID uuid.UUID,
	reconnectDeadline time.Time,
) {
	if b == nil || b.hubs == nil {
		return
	}
	_ = b.hubs.BroadcastJSON(ctx, duelID, EventOpponentDisconnected, OpponentDisconnectedPayload{
		DuelID:            duelID,
		PlayerID:          playerID,
		ReconnectDeadline: reconnectDeadline,
	})
}

func (b *Broadcaster) BroadcastDuelExpired(ctx context.Context, duelID uuid.UUID) {
	if b == nil || b.hubs == nil {
		return
	}
	_ = b.hubs.BroadcastJSON(ctx, duelID, EventDuelExpired, DuelExpiredPayload{DuelID: duelID})
}

func (b *Broadcaster) BroadcastDuelFinished(ctx context.Context, duel *domain.Duel) {
	if b == nil || b.hubs == nil || duel == nil {
		return
	}
	winnerUsername := b.winnerUsername(ctx, duel)
	if b.clients != nil {
		if c, ok := b.clients(duel.Player1ID); ok {
			payload := duelFinishedPayload(duel, duel.Player1ID, nil, winnerUsername)
			_ = c.sendEvent(EventDuelFinished, payload)
			c.clearDuel()
			c.setQueued(false)
		}
		if c, ok := b.clients(duel.Player2ID); ok {
			payload := duelFinishedPayload(duel, duel.Player2ID, nil, winnerUsername)
			_ = c.sendEvent(EventDuelFinished, payload)
			c.clearDuel()
			c.setQueued(false)
		}
	}

	delay := b.closeDelay
	if delay <= 0 {
		b.hubs.Close(duel.ID)
		return
	}
	runAfterOrDone(b.done(), delay, func() {
		b.hubs.Close(duel.ID)
	})
}

func (b *Broadcaster) done() <-chan struct{} {
	if b == nil || b.ctx == nil {
		return nil
	}
	return b.ctx.Done()
}

func (b *Broadcaster) winnerUsername(ctx context.Context, duel *domain.Duel) *string {
	if b == nil || duel == nil || duel.WinnerID == nil {
		return nil
	}
	if b.clients != nil {
		if c, ok := b.clients(*duel.WinnerID); ok && c.player != nil && c.player.Username != "" {
			username := c.player.Username
			return &username
		}
	}
	if b.players == nil || ctx == nil {
		return nil
	}
	player, err := b.players.GetByID(ctx, *duel.WinnerID)
	if err != nil || player == nil || player.Username == "" {
		return nil
	}
	username := player.Username
	return &username
}
