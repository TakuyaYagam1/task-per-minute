//go:build integration

package execution_test

import (
	"testing"

	executionintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/execution"
)

func TestExecutionMigration(t *testing.T) {
	executionintegration.RunExecutionMigration(t, sharedPool)
}
