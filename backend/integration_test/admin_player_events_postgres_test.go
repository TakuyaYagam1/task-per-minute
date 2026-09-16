//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	playerrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
)

func TestAdminPlayerEventsPostgres_NotifiesOnPlayerListChanges(t *testing.T) {
	pool, _ := SetupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	eventsRepo := playerrepo.NewAdminPlayerEventsPostgres(pool)
	events, unsubscribe, err := eventsRepo.SubscribeAdminPlayerChanges(ctx)
	require.NoError(t, err)
	defer unsubscribe()

	tx := postgres.NewTxManager(pool)
	players := postgres.NewPlayerPostgres(tx)

	player, err := players.Create(ctx, uniq("events_player"))
	require.NoError(t, err)
	requireAdminPlayerEvent(t, events)

	err = players.UpdateUsername(ctx, player.ID, uniq("events_renamed"))
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
