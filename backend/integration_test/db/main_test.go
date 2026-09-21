//go:build integration

package db_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var postgresDSN string
var postgresPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, teardown, err := testkit.StartPostgres(testkit.PostgresConfig{
		DSN:            os.Getenv("TPM_TEST_POSTGRES_DSN"),
		MigrationsDir:  filepath.Join("..", "..", "db", "migrations"),
		StartupTimeout: 2 * time.Minute,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration_test/db: failed to start postgres: %v\n", err)
		os.Exit(1)
	}
	postgresPool = pool
	postgresDSN = pool.Config().ConnString()

	code := m.Run()
	teardown()
	os.Exit(code)
}

func resetPostgres(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	truncate := func() {
		_, err := postgresPool.Exec(
			context.Background(),
			`TRUNCATE TABLE tasks, players RESTART IDENTITY CASCADE`,
		)
		require.NoError(tb, err)
	}
	truncate()
	tb.Cleanup(truncate)
	return postgresPool
}
