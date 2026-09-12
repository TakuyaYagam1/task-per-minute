package postgres

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestProjectionPayloadShapeUsesCanonicalCollectionKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		kind    domain.ArtifactKind
		payload string
		valid   bool
	}{
		{name: "standings entries", kind: domain.ArtifactKindStandings, payload: `{"entries":[{"position":1}]}`, valid: true},
		{name: "standings legacy alias", kind: domain.ArtifactKindStandings, payload: `{"entries":[{"position":1}],"standings":[{"position":1}]}`, valid: false},
		{name: "standings empty entries", kind: domain.ArtifactKindStandings, payload: `{"entries":[]}`, valid: false},
		{name: "bracket rounds", kind: domain.ArtifactKindBracket, payload: `{"rounds":[{"position":1}]}`, valid: true},
		{name: "bracket legacy alias", kind: domain.ArtifactKindBracket, payload: `{"rounds":[{"position":1}],"semifinals":[{"position":1}]}`, valid: false},
		{name: "bracket empty rounds", kind: domain.ArtifactKindBracket, payload: `{"rounds":[]}`, valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var payload map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(test.payload), &payload))
			require.Equal(t, test.valid, validProjectionPayloadShape(
				ProjectionArtifactInput{Kind: test.kind},
				payload,
			))
		})
	}
}

func TestInitialProjectionInputAcceptsCanonicalEmptyArtifacts(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	rosterID := uuid.New()
	createdAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	input, err := initialTournamentProjectionInput(tournamentID, rosterID, createdAt)
	require.NoError(t, err)
	require.True(t, validProjectionPublishInput(input))
	require.Equal(t, projectionSourceInitial, input.Source.Kind)
	require.Equal(t, tournamentID, input.Scope.TournamentID)
	require.Equal(t, rosterID, input.Scope.RosterID)
	require.Len(t, input.Artifacts, 3)
	for _, artifact := range input.Artifacts {
		require.Empty(t, artifact.Members)
		require.Empty(t, artifact.Dependencies)
	}

	replayed, err := initialTournamentProjectionInput(tournamentID, rosterID, createdAt)
	require.NoError(t, err)
	require.Equal(t, input.IDs, replayed.IDs)
	for index := range input.Artifacts {
		require.Equal(t, input.Artifacts[index].ID, replayed.Artifacts[index].ID)
		require.Equal(t, input.Artifacts[index].Payload, replayed.Artifacts[index].Payload)
	}
}

func TestNonInitialProjectionRejectsCanonicalEmptyArtifacts(t *testing.T) {
	t.Parallel()

	input, err := initialTournamentProjectionInput(
		uuid.New(), uuid.New(), time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	)
	require.NoError(t, err)
	input.Source = ProjectionSource{Kind: projectionSourceOperatorRebuild, Reason: "operator_rebuild"}
	input.SupersessionReason = "operator_rebuild"

	require.False(t, validProjectionPublishInput(input))
}
