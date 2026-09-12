//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth/mocks"
)

type databaseFixture struct {
	mgr     *postgres.TxManager
	players *postgres.PlayerPostgres
	tasks   *postgres.TaskPostgres
	board   *postgres.LeaderboardPostgres
}

func newDatabaseFixture(pools ...*pgxpool.Pool) *databaseFixture {
	pool := sharedPool
	if len(pools) > 0 && pools[0] != nil {
		pool = pools[0]
	}
	mgr := postgres.NewTxManager(pool)
	return &databaseFixture{
		mgr:     mgr,
		players: postgres.NewPlayerPostgres(mgr),
		tasks:   postgres.NewTaskPostgres(mgr),
		board:   postgres.NewLeaderboardPostgres(mgr),
	}
}

func (f *databaseFixture) makePlayer(tb testing.TB, name string) *domain.Player {
	tb.Helper()
	player, err := f.players.Create(context.Background(), name)
	require.NoError(tb, err)
	return player
}

func realIntegrationClock() *authmocks.MockClock {
	clock := &authmocks.MockClock{}
	clock.EXPECT().Now().RunAndReturn(func() time.Time { return time.Now().UTC() }).Maybe()
	return clock
}
