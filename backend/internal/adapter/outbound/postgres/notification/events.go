package notification

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	playerNotificationsChannel          = "player_notifications_changed"
	playerNotificationReconnectInterval = time.Second
	maxPlayerNotificationSubscribers    = 2048
	maxPlayerNotificationStreamsPerUser = 2
	playerNotificationEventBuffer       = 1
)

type EventsPostgres struct {
	pool *pgxpool.Pool

	mu              sync.Mutex
	subscribers     map[uuid.UUID]map[chan struct{}]struct{}
	subscriberCount int
	ready           atomic.Bool
	running         atomic.Bool
}

func NewEventsPostgres(pool *pgxpool.Pool) *EventsPostgres {
	return &EventsPostgres{
		pool:        pool,
		subscribers: make(map[uuid.UUID]map[chan struct{}]struct{}),
	}
}

// SubscribePlayerNotifications registers an in-memory invalidation channel.
// PostgreSQL connections are owned by the single process-level Run listener.
func (r *EventsPostgres) SubscribePlayerNotifications(
	ctx context.Context,
	playerID uuid.UUID,
) (<-chan struct{}, func(), error) {
	if r == nil || r.pool == nil {
		return nil, nil, errors.New("player notification events postgres: pool unavailable")
	}
	if playerID == uuid.Nil {
		return nil, nil, errors.New("player notification events postgres: player ID required")
	}
	if ctx == nil {
		return nil, nil, errors.New("player notification events postgres: context unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !r.ready.Load() {
		return nil, nil, errors.New("player notification events postgres: listener is not ready")
	}

	channel := make(chan struct{}, playerNotificationEventBuffer)
	r.mu.Lock()
	if !r.ready.Load() {
		r.mu.Unlock()
		return nil, nil, errors.New("player notification events postgres: listener is not ready")
	}
	if r.subscriberCount >= maxPlayerNotificationSubscribers {
		r.mu.Unlock()
		return nil, nil, errors.New("player notification events postgres: subscriber limit reached")
	}
	playerSubscribers := r.subscribers[playerID]
	if len(playerSubscribers) >= maxPlayerNotificationStreamsPerUser {
		r.mu.Unlock()
		return nil, nil, errors.New("player notification events postgres: player stream limit reached")
	}
	if playerSubscribers == nil {
		playerSubscribers = make(map[chan struct{}]struct{}, maxPlayerNotificationStreamsPerUser)
		r.subscribers[playerID] = playerSubscribers
	}
	playerSubscribers[channel] = struct{}{}
	r.subscriberCount++
	r.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			r.removeSubscriber(playerID, channel)
		})
	}
	return channel, unsubscribe, nil
}

// Run holds one dedicated LISTEN connection for the process and fans out
// coalesced invalidations to locally connected players.
func (r *EventsPostgres) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("player notification events postgres: context unavailable")
	}
	if r == nil || r.pool == nil {
		return errors.New("player notification events postgres: pool unavailable")
	}
	if !r.running.CompareAndSwap(false, true) {
		return errors.New("player notification events postgres: listener already running")
	}
	defer r.running.Store(false)

	for ctx.Err() == nil {
		if err := r.listen(ctx); err != nil && ctx.Err() == nil {
			timer := time.NewTimer(playerNotificationReconnectInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	}
	return nil
}

func (r *EventsPostgres) Ready() bool {
	return r != nil && r.ready.Load()
}

func (r *EventsPostgres) listen(ctx context.Context) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("player notification events postgres: acquire listener connection: %w", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+playerNotificationsChannel); err != nil {
		cleanupPlayerNotificationConnection(ctx, conn)
		return fmt.Errorf("player notification events postgres: listen: %w", err)
	}

	r.ready.Store(true)
	r.publishAll()
	defer func() {
		r.ready.Store(false)
		cleanupPlayerNotificationConnection(ctx, conn)
	}()

	for {
		notification, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("player notification events postgres: wait for notification: %w", err)
		}
		if notification.Channel != playerNotificationsChannel {
			continue
		}
		playerID, err := uuid.Parse(notification.Payload)
		if err != nil || playerID == uuid.Nil {
			continue
		}
		r.publish(playerID)
	}
}

func (r *EventsPostgres) publish(playerID uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for subscriber := range r.subscribers[playerID] {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}

func (r *EventsPostgres) publishAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, subscribers := range r.subscribers {
		for subscriber := range subscribers {
			select {
			case subscriber <- struct{}{}:
			default:
			}
		}
	}
}

func (r *EventsPostgres) removeSubscriber(playerID uuid.UUID, subscriber chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	playerSubscribers := r.subscribers[playerID]
	if _, ok := playerSubscribers[subscriber]; !ok {
		return
	}
	delete(playerSubscribers, subscriber)
	r.subscriberCount--
	if len(playerSubscribers) == 0 {
		delete(r.subscribers, playerID)
	}
	close(subscriber)
}

func cleanupPlayerNotificationConnection(ctx context.Context, conn *pgxpool.Conn) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if _, err := conn.Exec(cleanupCtx, "UNLISTEN "+playerNotificationsChannel); err != nil {
		_ = conn.Hijack().Close(cleanupCtx)
		return
	}
	conn.Release()
}
