package websocket

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	wskit "github.com/wahrwelt-kit/go-wskit"
	"golang.org/x/time/rate"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	defaultWriteWait       = 10 * time.Second
	defaultPingInterval    = 20 * time.Second
	defaultReadIdleTimeout = 75 * time.Second
	defaultSendBufferSize  = 32
	defaultReadLimit       = 16 * 1024
)

var errClientSendFailed = errors.New("websocket client send failed")

type client struct {
	player           *domain.Player
	sessionToken     uuid.UUID
	sessionExpiresAt *time.Time
	messageLimiter   *rate.Limiter
	actionLimiter    *rate.Limiter
	conn             *coderws.Conn
	send             chan []byte
	done             chan struct{}

	closeOnce sync.Once
	closed    atomic.Bool
	displaced atomic.Bool

	stateMu sync.RWMutex
	queued  bool
	duelID  *uuid.UUID
}

var _ wskit.Subscriber = (*client)(nil)

func newClient(player *domain.Player, conn *coderws.Conn, limits InboundRateLimits) *client {
	conn.SetReadLimit(defaultReadLimit)
	var sessionToken uuid.UUID
	var sessionExpiresAt *time.Time
	if player != nil && player.SessionToken != nil {
		sessionToken = *player.SessionToken
	}
	if player != nil && player.SessionExpiresAt != nil {
		expiresAt := *player.SessionExpiresAt
		sessionExpiresAt = &expiresAt
	}
	return &client{
		player:           player,
		sessionToken:     sessionToken,
		sessionExpiresAt: sessionExpiresAt,
		messageLimiter:   newInboundLimiter(limits.MessageAttempts, limits.MessageWindow),
		actionLimiter:    newInboundLimiter(limits.ActionAttempts, limits.ActionWindow),
		conn:             conn,
		send:             make(chan []byte, defaultSendBufferSize),
		done:             make(chan struct{}),
	}
}

func newInboundLimiter(attempts int, window time.Duration) *rate.Limiter {
	if attempts <= 0 || window <= 0 {
		return nil
	}
	return rate.NewLimiter(rate.Every(window/time.Duration(attempts)), attempts)
}

func (c *client) allowMessage() bool {
	return c == nil || c.messageLimiter == nil || c.messageLimiter.Allow()
}

func (c *client) allowAction() bool {
	return c == nil || c.actionLimiter == nil || c.actionLimiter.Allow()
}

func (c *client) Send(data []byte) bool {
	if c.closed.Load() {
		return false
	}
	select {
	case c.send <- data:
		return true
	case <-c.done:
		return false
	default:
		c.CloseNow()
		return false
	}
}

func (c *client) Close() {
	c.closeWith(func(conn *coderws.Conn) error {
		return conn.Close(coderws.StatusNormalClosure, "")
	})
}

func (c *client) CloseNow() {
	c.closeWith(func(conn *coderws.Conn) error {
		return conn.CloseNow()
	})
}

func (c *client) closeWith(closeConn func(*coderws.Conn) error) {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.done)
		if c.conn == nil {
			return
		}
		_ = closeConn(c.conn)
	})
}

func (c *client) markDisplaced() {
	c.displaced.Store(true)
}

func (c *client) isDisplaced() bool {
	return c.displaced.Load()
}

func (c *client) writePump(ctx context.Context) {
	ticker := time.NewTicker(defaultPingInterval)
	defer func() {
		ticker.Stop()
		c.CloseNow()
	}()

	for {
		select {
		case msg := <-c.send:
			writeCtx, cancel := context.WithTimeout(ctx, defaultWriteWait)
			err := c.conn.Write(writeCtx, coderws.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, defaultWriteWait)
			err := c.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		case <-c.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (c *client) sendEvent(typ string, payload any) error {
	data, err := marshalEvent(typ, payload)
	if err != nil {
		return err
	}
	if !c.Send(data) {
		return errClientSendFailed
	}
	return nil
}

func (c *client) sendError(code, message string) error {
	data, err := marshalError(code, message)
	if err != nil {
		return err
	}
	if !c.Send(data) {
		return errClientSendFailed
	}
	return nil
}

func (c *client) sendDuelFinishedIfCurrent(duelID uuid.UUID, payload DuelFinishedPayload) error {
	data, err := marshalEvent(EventDuelFinished, payload)
	if err != nil {
		return err
	}

	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.duelID == nil || *c.duelID != duelID {
		return nil
	}
	if !c.Send(data) {
		return errClientSendFailed
	}
	c.duelID = nil
	c.queued = false
	return nil
}

func (c *client) setQueued(queued bool) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.queued = queued
}

func (c *client) isQueued() bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.queued
}

func (c *client) setDuel(duelID uuid.UUID) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.duelID = &duelID
}

func (c *client) clearDuel() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.duelID = nil
}

func (c *client) currentDuel() (uuid.UUID, bool) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	if c.duelID == nil {
		return uuid.Nil, false
	}
	return *c.duelID, true
}

func (c *client) stateSnapshot() (queued bool, duelID uuid.UUID, inDuel bool) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	queued = c.queued
	if c.duelID == nil {
		return queued, uuid.Nil, false
	}
	return queued, *c.duelID, true
}
