//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const containerStartupTimeout = 90 * time.Second

const externalPostgresDSNEnv = "TPM_TEST_POSTGRES_DSN"

func startPostgres() (*pgxpool.Pool, func(), error) {
	if dsn := strings.TrimSpace(os.Getenv(externalPostgresDSNEnv)); dsn != "" {
		return startExternalPostgres(dsn)
	}
	ctx := context.Background()

	pgC, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("tpm_test"),
		postgres.WithUsername("tpm"),
		postgres.WithPassword("tpm"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(containerStartupTimeout),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start container: %w", err)
	}

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, nil, errors.Join(err, pgC.Terminate(ctx))
	}

	if err := runMigrations(ctx, dsn); err != nil {
		return nil, nil, errors.Join(err, pgC.Terminate(ctx))
	}

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, nil, errors.Join(err, pgC.Terminate(ctx))
	}
	poolCfg.MaxConns = 50
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, nil, errors.Join(err, pgC.Terminate(ctx))
	}

	if err := truncateTables(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, errors.Join(err, pgC.Terminate(ctx))
	}

	teardown := func() {
		pool.Close()
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = pgC.Terminate(termCtx)
	}
	return pool, teardown, nil
}

// startExternalPostgres is intentionally non-destructive. The caller owns the
// supplied disposable database; individual integration tests retain their
// normal scoped cleanup rather than TestMain resetting all external state.
func startExternalPostgres(dsn string) (*pgxpool.Pool, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), containerStartupTimeout)
	defer cancel()

	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("parse external postgres configuration: %w", err)
	}
	poolCfg.MaxConns = 50
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("connect external postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("ping external postgres: %w", err)
	}
	return pool, pool.Close, nil
}

func runMigrations(ctx context.Context, dsn string) error {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open sql.DB: %w", err)
	}
	defer sqlDB.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, sqlDB, migrationsDirAbs()); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

func migrationsDirAbs() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "db", "migrations")
}
