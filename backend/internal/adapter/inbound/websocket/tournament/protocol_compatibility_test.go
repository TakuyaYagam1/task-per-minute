package tournament

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRealtimeEnvelopeV1CompatibilityFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/realtime_envelope_v1_participant.json")
	require.NoError(t, err)

	var envelope RealtimeEnvelope
	require.NoError(t, json.Unmarshal(fixture, &envelope))
	require.Equal(t, TournamentRealtimeSchemaVersion, envelope.SchemaVersion)
	require.NoError(t, envelope.Validate())

	encoded, err := json.Marshal(envelope)
	require.NoError(t, err)
	var canonical bytes.Buffer
	require.NoError(t, json.Compact(&canonical, fixture))
	require.Equal(t, canonical.Bytes(), encoded)
	require.NotContains(t, string(encoded), `"resume_id"`)
}
