//go:build integration

package result

import "testing"

func TestResultAuditRepository(t *testing.T) {
	RunResultAuditRepository(t, sharedPool)
}
