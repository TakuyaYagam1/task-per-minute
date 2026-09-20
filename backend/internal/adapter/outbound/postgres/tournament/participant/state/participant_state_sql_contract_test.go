package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParticipantStateSeriesSelectionTreatsSupersededSeriesAsHistorical(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "..", "db", "queries", "participant_read.sql"))
	require.NoError(t, err)
	query := participantStateQueryContract(t, string(payload), "-- name: GetParticipantStateSeries :one")

	// Superseded Series have no participant-facing Wave link after replacement.
	// They must not outrank a terminal completed result when no next-stage
	// Series or active draft exists.
	require.Equal(t, 2, strings.Count(query, "series.state NOT IN ('completed', 'cancelled', 'superseded')"))
	require.NotContains(t, query, "series.state NOT IN ('completed', 'cancelled')")
}

func participantStateQueryContract(t *testing.T, queries, marker string) string {
	t.Helper()
	start := strings.Index(queries, marker)
	require.NotEqual(t, -1, start)
	remainder := queries[start+len(marker):]
	end := strings.Index(remainder, "-- name:")
	if end == -1 {
		return remainder
	}
	return remainder[:end]
}
