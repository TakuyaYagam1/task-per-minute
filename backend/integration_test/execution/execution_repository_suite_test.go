//go:build integration

package execution_test

import (
	"testing"

	executionintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/execution"
)

func TestExecutionRepositorySerializesReadinessAndStart(t *testing.T) {
	executionintegration.RunExecutionRepositorySerializesReadinessAndStart(t, sharedPool)
}
