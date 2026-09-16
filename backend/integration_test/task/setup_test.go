//go:build integration

package task_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	testkit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
)

func newParallelTestDB(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	return testkit.NewParallelDatabase(tb, sharedPool, postgresConfig(""))
}
