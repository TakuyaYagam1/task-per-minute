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
// Per-connection events (duel_resume, opponent_reconnected) are NOT here —
// those are sent by the controller directly to specific *client values, since
// the usecase has no notion of individual connections.
type Broadcaster struct {
	hubs       *HubRegistry
	clients    func(uuid.UUID) (*client, bool)
	closeDelay time.Duration
}

func newBroadcaster(hubs *HubRegistry, clients func(uuid.UUID) (*client, bool), closeDelay time.Duration) *Broadcaster {
	return &Broadcaster{
		hubs:       hubs,
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
	payload := DuelFinishedPayload{Duel: duelPayload(duel)}
	_ = b.hubs.BroadcastJSON(ctx, duel.ID, EventDuelFinished, payload)

	if b.clients != nil {
		if c, ok := b.clients(duel.Player1ID); ok {
			c.clearDuel()
			c.setQueued(false)
		}
		if c, ok := b.clients(duel.Player2ID); ok {
			c.clearDuel()
			c.setQueued(false)
		}
	}

	delay := b.closeDelay
	if delay <= 0 {
		b.hubs.Close(duel.ID)
		return
	}
	time.AfterFunc(delay, func() {
		b.hubs.Close(duel.ID)
	})
}
