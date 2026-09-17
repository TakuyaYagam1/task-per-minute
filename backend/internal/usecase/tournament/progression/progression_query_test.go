package progression

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/canonical"
)

func TestProgressionQueryUsesCanonicalSeriesTerminalTimestamp(t *testing.T) {
	t.Parallel()

	queryPath := filepath.Join("..", "..", "..", "..", "db", "queries", "tournament_progression.sql")
	query, err := os.ReadFile(queryPath)
	require.NoError(t, err)

	source := string(query)
	require.Contains(t, source, "series.finished_at")
	require.NotContains(t, source, "series.completed_at")
	require.Contains(t, source, "FOR UPDATE OF series;")
	require.Contains(t, source, "FOR UPDATE OF result_head, result_revision;")
	require.Contains(t, source, "FOR UPDATE OF score_head, score_revision;")
	require.Contains(t, source, "FOR UPDATE OF game_slot, game_attempt, game_result_head, game_result_revision;")
	require.Contains(t, source, "normal_no_show_commits")
	require.Contains(t, source, "operator_forfeit_commits")
	require.Contains(t, source, "result_projection_nodes")
	require.Contains(t, source, "node.payload_digest")
	require.Contains(t, source, "FOR UPDATE OF tournament, roster, source_revision, resulting_revision")
}

func TestProgressionRejectsStaleStandingsAfterLastTerminalSeries(t *testing.T) {
	t.Parallel()

	participantID := uuid.New()
	materialization := resultprojection.CanonicalMaterialization{Artifacts: []resultprojection.CanonicalMaterializedArtifact{{
		Kind:          domain.ArtifactKindStandings,
		PayloadDigest: [32]byte{1},
		Members:       []resultprojection.CanonicalMaterializedMember{{ParticipantID: participantID, Position: 1}},
	}}}
	current := ProjectionReference{
		ArtifactID: uuid.New(), RevisionID: uuid.New(), Revision: 4, Kind: domain.ArtifactKindStandings,
		Digest: [32]byte{2}, Members: []ProjectionMember{{ParticipantID: participantID, Position: 1}},
	}

	require.False(t, currentMatchesCanonicalSwiss(current, materialization))
	current.Digest = materialization.Artifacts[0].PayloadDigest
	require.True(t, currentMatchesCanonicalSwiss(current, materialization))
}

func TestProgressionRejectsPartialWaveMaterialization(t *testing.T) {
	t.Parallel()

	err := validateSwissEvidence(Authority{}, SwissEvidence{
		ExpectedRounds: 3, TerminalRounds: 3,
		ExpectedWaves: 3, TerminalWaves: 2,
		ExpectedSeries: 6, TerminalSeries: 6,
	})

	require.ErrorIs(t, err, domain.ErrConflict)
}
