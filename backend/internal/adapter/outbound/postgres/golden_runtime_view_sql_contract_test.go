package postgres

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGoldenRuntimeViewUsesExactImmutableSnapshotAndParticipantGate(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "queries", "golden.sql"))
	require.NoError(t, err)
	query := goldenRuntimeViewQueryContract(t, string(payload))

	for _, fragment := range []string{
		"snapshot.id = runtime.snapshot_id",
		"snapshot.reservation_id = runtime.assignment_id",
		"snapshot.task_id = runtime.task_id",
		"snapshot.task_version = runtime.task_version",
		"snapshot.kind = 'golden'",
		"snapshot.content_digest = runtime.source_digest",
		"participant_eligible",
		"snapshot.task_version",
		"snapshot.description",
		"snapshot.task_url",
		"source_file_available",
	} {
		require.Contains(t, query, fragment)
	}
	require.NotContains(t, query, "snapshot.flag")
	require.NotContains(t, query, "snapshot.hints")
}

func goldenRuntimeViewQueryContract(t *testing.T, queries string) string {
	t.Helper()
	const marker = "-- name: ListGoldenRuntimeView :many"
	start := strings.Index(queries, marker)
	require.NotEqual(t, -1, start)
	remainder := queries[start+len(marker):]
	end := strings.Index(remainder, "-- name:")
	require.NotEqual(t, -1, end)
	return remainder[:end]
}
