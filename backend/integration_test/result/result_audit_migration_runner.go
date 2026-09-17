//go:build integration

package result

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var (
	migrationPool              *pgxpool.Pool
	resultAuditMigrationPoolMu sync.Mutex
)

// RunResultAuditMigration runs the moved result audit migration assertions
// against the caller-owned pool.
func RunResultAuditMigration(t *testing.T, pool *pgxpool.Pool) {
	bindResultAuditMigrationPool(t, pool, runResultAuditMigration)
}

// RunResultAuditMigrationCommitLocks runs the lock-order assertions against
// the caller-owned pool.
func RunResultAuditMigrationCommitLocks(t *testing.T, pool *pgxpool.Pool) {
	bindResultAuditMigrationPool(t, pool, runResultAuditMigrationCommitLocks)
}

func bindResultAuditMigrationPool(
	t *testing.T,
	pool *pgxpool.Pool,
	run func(*testing.T),
) {
	t.Helper()
	require.NotNil(t, pool)
	resultAuditMigrationPoolMu.Lock()
	previousPool := migrationPool
	migrationPool = pool
	defer func() {
		migrationPool = previousPool
		resultAuditMigrationPoolMu.Unlock()
	}()
	run(t)
}

func resetMigrationTables(ctx context.Context, tb testing.TB) {
	tb.Helper()
	require.NoError(tb, resetResultTables(ctx, migrationPool))
}
