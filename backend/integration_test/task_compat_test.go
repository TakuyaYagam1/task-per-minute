//go:build integration

package integration_test

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	taskrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/task"
)

// newTaskRepo keeps the root integration helpers source-compatible while the
// task repository tests run in their own package.
func newTaskRepo(pools ...*pgxpool.Pool) *taskrepo.TaskPostgres {
	pool := sharedPool
	if len(pools) > 0 && pools[0] != nil {
		pool = pools[0]
	}
	return taskrepo.NewTaskPostgres(postgres.NewTxManager(pool))
}
