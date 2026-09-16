//go:build integration

package player_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	testkit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
	authmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth/mocks"
)

type databaseFixture struct {
	mgr     *postgres.TxManager
	players *player.PlayerPostgres
}

func newDatabaseFixture(pools ...*pgxpool.Pool) *databaseFixture {
	pool := sharedPool
	if len(pools) > 0 && pools[0] != nil {
		pool = pools[0]
	}
	fixture := testkit.NewDatabaseFixture(pool)
	return &databaseFixture{mgr: fixture.Manager, players: fixture.Players}
}

func realIntegrationClock() *authmocks.MockClock {
	clock := &authmocks.MockClock{}
	clock.EXPECT().Now().RunAndReturn(func() time.Time { return time.Now().UTC() }).Maybe()
	return clock
}

func resetMigrationTables(ctx context.Context, tb testing.TB) {
	tb.Helper()
	_, err := sharedPool.Exec(ctx, `TRUNCATE TABLE participant_reservations, tournaments CASCADE`)
	require.NoError(tb, err)
}

func createMigrationTournament(ctx context.Context, tb testing.TB) uuid.UUID {
	tb.Helper()
	var id uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO tournaments DEFAULT VALUES
		RETURNING id`).Scan(&id)
	require.NoError(tb, err)
	return id
}
