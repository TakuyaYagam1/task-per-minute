//go:build integration

package projection_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	testkit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/jackc/pgx/v5/pgxpool"
)

const containerStartupTimeout = 90 * time.Second

const externalPostgresDSNEnv = "TPM_TEST_POSTGRES_DSN"

var sharedPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, teardown, err := startPostgres()
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration_test/projection: failed to start postgres: %v\n", err)
		os.Exit(1)
	}
	sharedPool = pool

	code := m.Run()
	teardown()
	os.Exit(code)
}

func startPostgres() (*pgxpool.Pool, func(), error) {
	if dsn := strings.TrimSpace(os.Getenv(externalPostgresDSNEnv)); dsn != "" {
		return startExternalPostgres(dsn)
	}

	pool, teardown, err := testkit.StartPostgres(postgresConfig(""))
	if err != nil {
		return nil, nil, err
	}
	if _, err := pool.Exec(context.Background(), `TRUNCATE TABLE tasks, players RESTART IDENTITY CASCADE`); err != nil {
		teardown()
		return nil, nil, err
	}
	return pool, teardown, nil
}

func startExternalPostgres(dsn string) (*pgxpool.Pool, func(), error) {
	return testkit.StartExternalPostgres(postgresConfig(dsn))
}

func postgresConfig(dsn string) testkit.PostgresConfig {
	return testkit.PostgresConfig{
		DSN: dsn, MigrationsDir: migrationsDirAbs(), StartupTimeout: containerStartupTimeout,
	}
}

func migrationsDirAbs() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "db", "migrations")
}
