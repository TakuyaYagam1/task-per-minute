package websocket

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/google/uuid"
	wskit "github.com/wahrwelt-kit/go-wskit"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	defaultWriteWait      = 10 * time.Second
	defaultPingInterval   = 30 * time.Second
	defaultSendBufferSize = 32
	defaultReadLimit      = 16 * 1024
)

type client struct {
	player       *domain.Player
	sessionToken uuid.UUID
	conn         *coderws.Conn
	send         chan []byte
	done         chan struct{}

	closeOnce sync.Once
	closed    atomic.Bool

	stateMu sync.RWMutex
	queued  bool
	duelID  *uuid.UUID
}

var _ wskit.Subscriber = (*client)(nil)

func newClient(player *domain.Player, conn *coderws.Conn) *client {
	conn.SetReadLimit(defaultReadLimit)
	var sessionToken uuid.UUID
	if player != nil && player.SessionToken != nil {
		sessionToken = *player.SessionToken
	}
	return &client{
		player:       player,
		sessionToken: sessionToken,
		conn:         conn,
		send:         make(chan []byte, defaultSendBufferSize),
		done:         make(chan struct{}),
	}
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
		c.Close()
		return false
	}
}

func (c *client) Close() {
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.done)
		go func() {
			_ = c.conn.Close(coderws.StatusNormalClosure, "")
		}()
	})
}

func (c *client) writePump(ctx context.Context) {
	ticker := time.NewTicker(defaultPingInterval)
	defer func() {
		ticker.Stop()
		c.Close()
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
	c.Send(data)
	return nil
}

func (c *client) sendError(code, message string) error {
	data, err := marshalError(code, message)
	if err != nil {
		return err
	}
	c.Send(data)
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
