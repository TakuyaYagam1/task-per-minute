//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// SetupTestDB starts an isolated Postgres testcontainer, applies migrations,
// truncates all domain tables, and returns the pool with an idempotent cleanup.
func SetupTestDB(t testing.TB) (*pgxpool.Pool, func()) {
	t.Helper()

	pool, cleanup, err := startPostgres()
	require.NoError(t, err)
	TruncateTables(t, pool)

	var once sync.Once
	wrappedCleanup := func() {
		once.Do(func() {
			TruncateTables(t, pool)
			cleanup()
		})
	}
	t.Cleanup(wrappedCleanup)

	return pool, wrappedCleanup
}

// TruncateTables clears all persistent domain tables between isolated cases.
func TruncateTables(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	require.NoError(t, truncateTables(context.Background(), pool))
}

func truncateTables(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx,
		`TRUNCATE TABLE player_task_history, duel_player_tasks, duels, tasks, players RESTART IDENTITY CASCADE`,
	)
	return err
}
