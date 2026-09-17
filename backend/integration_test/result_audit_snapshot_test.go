//go:build integration

package integration_test

import (
	"testing"

	resultintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/result"
)

func TestResultAuditRepositoryPinsSnapshotAcrossPages(t *testing.T) {
	resultintegration.RunResultAuditRepositoryPinsSnapshotAcrossPages(t, sharedPool)
}
