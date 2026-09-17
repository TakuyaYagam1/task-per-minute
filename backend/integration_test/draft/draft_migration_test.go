//go:build integration

package draft_test

import (
	"testing"

	draftintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/draft"
)

func TestDraftMigration(t *testing.T) {
	draftintegration.RunDraftMigration(t, sharedPool)
}
