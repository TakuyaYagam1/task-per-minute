//go:build integration

package integration_test

import (
	"testing"

	reconnectsuite "github.com/TakuyaYagam1/task-per-minute/integration_test/reconnect"
)

// Keep the recovery restart aggregate source-compatible while the migration
// scenarios live in the reconnect capability package.
func TestReconnectMigration(t *testing.T) {
	reconnectsuite.RunReconnectMigration(t, sharedPool)
}

func TestReconnectMigrationResumeCAS(t *testing.T) {
	reconnectsuite.RunReconnectMigrationResumeCAS(t, sharedPool)
}
