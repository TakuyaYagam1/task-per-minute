//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	testkit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
)

// parallelDatabaseMigrationMu remains available to root migration tests that
// coordinate direct migration calls outside newParallelTestDB.
var parallelDatabaseMigrationMu testkit.MigrationLock

// newParallelTestDB provisions a disposable database for one parallel test.
// Goose keeps its dialect process-global, so migrations are serialized while
// the test databases themselves remain independent and can be used in
// parallel afterwards.
func newParallelTestDB(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	return testkit.NewParallelDatabase(tb, sharedPool, postgresConfig(""))
}

// SetupTestDB starts an isolated Postgres testcontainer, applies migrations,
// truncates all domain tables, and returns the pool with an idempotent cleanup.
func SetupTestDB(tb testing.TB) (*pgxpool.Pool, func()) {
	tb.Helper()

	pool, cleanup, err := startPostgres()
	require.NoError(tb, err)
	TruncateTables(tb, pool)

	var once sync.Once
	wrappedCleanup := func() {
		once.Do(func() {
			TruncateTables(tb, pool)
			cleanup()
		})
	}
	tb.Cleanup(wrappedCleanup)

	return pool, wrappedCleanup
}

// TruncateTables clears all persistent domain tables between isolated cases.
func TruncateTables(tb testing.TB, pool *pgxpool.Pool) {
	tb.Helper()
	require.NoError(tb, truncateTables(context.Background(), pool))
}

func truncateTables(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx,
		`TRUNCATE TABLE tasks, players RESTART IDENTITY CASCADE`,
	)
	return err
}
