//go:build integration

package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	testkit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

const containerStartupTimeout = 90 * time.Second

const externalPostgresDSNEnv = "TPM_TEST_POSTGRES_DSN"

func startPostgres() (*pgxpool.Pool, func(), error) {
	if dsn := strings.TrimSpace(os.Getenv(externalPostgresDSNEnv)); dsn != "" {
		return startExternalPostgres(dsn)
	}
	pool, teardown, err := testkit.StartPostgres(postgresConfig(""))
	if err != nil {
		return nil, nil, err
	}
	if err := truncateTables(context.Background(), pool); err != nil {
		teardown()
		return nil, nil, err
	}
	return pool, teardown, nil
}

// startExternalPostgres is intentionally non-destructive. The caller owns the
// supplied disposable database; individual integration tests retain their
// normal scoped cleanup rather than TestMain resetting all external state.
func startExternalPostgres(dsn string) (*pgxpool.Pool, func(), error) {
	return testkit.StartExternalPostgres(postgresConfig(dsn))
}

func runMigrations(ctx context.Context, dsn string) error {
	return testkit.RunMigrations(ctx, postgresConfig(dsn))
}

func postgresConfig(dsn string) testkit.PostgresConfig {
	return testkit.PostgresConfig{
		DSN: dsn, MigrationsDir: migrationsDirAbs(), StartupTimeout: containerStartupTimeout,
	}
}

func migrationsDirAbs() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "db", "migrations")
}
