//go:build integration

package testkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgresConfig contains the runtime inputs for an integration Postgres
// fixture and its migration helper.
type PostgresConfig struct {
	DSN            string
	MigrationsDir  string
	StartupTimeout time.Duration
}

// StartPostgres starts a disposable container unless config.DSN points at an
// externally managed database. The returned teardown owns the pool and
// container lifecycle for container-backed fixtures.
func StartPostgres(config PostgresConfig) (*pgxpool.Pool, func(), error) {
	if strings.TrimSpace(config.DSN) != "" {
		return StartExternalPostgres(config)
	}
	ctx := context.Background()

	pgC, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("tpm_test"),
		postgres.WithUsername("tpm"),
		postgres.WithPassword("tpm"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(config.StartupTimeout),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start container: %w", err)
	}

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, nil, errors.Join(err, pgC.Terminate(ctx))
	}

	if err := RunMigrations(ctx, PostgresConfig{
		DSN: dsn, MigrationsDir: config.MigrationsDir, StartupTimeout: config.StartupTimeout,
	}); err != nil {
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

	teardown := func() {
		pool.Close()
		termCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = pgC.Terminate(termCtx)
	}
	return pool, teardown, nil
}

// StartExternalPostgres connects to a caller-owned disposable database
// without applying migrations or resetting its state.
func StartExternalPostgres(config PostgresConfig) (*pgxpool.Pool, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), config.StartupTimeout)
	defer cancel()

	poolCfg, err := pgxpool.ParseConfig(config.DSN)
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

// RunMigrations applies the repository migrations to config.DSN.
func RunMigrations(ctx context.Context, config PostgresConfig) error {
	sqlDB, err := sql.Open("pgx", config.DSN)
	if err != nil {
		return fmt.Errorf("open sql.DB: %w", err)
	}
	defer sqlDB.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, sqlDB, config.MigrationsDir); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
