package postgres

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParticipantArchiveQueryAuthorizesStartedImmutableSnapshots(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "queries", "participant_read.sql"))
	require.NoError(t, err)
	query := archiveQueryContract(t, string(payload))

	for _, fragment := range []string{
		"task_delivery_receipts AS receipt",
		"assignment.id = sqlc.arg(assignment_id)",
		"receipt.participant_id = participant.id",
		"golden_runtime_assignments AS runtime",
		"golden_memberships AS membership",
		"runtime.started_at IS NOT NULL",
		"membership.participation_established_at IS NOT NULL",
		"runtime.assignment_id = sqlc.arg(assignment_id)",
		"participant.player_id = sqlc.arg(player_id)",
		"task_snapshots AS snapshot",
		"snapshot.id = runtime.snapshot_id",
		"snapshot.source_file_url",
		"UNION ALL",
	} {
		require.Contains(t, query, fragment)
	}
	require.NotContains(t, query, "UPDATE ")
	require.NotContains(t, query, "INSERT ")
	require.NotContains(t, query, "DELETE ")
	require.NotContains(t, query, "FROM tasks ")
}

func archiveQueryContract(t *testing.T, queries string) string {
	t.Helper()
	const marker = "-- name: GetParticipantArchiveSource :one"
	start := strings.Index(queries, marker)
	require.NotEqual(t, -1, start)
	remainder := queries[start+len(marker):]
	end := strings.Index(remainder, "-- name:")
	require.NotEqual(t, -1, end)
	return remainder[:end]
}
