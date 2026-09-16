//go:build integration

package testkit

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/leaderboard"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/task"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// DatabaseFixture bundles the repositories shared by integration tests that
// build players, tasks, and leaderboard state directly against PostgreSQL.
type DatabaseFixture struct {
	Manager *postgres.TxManager
	Players *player.PlayerPostgres
	Tasks   *task.TaskPostgres
	Board   *leaderboard.LeaderboardPostgres
}

// NewDatabaseFixture constructs the repository bundle for an already selected
// integration pool. Pool selection remains with the root integration package.
func NewDatabaseFixture(pool *pgxpool.Pool) *DatabaseFixture {
	mgr := postgres.NewTxManager(pool)
	return &DatabaseFixture{
		Manager: mgr,
		Players: player.NewPlayerPostgres(mgr),
		Tasks:   task.NewTaskPostgres(mgr),
		Board:   leaderboard.NewLeaderboardPostgres(mgr),
	}
}

// MakePlayer creates one player through the fixture repository and preserves
// the integration test assertion behavior of the original helper.
func MakePlayer(tb testing.TB, players *player.PlayerPostgres, name string) *domain.Player {
	tb.Helper()
	player, err := players.Create(context.Background(), name)
	require.NoError(tb, err)
	return player
}
