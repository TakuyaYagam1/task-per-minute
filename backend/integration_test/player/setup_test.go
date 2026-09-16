//go:build integration

package player_test

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	testkit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
)

var parallelDatabaseMigrationMu sync.Mutex

func newParallelTestDB(tb testing.TB) *pgxpool.Pool {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), containerStartupTimeout)
	defer cancel()

	pool, _ := createMigrationIsolatedDatabase(ctx, tb, "parallel")
	parallelDatabaseMigrationMu.Lock()
	err := func() error {
		defer parallelDatabaseMigrationMu.Unlock()
		return runMigrations(ctx, migrationDSN(tb, pool, "public"))
	}()
	require.NoError(tb, err)
	return pool
}

func runMigrations(ctx context.Context, dsn string) error {
	return testkit.RunMigrations(ctx, postgresConfig(dsn))
}

func migrationDSN(tb testing.TB, pool *pgxpool.Pool, searchPath string) string {
	tb.Helper()
	config := pool.Config()
	dsn := config.ConnString()
	// ConnString retains the original parsed DSN after Config.Database changes.
	// Always select the isolated database explicitly, retaining connection options.
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		require.NoError(tb, err)
		parsed.Path = "/" + config.ConnConfig.Database
		query := parsed.Query()
		query.Del("dbname")
		query.Del("database")
		query.Set("search_path", searchPath)
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	quote := func(value string) string {
		return "'" + strings.NewReplacer(`\`, `\\`, "'", `\'`).Replace(value) + "'"
	}
	return dsn + " dbname=" + quote(config.ConnConfig.Database) + " search_path=" + quote(searchPath)
}

func createMigrationIsolatedDatabase(
	ctx context.Context, tb testing.TB,
	label string,
) (*pgxpool.Pool, *sql.DB) {
	tb.Helper()

	adminPool := sharedPool
	databaseName := uniq("schema_migration_" + label)
	identifier := pgx.Identifier{databaseName}.Sanitize()
	_, err := adminPool.Exec(ctx, "CREATE DATABASE "+identifier+" TEMPLATE template0")
	require.NoError(tb, err)

	var (
		pool     *pgxpool.Pool
		database *sql.DB
	)
	tb.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		if database != nil {
			_ = database.Close()
		}

		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_, dropErr := adminPool.Exec(cleanupCtx, "DROP DATABASE "+identifier+" WITH (FORCE)")
		require.NoError(tb, dropErr)
	})

	config := adminPool.Config().Copy()
	config.ConnConfig.Database = databaseName
	database = stdlib.OpenDB(*config.ConnConfig)
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	require.NoError(tb, database.PingContext(ctx))

	pool, err = pgxpool.NewWithConfig(ctx, config)
	require.NoError(tb, err)
	require.NoError(tb, pool.Ping(ctx))
	return pool, database
}
