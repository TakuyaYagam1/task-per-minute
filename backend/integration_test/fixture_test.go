//go:build integration

package integration_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	testkit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/leaderboard"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/task"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth/mocks"
)

type databaseFixture struct {
	mgr     *postgres.TxManager
	players *player.PlayerPostgres
	tasks   *task.TaskPostgres
	board   *leaderboard.LeaderboardPostgres
}

func newDatabaseFixture(pools ...*pgxpool.Pool) *databaseFixture {
	pool := sharedPool
	if len(pools) > 0 && pools[0] != nil {
		pool = pools[0]
	}
	fixture := testkit.NewDatabaseFixture(pool)
	return &databaseFixture{
		mgr:     fixture.Manager,
		players: fixture.Players,
		tasks:   fixture.Tasks,
		board:   fixture.Board,
	}
}

func (f *databaseFixture) makePlayer(tb testing.TB, name string) *domain.Player {
	return testkit.MakePlayer(tb, f.players, name)
}

func realIntegrationClock() *authmocks.MockClock {
	clock := &authmocks.MockClock{}
	clock.EXPECT().Now().RunAndReturn(func() time.Time { return time.Now().UTC() }).Maybe()
	return clock
}
