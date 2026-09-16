//go:build integration

package db_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
)

var postgresDSN string

func TestMain(m *testing.M) {
	pool, teardown, err := testkit.StartPostgres(testkit.PostgresConfig{
		MigrationsDir:  filepath.Join("..", "..", "db", "migrations"),
		StartupTimeout: 2 * time.Minute,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration_test/db: failed to start postgres: %v\n", err)
		os.Exit(1)
	}
	postgresDSN = pool.Config().ConnString()

	code := m.Run()
	teardown()
	os.Exit(code)
}
