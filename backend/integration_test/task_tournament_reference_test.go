//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestTaskRepo_TournamentReferenceProtectsTask(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createResultAuditMigrationFixture(ctx, t)
	var taskID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT task_id
		FROM assignments
		WHERE id = $1`, fixture.assignmentID).Scan(&taskID))

	repo := newTaskRepo()
	require.ErrorIs(t, repo.Delete(ctx, taskID), domain.ErrTaskInUse)
}
