//go:build integration

package integration_test

import "testing"

func TestResultAuditFlow(t *testing.T) {
	t.Run("atomic server settlement", TestConcurrentResultSettlement)
	t.Run("append only retention and maintenance guards", TestResultAuditMigration)
	t.Run("pagination redaction and export filters", TestResultAuditRepository)
}
