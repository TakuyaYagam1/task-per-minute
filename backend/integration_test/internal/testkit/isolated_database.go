//go:build integration

package testkit

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

var parallelDatabaseMigrationMu sync.Mutex

// MigrationLock exposes the process-wide migration lock to compatibility
// callers that still coordinate direct goose operations.
type MigrationLock struct{}

func (MigrationLock) Lock() {
	parallelDatabaseMigrationMu.Lock()
}

func (MigrationLock) Unlock() {
	parallelDatabaseMigrationMu.Unlock()
}

// NewParallelDatabase provisions one isolated database from the immutable
// migration template and registers cleanup for the pool and database.
func NewParallelDatabase(tb testing.TB, adminPool *pgxpool.Pool, config PostgresConfig) *pgxpool.Pool {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), config.StartupTimeout)
	defer cancel()

	templateName, err := migrationTemplateDatabaseName(config.MigrationsDir)
	require.NoError(tb, err)
	pool, _ := createIsolatedDatabase(ctx, tb, adminPool, "parallel", templateName)
	return pool
}

// MigrationDSN selects an isolated database while retaining the connection
// options from the administrative pool and setting the requested search path.
func MigrationDSN(tb testing.TB, pool *pgxpool.Pool, searchPath string) string {
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

// CreateIsolatedDatabase creates a disposable database from the supplied
// administrative pool and registers cleanup with the test lifecycle.
func CreateIsolatedDatabase(
	ctx context.Context, tb testing.TB,
	adminPool *pgxpool.Pool,
	label string,
) (*pgxpool.Pool, *sql.DB) {
	tb.Helper()
	return createIsolatedDatabase(ctx, tb, adminPool, label, "template0")
}

func createIsolatedDatabase(
	ctx context.Context, tb testing.TB,
	adminPool *pgxpool.Pool,
	label string,
	templateName string,
) (*pgxpool.Pool, *sql.DB) {
	tb.Helper()

	databaseName := "schema_migration_" + label + "_" + uuid.NewString()[:16]
	identifier := pgx.Identifier{databaseName}.Sanitize()
	templateIdentifier := pgx.Identifier{templateName}.Sanitize()
	adminConfig := adminPool.Config().ConnConfig.Copy()
	adminConfig.Database = "postgres"
	adminConnection, err := pgx.ConnectConfig(ctx, adminConfig)
	require.NoError(tb, err)
	_, err = adminConnection.Exec(ctx, "CREATE DATABASE "+identifier+" TEMPLATE "+templateIdentifier)
	if err != nil {
		_ = adminConnection.Close(context.WithoutCancel(ctx))
	}
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

		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, dropErr := adminConnection.Exec(cleanupCtx, "DROP DATABASE "+identifier+" WITH (FORCE)")
		_ = adminConnection.Close(cleanupCtx)
		require.NoError(tb, dropErr)
	})

	config := adminPool.Config().Copy()
	config.ConnConfig.Database = databaseName
	config.MaxConns = 10
	database = stdlib.OpenDB(*config.ConnConfig)
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	require.NoError(tb, database.PingContext(ctx))

	pool, err = pgxpool.NewWithConfig(ctx, config)
	require.NoError(tb, err)
	require.NoError(tb, pool.Ping(ctx))
	return pool, database
}
