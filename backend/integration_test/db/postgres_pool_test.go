//go:build integration

package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	postgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestPostgresPoolNew_AppliesFacadeConfiguration(t *testing.T) {
	dsn := isolatedPostgresDSN(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const maxConns int32 = 4
	pool, err := postgres.New(ctx, postgres.Config{DSN: dsn, MaxConns: maxConns})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	poolConfig := pool.Config()
	require.Equal(t, maxConns, poolConfig.MaxConns)
	require.Equal(t, int32(2), poolConfig.MinConns)
	require.Equal(t, 30*time.Minute, poolConfig.MaxConnLifetime)
	require.Equal(t, 5*time.Minute, poolConfig.MaxConnIdleTime)
	require.NoError(t, postgres.HealthCheck(ctx, pool))
}

func TestPostgresPoolHealthCheck_AfterCloseHasStableOperationPrefix(t *testing.T) {
	dsn := isolatedPostgresDSN(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := postgres.New(ctx, postgres.Config{DSN: dsn, MaxConns: 2})
	require.NoError(t, err)
	pool.Close()

	err = postgres.HealthCheck(ctx, pool)
	require.Error(t, err)
	require.Truef(t,
		strings.HasPrefix(err.Error(), "postgres - HealthCheck - Pool.Ping:"),
		"unexpected HealthCheck operation prefix: %v",
		err,
	)
}

func TestPostgresPoolNew_NonPositiveMaxConnsUsesDriverDefault(t *testing.T) {
	dsn := isolatedPostgresDSN(t)

	driverConfig, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)

	for _, testCase := range []struct {
		name     string
		maxConns int32
	}{
		{name: "zero", maxConns: 0},
		{name: "negative", maxConns: -1},
	} {
		t.Run("max_conns="+testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			pool, err := postgres.New(ctx, postgres.Config{DSN: dsn, MaxConns: testCase.maxConns})
			require.NoError(t, err)
			t.Cleanup(pool.Close)

			require.Equal(t, driverConfig.MaxConns, pool.Config().MaxConns)
		})
	}
}

func isolatedPostgresDSN(t *testing.T) string {
	t.Helper()
	return postgresDSN
}
