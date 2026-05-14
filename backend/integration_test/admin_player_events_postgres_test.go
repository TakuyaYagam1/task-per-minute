//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent"
)

func TestAdminPlayerEventsPostgres_NotifiesOnPlayerListChanges(t *testing.T) {
	pool, _ := SetupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	eventsRepo := persistent.NewAdminPlayerEventsPostgres(pool)
	events, unsubscribe, err := eventsRepo.SubscribeAdminPlayerChanges(ctx)
	require.NoError(t, err)
	defer unsubscribe()

	tx := persistent.NewTxManager(pool)
	players := persistent.NewPlayerPostgres(tx)

	player, err := players.Create(ctx, uniq("events_player"))
	require.NoError(t, err)
	requireAdminPlayerEvent(t, events)

	_, err = players.UpdateStatus(ctx, player.ID, domain.PlayerStatusQueued)
	require.NoError(t, err)
	requireAdminPlayerEvent(t, events)

	_, err = pool.Exec(ctx, `
		INSERT INTO player_leaderboard_overrides (player_id, wins, average_solve_time_ms)
		VALUES ($1, 1, 1000)
	`, player.ID)
	require.NoError(t, err)
	requireAdminPlayerEvent(t, events)
}

func requireAdminPlayerEvent(t *testing.T, events <-chan struct{}) {
	t.Helper()

	require.Eventually(t, func() bool {
		select {
		case _, ok := <-events:
			return ok
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond)
}
