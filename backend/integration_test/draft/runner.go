//go:build integration

package draft

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var (
	migrationPool   *pgxpool.Pool
	migrationPoolMu sync.Mutex
)

// RunDraftMigration runs the moved draft migration assertions against the
// caller-owned integration pool.
func RunDraftMigration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	require.NotNil(t, pool)
	migrationPoolMu.Lock()
	previousPool := migrationPool
	migrationPool = pool
	t.Cleanup(func() {
		migrationPool = previousPool
		migrationPoolMu.Unlock()
	})

	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	runDraftMigration(t)
}

func resetMigrationTables(ctx context.Context, tb testing.TB) {
	tb.Helper()
	_, err := migrationPool.Exec(ctx, `
		TRUNCATE TABLE participant_reservations, tournaments CASCADE`)
	require.NoError(tb, err)
}
