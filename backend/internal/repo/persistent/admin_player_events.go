package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const adminPlayersChangedChannel = "admin_players_changed"

type AdminPlayerEventsPostgres struct {
	pool *pgxpool.Pool
}

func NewAdminPlayerEventsPostgres(pool *pgxpool.Pool) *AdminPlayerEventsPostgres {
	return &AdminPlayerEventsPostgres{pool: pool}
}

func (r *AdminPlayerEventsPostgres) SubscribeAdminPlayerChanges(
	ctx context.Context,
) (<-chan struct{}, func(), error) {
	if r == nil || r.pool == nil {
		return nil, nil, errors.New("admin player events postgres: nil pool")
	}

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("AdminPlayerEventsPostgres - SubscribeAdminPlayerChanges - Pool.Acquire: %w", err)
	}

	if _, err := conn.Exec(ctx, "LISTEN "+adminPlayersChangedChannel); err != nil {
		conn.Release()
		return nil, nil, fmt.Errorf("AdminPlayerEventsPostgres - SubscribeAdminPlayerChanges - Conn.Exec: %w", err)
	}

	listenCtx, cancel := context.WithCancel(ctx)
	events := make(chan struct{}, 1)
	go func() {
		defer close(events)
		defer func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cleanupCancel()
			_, _ = conn.Exec(cleanupCtx, "UNLISTEN "+adminPlayersChangedChannel)
			conn.Release()
		}()

		for {
			notification, err := conn.Conn().WaitForNotification(listenCtx)
			if err != nil {
				return
			}
			if notification.Channel != adminPlayersChangedChannel {
				continue
			}
			select {
			case events <- struct{}{}:
			default:
			}
		}
	}()

	return events, cancel, nil
}
