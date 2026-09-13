package tournament

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTournamentRealtimeEnvelope(t *testing.T) {
	t.Parallel()

	tournamentID := testUUID("00000000-0000-4000-8000-000000000001")
	snapshot := testParticipantSnapshot(t, tournamentID)
	metadata := RealtimeEnvelopeMetadata{
		SchemaVersion:      TournamentRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           12,
		EventID:            testUUID("00000000-0000-4000-8000-000000000002"),
		OccurredAt:         time.Date(2026, 9, 2, 8, 30, 0, 123, time.UTC),
		ProjectionRevision: 9,
	}

	envelope, err := NewRealtimeEnvelope(metadata, snapshot)
	if err != nil {
		t.Fatalf("NewRealtimeEnvelope() error = %v", err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !bytes.HasPrefix(encoded, []byte(`{"schema_version":1,"tournament_id":`)) {
		t.Fatalf("envelope field order is not stable: %s", encoded)
	}
	requireJSONKeys(t, encoded, "schema_version", "tournament_id", "sequence", "event_id", "occurred_at", "projection_revision", "participant")

	var decoded RealtimeEnvelope
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	roundTrip, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("round-trip json.Marshal() error = %v", err)
	}
	if !bytes.Equal(encoded, roundTrip) {
		t.Fatalf("JSON round trip changed bytes:\n got %s\nwant %s", roundTrip, encoded)
	}

	participantJSON, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	publicSnapshot := testPublicSnapshot(t, tournamentID)
	publicJSON, err := json.Marshal(publicSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	validPrefix := `{"schema_version":1,"tournament_id":"` + tournamentID.String() + `","sequence":12,"event_id":"00000000-0000-4000-8000-000000000002","occurred_at":"2026-09-02T08:30:00.000000123Z","projection_revision":9,`

	tests := []struct {
		name string
		body string
	}{
		{name: "malformed tournament ID", body: strings.Replace(validPrefix+`"participant":`+string(participantJSON)+`}`, tournamentID.String(), "not-a-uuid", 1)},
		{name: "snapshot cursor mismatch", body: strings.Replace(validPrefix+`"participant":`+string(participantJSON)+`}`, `"sequence":12`, `"sequence":0`, 1)},
		{name: "zero projection revision", body: strings.Replace(validPrefix+`"participant":`+string(participantJSON)+`}`, `"projection_revision":9`, `"projection_revision":0`, 1)},
		{name: "non UTC timestamp", body: strings.Replace(validPrefix+`"participant":`+string(participantJSON)+`}`, `2026-09-02T08:30:00.000000123Z`, `2026-09-02T11:30:00.000000123+03:00`, 1)},
		{name: "missing role payload", body: strings.TrimSuffix(validPrefix, ",") + `}`},
		{name: "multiple role payloads", body: validPrefix + `"participant":` + string(participantJSON) + `,"public":` + string(publicJSON) + `}`},
		{name: "mutation command", body: validPrefix + `"participant":` + string(participantJSON) + `,"command":{"pause":true}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got RealtimeEnvelope
			if err := json.Unmarshal([]byte(tt.body), &got); err == nil {
				t.Fatalf("json.Unmarshal() accepted %s", tt.name)
			}
		})
	}

	duplicateNested := strings.Replace(
		validPrefix+`"participant":`+string(participantJSON)+`}`,
		`"player_id":"00000000-0000-4000-8000-000000000010"`,
		`"player_id":"00000000-0000-4000-8000-000000000010","player_id":"00000000-0000-4000-8000-000000000099"`,
		1,
	)
	var duplicate RealtimeEnvelope
	if err := json.Unmarshal([]byte(duplicateNested), &duplicate); err == nil {
		t.Fatal("json.Unmarshal() accepted a nested duplicate key")
	}
}

func TestTournamentRealtimeEnvelopeV1InitialWatermarkZero(t *testing.T) {
	t.Parallel()

	tournamentID := testUUID("00000000-0000-4000-8000-000000000020")
	snapshot, err := NewParticipantSnapshot(
		ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: testUUID("00000000-0000-4000-8000-000000000021")},
		ParticipantSnapshotInput{
			TournamentID: tournamentID, PlayerID: testUUID("00000000-0000-4000-8000-000000000021"),
			Revision: 1, LastSequence: 0,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := NewRealtimeEnvelope(RealtimeEnvelopeMetadata{
		SchemaVersion:      TournamentRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           0,
		EventID:            testUUID("00000000-0000-4000-8000-000000000022"),
		OccurredAt:         time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC),
		ProjectionRevision: 1,
	}, snapshot)
	if err != nil {
		t.Fatalf("NewRealtimeEnvelope() error = %v", err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	requireJSONKeys(t, encoded, "schema_version", "tournament_id", "sequence", "event_id", "occurred_at", "projection_revision", "participant")
	if bytes.Contains(encoded, []byte(`"resume_id"`)) {
		t.Fatalf("v1 initial fixture unexpectedly emitted resume_id: %s", encoded)
	}
	var decoded RealtimeEnvelope
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if decoded.Sequence != 0 || decoded.Participant == nil || decoded.Participant.LastSequence != 0 {
		t.Fatalf("initial cursor was not preserved: %+v", decoded)
	}
}

func testUUID(value string) uuid.UUID {
	return uuid.MustParse(value)
}

func requireJSONKeys(t *testing.T, encoded []byte, want ...string) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatalf("decode JSON object: %v", err)
	}
	if len(object) != len(want) {
		t.Fatalf("JSON keys = %v, want %v", sortedKeys(object), want)
	}
	for _, key := range want {
		if _, ok := object[key]; !ok {
			t.Fatalf("JSON is missing key %q: %s", key, encoded)
		}
	}
}

func sortedKeys(object map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func requireNoSecretNames(t *testing.T, encoded []byte) {
	t.Helper()
	for _, forbidden := range []string{"flag", "credential", "password", "secret", "hidden_hint", "hints", "source_file_url", "content_digest", "submission", "raw_connection", "audit_actor", "command"} {
		if bytes.Contains(bytes.ToLower(encoded), []byte(forbidden)) {
			t.Fatalf("JSON contains forbidden field name %q: %s", forbidden, encoded)
		}
	}
}
