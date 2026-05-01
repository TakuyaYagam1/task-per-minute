//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent"
)

type duelFixture struct {
	pool    *pgxpool.Pool
	mgr     *persistent.TxManager
	players *persistent.PlayerPostgres
	tasks   *persistent.TaskPostgres
	duels   *persistent.DuelPostgres
	history *persistent.HistoryPostgres
	board   *persistent.LeaderboardPostgres
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
	mgr := persistent.NewTxManager(pool)
	return &duelFixture{
		pool:    pool,
		mgr:     mgr,
		players: persistent.NewPlayerPostgres(mgr),
		tasks:   persistent.NewTaskPostgres(mgr),
		duels:   persistent.NewDuelPostgres(mgr),
		history: persistent.NewHistoryPostgres(mgr),
		board:   persistent.NewLeaderboardPostgres(mgr),
	}
}

func (f *duelFixture) makePlayer(t testing.TB, name string) *domain.Player {
	t.Helper()
	player, err := f.players.Create(context.Background(), name)
	require.NoError(t, err)
	return player
}
