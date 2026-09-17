//go:build integration

package result

import "testing"

func TestResultAuditMigration(t *testing.T) {
	RunResultAuditMigration(t, sharedPool)
}

func TestResultAuditMigrationCommitLocks(t *testing.T) {
	RunResultAuditMigrationCommitLocks(t, sharedPool)
}
