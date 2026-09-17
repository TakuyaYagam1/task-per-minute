//go:build integration

package assignment

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

var (
	migrationPool   *pgxpool.Pool
	migrationPoolMu sync.Mutex
)

func resetMigrationTables(ctx context.Context, tb testing.TB) {
	tb.Helper()
	_, err := migrationPool.Exec(ctx, `TRUNCATE TABLE participant_reservations, tournaments CASCADE`)
	require.NoError(tb, err)
}
