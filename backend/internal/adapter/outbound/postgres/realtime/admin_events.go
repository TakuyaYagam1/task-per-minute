package realtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	adminChangesChannel        = "admin_changes"
	adminPlayersChangedChannel = "admin_players_changed"
	adminEventsBufferSize      = 3

	adminEventTopicPlayers     = "players"
	adminEventTopicTasks       = "tasks"
	adminEventTopicTournaments = "tournaments"
)

// AdminEventsPostgres subscribes to the small allowlisted invalidation stream
// used by authenticated admin dashboards. It never forwards notification
// payloads to the HTTP layer.
type AdminEventsPostgres struct {
	pool *pgxpool.Pool
}

func NewAdminEventsPostgres(pool *pgxpool.Pool) *AdminEventsPostgres {
	return &AdminEventsPostgres{pool: pool}
}

func (r *AdminEventsPostgres) SubscribeAdminChanges(
	ctx context.Context,
) (<-chan string, func(), error) {
	if r == nil || r.pool == nil {
		return nil, nil, errors.New("admin events postgres: nil pool")
	}

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("AdminEventsPostgres - SubscribeAdminChanges - Pool.Acquire: %w", err)
	}

	if _, err := conn.Exec(ctx, "LISTEN "+adminChangesChannel); err != nil {
		cleanupAdminEventsConnection(ctx, conn)
		return nil, nil, fmt.Errorf("AdminEventsPostgres - SubscribeAdminChanges - LISTEN admin_changes: %w", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+adminPlayersChangedChannel); err != nil {
		cleanupAdminEventsConnection(ctx, conn)
		return nil, nil, fmt.Errorf("AdminEventsPostgres - SubscribeAdminChanges - LISTEN admin_players_changed: %w", err)
	}

	listenCtx, cancel := context.WithCancel(ctx)
	queue := newAdminEventQueue()
	events := make(chan string, adminEventsBufferSize)
	go readAdminEvents(listenCtx, ctx, conn, queue, cancel)
	go forwardAdminEvents(listenCtx, queue, events)

	return events, cancel, nil
}

type adminEventQueue struct {
	mu      sync.Mutex
	pending map[string]struct{}
	wake    chan struct{}
}

func newAdminEventQueue() *adminEventQueue {
	return &adminEventQueue{
		pending: make(map[string]struct{}, adminEventsBufferSize),
		wake:    make(chan struct{}, 1),
	}
}

func (q *adminEventQueue) add(topic string) {
	q.mu.Lock()
	q.pending[topic] = struct{}{}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *adminEventQueue) drain(queued map[string]struct{}, queue []string) []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	for topic := range q.pending {
		if _, exists := queued[topic]; !exists {
			queued[topic] = struct{}{}
			queue = append(queue, topic)
		}
		delete(q.pending, topic)
	}
	return queue
}

func readAdminEvents(
	listenCtx context.Context,
	parentCtx context.Context,
	conn *pgxpool.Conn,
	queue *adminEventQueue,
	cancel context.CancelFunc,
) {
	defer func() {
		cleanupAdminEventsConnection(parentCtx, conn)
	}()
	defer cancel()

	for {
		notification, err := conn.Conn().WaitForNotification(listenCtx)
		if err != nil {
			return
		}
		topic, ok := adminEventTopic(notification.Channel, notification.Payload)
		if ok {
			queue.add(topic)
		}
	}
}

func forwardAdminEvents(ctx context.Context, queue *adminEventQueue, events chan string) {
	defer close(events)

	queued := make(map[string]struct{}, adminEventsBufferSize)
	pending := make([]string, 0, adminEventsBufferSize)
	for {
		var output chan string
		var next string
		if len(pending) > 0 {
			output = events
			next = pending[0]
		}

		select {
		case <-queue.wake:
			pending = queue.drain(queued, pending)
		case output <- next:
			delete(queued, next)
			pending = pending[1:]
		case <-ctx.Done():
			return
		}
	}
}

func adminEventTopic(channel, payload string) (string, bool) {
	if channel == adminPlayersChangedChannel {
		return adminEventTopicPlayers, true
	}
	if channel != adminChangesChannel {
		return "", false
	}

	switch payload {
	case adminEventTopicPlayers, adminEventTopicTasks, adminEventTopicTournaments:
		return payload, true
	default:
		return "", false
	}
}

func cleanupAdminEventsConnection(ctx context.Context, conn *pgxpool.Conn) {
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cleanupCancel()
	_, adminChangesErr := conn.Exec(cleanupCtx, "UNLISTEN "+adminChangesChannel)
	_, adminPlayersErr := conn.Exec(cleanupCtx, "UNLISTEN "+adminPlayersChangedChannel)
	if adminChangesErr != nil || adminPlayersErr != nil {
		_ = conn.Hijack().Close(cleanupCtx)
		return
	}
	conn.Release()
}
