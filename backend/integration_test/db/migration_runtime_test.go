//go:build integration

package db_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
)

const (
	migrationRuntimeHeadVersion = 18
	migrationMaxStepDuration    = 15 * time.Second
)

func TestMigrationRuntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	pool, database := testkit.CreateIsolatedDatabase(ctx, t, postgresPool, "duration")
	migrationLock := testkit.MigrationLock{}
	migrationLock.Lock()
	defer migrationLock.Unlock()
	require.NoError(t, goose.SetDialect("postgres"))

	for version := int64(1); version <= migrationRuntimeHeadVersion; version++ {
		startedAt := time.Now()
		require.NoError(t, goose.UpToContext(
			ctx,
			database,
			migrationsDirAbs(),
			version,
		))
		duration := time.Since(startedAt)

		t.Logf("migration version=%06d duration=%s", version, duration)
		require.LessOrEqual(t, duration, migrationMaxStepDuration,
			"domain migration exceeded the reviewed step threshold")
		requireMigrationVersion(ctx, t, pool, version)
	}
}

func migrationsDirAbs() string {
	_, sourceFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(sourceFile), "..", "..", "db", "migrations")
}

func requireMigrationVersion(
	ctx context.Context, tb testing.TB,
	pool *pgxpool.Pool,
	expected int64,
) {
	tb.Helper()

	var actual int64
	require.NoError(tb, pool.QueryRow(ctx, `
		SELECT MAX(version_id)
		FROM goose_db_version
		WHERE is_applied`).Scan(&actual))
	require.Equal(tb, expected, actual)

	var lineage sql.NullString
	require.NoError(tb, pool.QueryRow(ctx, `
		SELECT obj_description('public.goose_db_version'::regclass, 'pg_class')
	`).Scan(&lineage))
	if expected == 0 {
		require.False(tb, lineage.Valid, "full Down must clear the domain marker")
	} else {
		require.Equal(tb, "task-per-minute:domain-schema:v1", lineage.String)
	}
}
