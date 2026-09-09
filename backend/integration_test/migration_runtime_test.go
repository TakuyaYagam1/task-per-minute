//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
)

const migrationMaxStepDuration = 15 * time.Second

func TestMigrationRuntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	pool, database := createMigrationIsolatedDatabase(ctx, t, "duration")
	for version := int64(1); version <= schemaHeadVersion; version++ {
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
