//go:build integration

package integration_test

import "testing"

func TestArenaAuditCompleteness(t *testing.T) {
	t.Run("atomic server settlement", TestArenaConcurrentSettlement)
	t.Run("append only retention and maintenance guards", TestArenaResultAuditMigration)
	t.Run("pagination redaction and export filters", TestArenaAuditRepository)
}
