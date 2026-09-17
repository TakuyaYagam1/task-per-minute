//go:build integration

package golden_test

import (
	"testing"

	goldenintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/golden"
)

func TestGoldenMigration(t *testing.T) {
	goldenintegration.RunGoldenMigration(t, sharedPool)
}
