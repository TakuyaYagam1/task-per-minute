//go:build integration

package swiss_test

import (
	"testing"

	swissintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/swiss"
)

func TestSwissMigration(t *testing.T) {
	swissintegration.RunSwissMigration(t, sharedPool)
}
