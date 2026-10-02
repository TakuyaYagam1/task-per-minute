package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	auth "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	count, err := run(context.Background(), os.LookupEnv)
	if err != nil {
		if _, writeErr := fmt.Fprintf(os.Stderr, "test account seeding failed: %s\n", safeError(err)); writeErr != nil {
			os.Exit(1)
		}
		os.Exit(1)
	}
	if count > 0 {
		if _, err := fmt.Fprintf(os.Stdout, "%d test accounts ready\n", count); err != nil {
			os.Exit(1)
		}
	}
}

func run(parent context.Context, lookup func(string) (string, bool)) (int, error) {
	config, enabled, err := loadConfig(lookup)
	if err != nil {
		return 0, err
	}
	if !enabled {
		return 0, nil
	}

	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()

	pool, err := newPool(ctx, config.dsn)
	if err != nil {
		return 0, errDatabaseUnavailable
	}
	defer pool.Close()

	if err := verifyDatabase(ctx, pool, config.expectedDB); err != nil {
		return 0, err
	}

	accounts, err := loadOrCreateManifest(config.manifestPath)
	if err != nil {
		return 0, err
	}

	if err := seedAccounts(ctx, pool, config.manifestPath, &accounts, auth.NewPasswordHasher()); err != nil {
		return 0, err
	}
	return accountCount, nil
}

func safeError(err error) string {
	switch {
	case errors.Is(err, errInvalidConfiguration):
		return "invalid configuration"
	case errors.Is(err, errDatabaseUnavailable):
		return "database unavailable"
	case errors.Is(err, errDatabaseNameMismatch):
		return "database name did not match expected target"
	case errors.Is(err, errManifestNotFound):
		return "manifest is missing"
	case errors.Is(err, errManifestFile):
		return "manifest file is not private or usable"
	case errors.Is(err, errInvalidManifest):
		return "manifest content is invalid"
	case errors.Is(err, errCredentialGeneration):
		return "credential generation failed"
	case errors.Is(err, errIdentityConflict):
		return "account identity collision"
	default:
		return "unexpected failure"
	}
}

func newPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errDatabaseUnavailable
	}
	config.MinConns = 0
	config.MaxConns = 1

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errDatabaseUnavailable
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errDatabaseUnavailable
	}
	return pool, nil
}

func verifyDatabase(ctx context.Context, pool *pgxpool.Pool, expected string) error {
	var actual string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&actual); err != nil {
		return errDatabaseUnavailable
	}
	if actual != expected {
		return errDatabaseNameMismatch
	}
	return nil
}
