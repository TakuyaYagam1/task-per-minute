//go:build integration

package testkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

const (
	// PostgreSQL serializes database catalog changes at cluster scope. Every
	// harness process uses this advisory lock before physical database DDL.
	isolatedDatabaseDDLAdvisoryLockKey     int64 = 0x54504d44444c
	isolatedDatabaseDDLLockTimeout               = 5 * time.Minute
	isolatedDatabaseDDLOperationTimeout          = 5 * time.Minute
	isolatedDatabaseDatabaseSetupTimeout         = 2 * time.Minute
	isolatedDatabaseDDLUnlockTimeout             = 5 * time.Second
	isolatedDatabaseConnectionCloseTimeout       = 5 * time.Second
	// DROP DATABASE waits for PostgreSQL's cluster checkpointer. Keep cleanup
	// bounded, but allow the same operation window as the other physical DDL.
	isolatedDatabaseCleanupOperationTimeout = isolatedDatabaseDDLOperationTimeout
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
	if err != nil {
		require.NoError(tb, err)
		return nil, nil
	}
	err = withDatabaseDDL(ctx, adminConnection, func(operationCtx context.Context) error {
		_, execErr := adminConnection.Exec(operationCtx, "CREATE DATABASE "+identifier+" TEMPLATE "+templateIdentifier)
		return execErr
	})
	if err != nil {
		_ = closeDatabaseDDLConnection(adminConnection)
		require.NoError(tb, err)
		return nil, nil
	}

	var (
		pool     *pgxpool.Pool
		database *sql.DB
	)
	tb.Cleanup(func() {
		if database != nil {
			_ = database.Close()
		}
		if pool != nil {
			pool.Close()
		}

		defer func() { _ = closeDatabaseDDLConnection(adminConnection) }()
		dropErr := dropIsolatedDatabase(ctx, adminConnection, databaseName, identifier)
		require.NoError(tb, dropErr)
	})

	config := adminPool.Config().Copy()
	config.ConnConfig.Database = databaseName
	config.MaxConns = 10
	database = stdlib.OpenDB(*config.ConnConfig)
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	setupCtx, setupCancel := context.WithTimeout(context.Background(), isolatedDatabaseDatabaseSetupTimeout)
	defer setupCancel()
	require.NoError(tb, database.PingContext(setupCtx))

	pool, err = pgxpool.NewWithConfig(setupCtx, config)
	require.NoError(tb, err)
	require.NoError(tb, pool.Ping(setupCtx))
	return pool, database
}

func withDatabaseDDL(
	ctx context.Context,
	connection *pgx.Conn,
	operation func(context.Context) error,
) error {
	return withDatabaseDDLTimeouts(
		ctx,
		connection,
		isolatedDatabaseDDLLockTimeout,
		isolatedDatabaseDDLOperationTimeout,
		operation,
	)
}

func withDatabaseCleanupDDL(
	parent context.Context,
	connection *pgx.Conn,
	operation func(context.Context) error,
) error {
	return withDatabaseDDLTimeouts(
		context.WithoutCancel(parent),
		connection,
		isolatedDatabaseDDLLockTimeout,
		isolatedDatabaseCleanupOperationTimeout,
		operation,
	)
}

func withDatabaseDDLTimeouts(
	ctx context.Context,
	connection *pgx.Conn,
	lockTimeout time.Duration,
	operationTimeout time.Duration,
	operation func(context.Context) error,
) (retErr error) {
	lockCtx, lockCancel := context.WithTimeout(ctx, lockTimeout)
	if err := acquireDatabaseAdvisoryLock(lockCtx, connection, isolatedDatabaseDDLAdvisoryLockKey); err != nil {
		lockCancel()
		return fmt.Errorf("acquire isolated database DDL lock: %w", err)
	}
	lockCancel()
	defer func() {
		retErr = errors.Join(retErr, releaseDatabaseAdvisoryLock(connection, isolatedDatabaseDDLAdvisoryLockKey))
	}()

	operationCtx, operationCancel := context.WithTimeout(ctx, operationTimeout)
	defer operationCancel()
	if err := operation(operationCtx); err != nil {
		return fmt.Errorf("isolated database DDL operation: %w", err)
	}
	return nil
}

func dropIsolatedDatabase(ctx context.Context, connection *pgx.Conn, databaseName, identifier string) error {
	return withDatabaseCleanupDDL(ctx, connection, func(operationCtx context.Context) error {
		if _, err := connection.Exec(operationCtx, `
			SELECT pg_terminate_backend(pid)
			FROM pg_stat_activity
			WHERE datname = $1 AND pid <> pg_backend_pid()`, databaseName); err != nil {
			return fmt.Errorf("terminate isolated database sessions: %w", err)
		}
		if _, err := connection.Exec(operationCtx, "DROP DATABASE "+identifier+" WITH (FORCE)"); err != nil {
			return fmt.Errorf("drop isolated database: %w", err)
		}
		return nil
	})
}

func acquireDatabaseAdvisoryLock(ctx context.Context, connection *pgx.Conn, key int64) error {
	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		return errors.Join(err, closeDatabaseDDLConnection(connection))
	}
	return nil
}

func releaseDatabaseAdvisoryLock(connection *pgx.Conn, key int64) error {
	unlockCtx, cancel := context.WithTimeout(context.Background(), isolatedDatabaseDDLUnlockTimeout)
	defer cancel()
	var unlocked bool
	if err := connection.QueryRow(unlockCtx, "SELECT pg_advisory_unlock($1)", key).Scan(&unlocked); err != nil {
		return errors.Join(fmt.Errorf("release database advisory lock: %w", err), closeDatabaseDDLConnection(connection))
	}
	if !unlocked {
		return errors.Join(errors.New("release database advisory lock: lock was not held"), closeDatabaseDDLConnection(connection))
	}
	return nil
}

func closeDatabaseDDLConnection(connection *pgx.Conn) error {
	if connection == nil {
		return nil
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), isolatedDatabaseConnectionCloseTimeout)
	defer cancel()
	return connection.Close(closeCtx)
}
