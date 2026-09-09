package postgres

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFinalDraftSourcePinsNormalPoolAndRetainedStageMembership(t *testing.T) {
	terminal, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "queries", "playoff_terminal.sql"))
	require.NoError(t, err)
	for _, fragment := range []string{"-- name: LockPostseasonFinalNormalPool :one", "configuration.revision = sqlc.arg(content_revision)", "category_pool.id = sqlc.arg(category_pool_id)", "pool.id = configuration.normal_pool_revision_id", "pool.kind = 'normal'"} {
		require.Contains(t, string(terminal), fragment)
	}
	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "queries", "exact_draft_assignment.sql"))
	require.NoError(t, err)
	stage := strings.Split(string(query), "-- name: LockExactDraftPlanningParticipants")[0]
	require.NotContains(t, stage, "AND projection.state = 'published'")
	for _, fragment := range []string{"projection.id = evidence.published_projection_revision_id", "current_projection.state = 'published'", "current_membership.artifact_id = bracket.id", "source_membership.artifact_id = bracket.id"} {
		require.Contains(t, stage, fragment)
	}
}
