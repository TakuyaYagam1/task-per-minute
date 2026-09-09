package postgres

import (
	"encoding/json"
	"testing"

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
