//go:build integration

package result

import "testing"

func TestResultAuditRepositoryPinsSnapshotAcrossPages(t *testing.T) {
	RunResultAuditRepositoryPinsSnapshotAcrossPages(t, sharedPool)
}
