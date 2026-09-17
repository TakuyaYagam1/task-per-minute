//go:build integration

package assignment_test

import (
	"testing"

	assignmentintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/assignment"
)

func TestAssignmentMigration(t *testing.T) {
	assignmentintegration.RunAssignmentMigration(t, sharedPool)
}
