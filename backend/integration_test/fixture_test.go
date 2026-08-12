//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type duelFixture struct {
	pool    *pgxpool.Pool
	mgr     *postgres.TxManager
	players *postgres.PlayerPostgres
	tasks   *postgres.TaskPostgres
	duels   *postgres.DuelPostgres
	history *postgres.HistoryPostgres
	board   *postgres.LeaderboardPostgres
}

func newDuelFixture() *duelFixture {
	return newDuelFixtureWithPool(sharedPool)
}

func newIsolatedDuelFixture(t testing.TB) *duelFixture {
	t.Helper()
	pool, _ := SetupTestDB(t)
	return newDuelFixtureWithPool(pool)
}

func newDuelFixtureWithPool(pool *pgxpool.Pool) *duelFixture {
	mgr := postgres.NewTxManager(pool)
	return &duelFixture{
		pool:    pool,
		mgr:     mgr,
		players: postgres.NewPlayerPostgres(mgr),
		tasks:   postgres.NewTaskPostgres(mgr),
		duels:   postgres.NewDuelPostgres(mgr),
		history: postgres.NewHistoryPostgres(mgr),
		board:   postgres.NewLeaderboardPostgres(mgr),
	}
}

func (f *duelFixture) makePlayer(t testing.TB, name string) *domain.Player {
	t.Helper()
	player, err := f.players.Create(context.Background(), name)
	require.NoError(t, err)
	return player
}
