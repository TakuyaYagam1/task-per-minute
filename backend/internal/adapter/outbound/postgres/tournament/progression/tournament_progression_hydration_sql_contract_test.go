package progression

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSwissProgressionHydrationLocksExactHistoricalSourcesAndLogicalNodes(t *testing.T) {
	t.Parallel()

	queries, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "..", "..", "..", "db", "queries", "tournament_progression.sql",
	))
	require.NoError(t, err)

	// Receipt rows retain the physical projection revision used while a result
	// was accepted. The reader must lock that exact revision and its persisted
	// artifacts rather than silently replacing it with a mutable current head.
	// Logical nodes identify the corresponding immutable result, score, and
	// game payloads for both ordinary and correction writers.
	for _, fragment := range []string{
		"-- name: LockTournamentProgressionFinalSwissReceiptSourceProjections :many",
		"final_swiss_projection_receipt_series AS receipt_series",
		"final_swiss_projection_receipt_games AS receipt_game",
		"source_projection_revision_id",
		"projection_revisions AS projection_revision",
		"projection_revision_artifacts AS membership",
		"projection_artifacts AS artifact",
		"FOR KEY SHARE OF projection_revision, membership, artifact",
		"-- name: LockTournamentProgressionFinalSwissReceiptLogicalResultNodes :many",
		"receipt_series.series_result_node_id",
		"receipt_series.score_node_id",
		"receipt_game.game_result_node_id",
		"result_projection_nodes AS node",
		"result_projection_node_authorities AS authority",
		"correction_projection_bindings AS binding",
		"node.id AS node_id",
		"binding.node_id = head.node_id",
	} {
		require.Contains(t, string(queries), fragment)
	}
	sourceBlock := progressionSQLQueryBlock(
		t, string(queries),
		"-- name: LockTournamentProgressionFinalSwissReceiptSourceProjections :many",
		"-- name: LockTournamentProgressionFinalSwissReceiptLogicalResultNodes :many",
	)
	logicalBlock := progressionSQLQueryBlock(
		t, string(queries),
		"-- name: LockTournamentProgressionFinalSwissReceiptLogicalResultNodes :many",
		"-- name: LockTournamentProgressionSwissProjectionDependencies :many",
	)
	require.NotContains(t, sourceBlock, "current_result_revision_id")
	require.NotContains(t, sourceBlock, "current_score_revision_id")
	// Reused membership is immutable publication authority as well. A source
	// revision need not have produced an artifact when its Wave was partial.
	require.NotContains(t, sourceBlock, "artifact.produced_by_revision_id = projection_revision.id")
	require.Contains(t, sourceBlock, "artifact.artifact_kind = membership.artifact_kind")
	require.NotContains(t, logicalBlock, "current_result_revision_id")
	require.NotContains(t, logicalBlock, "current_score_revision_id")
	require.NotContains(t, logicalBlock, "game_attempt.result_revision_id")
}

func progressionSQLQueryBlock(t *testing.T, source, start, end string) string {
	t.Helper()
	startIndex := strings.Index(source, start)
	require.GreaterOrEqual(t, startIndex, 0)
	endIndex := strings.Index(source[startIndex:], end)
	require.Positive(t, endIndex)
	return source[startIndex : startIndex+endIndex]
}
