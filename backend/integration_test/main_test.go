//go:build integration

package integration_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var sharedPool *pgxpool.Pool

func TestMain(m *testing.M) {
	pool, teardown, err := startPostgres()
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration_test: failed to start postgres: %v\n", err)
		os.Exit(1)
	}
	sharedPool = pool

	code := m.Run()

	teardown()
	if redisTeardown != nil {
		redisTeardown()
	}
	if seaweedTeardown != nil {
		seaweedTeardown()
	}
	os.Exit(code)
}

// uniq builds a unique-per-call identifier suffixed with 16 hex chars.
// Tests use it to scope their entities (usernames, task titles, file keys) so
// parallel tests do not collide on UNIQUE constraints or shared bucket counts.
func uniq(prefix string) string {
	return prefix + "_" + uuid.NewString()[:16]
}
