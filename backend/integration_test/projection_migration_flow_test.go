//go:build integration

package integration_test

import (
	"testing"

	projectiontest "github.com/TakuyaYagam1/task-per-minute/integration_test/projection"
)

// TestProjectionMigration remains a root compatibility entry point for the
// aggregate playoff flow while the implementation lives in projection.
func TestProjectionMigration(t *testing.T) {
	projectiontest.RunProjectionMigration(t, sharedPool)
}
