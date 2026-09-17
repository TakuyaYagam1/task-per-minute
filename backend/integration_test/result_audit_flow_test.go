//go:build integration

package integration_test

import (
	"testing"

	resultintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/result"
)

func TestResultAuditFlow(t *testing.T) {
	t.Run("atomic server settlement", TestConcurrentResultSettlement)
	t.Run("append only retention and maintenance guards", TestResultAuditMigration)
	t.Run("pagination redaction and export filters", func(t *testing.T) {
		resultintegration.RunResultAuditRepository(t, sharedPool)
	})
}
