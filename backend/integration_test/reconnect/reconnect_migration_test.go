//go:build integration

package reconnect

import "testing"

func TestReconnectMigration(t *testing.T) {
	RunReconnectMigration(t, sharedPool)
}

func TestReconnectMigrationResumeCAS(t *testing.T) {
	RunReconnectMigrationResumeCAS(t, sharedPool)
}
