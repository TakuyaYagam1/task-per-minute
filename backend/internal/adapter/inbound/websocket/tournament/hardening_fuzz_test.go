package tournament

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func FuzzParticipantRealtimeEnvelopeCopiesSource(f *testing.F) {
	f.Add("participant")
	f.Add("")
	f.Fuzz(func(t *testing.T, value string) {
		tournamentID := testUUID("95000000-0000-4000-8000-000000000101")
		playerID := testUUID("95000000-0000-4000-8000-000000000102")
		input := testParticipantSnapshotInput(tournamentID, playerID)
		snapshot, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, input)
		if err != nil {
			t.Fatal(err)
		}
		envelope := hardeningEnvelope(t, tournamentID, snapshot.Revision, snapshot.LastSequence, snapshot)
		copy, err := cloneRealtimeEnvelope(envelope)
		if err != nil {
			t.Fatal(err)
		}

		probe := hardeningProbe(value)
		input.Assignment.Task.Title = probe
		input.Opponent.DisplayName = probe
		requireFrameWithoutProbe(t, envelope, probe)

		snapshot.Assignment.Task.Title = probe
		snapshot.Opponent.DisplayName = probe
		requireFrameWithoutProbe(t, envelope, probe)

		envelope.Participant.Assignment.Task.Title = probe
		envelope.Participant.Opponent.DisplayName = probe
		requireFrameWithoutProbe(t, copy, probe)
	})
}

func FuzzPublicRealtimeEnvelopeCopiesSource(f *testing.F) {
	f.Add("public")
	f.Add("")
	f.Fuzz(func(t *testing.T, value string) {
		tournamentID := testUUID("95000000-0000-4000-8000-000000000111")
		input := testPublicSnapshotInput(tournamentID)
		snapshot, err := NewPublicSnapshot(tournamentID, input)
		if err != nil {
			t.Fatal(err)
		}
		envelope := hardeningEnvelope(t, tournamentID, snapshot.Revision, snapshot.LastSequence, snapshot)
		copy, err := cloneRealtimeEnvelope(envelope)
		if err != nil {
			t.Fatal(err)
		}

		probe := hardeningProbe(value)
		input.Scoreboard[0].DisplayName = probe
		input.Draft.Pool[0] = probe
		*input.Tournament.StartedAt = input.Tournament.StartedAt.Add(time.Minute)
		requireFrameWithoutProbe(t, envelope, probe)

		snapshot.Scoreboard[0].DisplayName = probe
		snapshot.Draft.Pool[0] = probe
		*snapshot.Tournament.StartedAt = snapshot.Tournament.StartedAt.Add(time.Minute)
		requireFrameWithoutProbe(t, envelope, probe)

		envelope.Public.Scoreboard[0].DisplayName = probe
		envelope.Public.Draft.Pool[0] = probe
		*envelope.Public.Tournament.StartedAt = envelope.Public.Tournament.StartedAt.Add(time.Minute)
		requireFrameWithoutProbe(t, copy, probe)
	})
}

func FuzzOperatorRealtimeEnvelopeCopiesSource(f *testing.F) {
	f.Add("operator")
	f.Add("")
	f.Fuzz(func(t *testing.T, value string) {
		tournamentID := testUUID("95000000-0000-4000-8000-000000000121")
		access := OperatorSnapshotAccess{Authenticated: true, TournamentID: tournamentID, OperatorID: testUUID("95000000-0000-4000-8000-000000000122")}
		input := testOperatorSnapshotInput(tournamentID)
		snapshot, err := NewOperatorSnapshot(access, input)
		if err != nil {
			t.Fatal(err)
		}
		envelope := hardeningEnvelope(t, tournamentID, snapshot.Revision, snapshot.LastSequence, snapshot)
		copy, err := cloneRealtimeEnvelope(envelope)
		if err != nil {
			t.Fatal(err)
		}

		probe := hardeningProbe(value)
		input.Waves[0].State = probe
		*input.Waves[0].Members[0].SeriesID = testUUID("95000000-0000-4000-8000-000000000123")
		*input.Waves[0].WindowDeadline = input.Waves[0].WindowDeadline.Add(time.Minute)
		requireFrameWithoutProbe(t, envelope, probe)

		snapshot.Waves[0].State = probe
		*snapshot.Waves[0].Members[0].SeriesID = testUUID("95000000-0000-4000-8000-000000000124")
		*snapshot.Waves[0].WindowDeadline = snapshot.Waves[0].WindowDeadline.Add(time.Minute)
		requireFrameWithoutProbe(t, envelope, probe)

		envelope.Operator.Waves[0].State = probe
		*envelope.Operator.Waves[0].Members[0].SeriesID = testUUID("95000000-0000-4000-8000-000000000125")
		*envelope.Operator.Waves[0].WindowDeadline = envelope.Operator.Waves[0].WindowDeadline.Add(time.Minute)
		requireFrameWithoutProbe(t, copy, probe)
	})
}

func FuzzRealtimeEnvelopeRejectsMalformedTimestamps(f *testing.F) {
	f.Add(uint8(0), "not-a-timestamp")
	f.Add(uint8(1), "2026-09-06T12:00:00+03:00")
	f.Add(uint8(2), "")
	f.Fuzz(func(t *testing.T, selector uint8, timestamp string) {
		envelope := hardeningEnvelopeForRole(t, selector%3)
		encoded, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		field, original := hardeningTimestampField(selector % 3)
		candidate := replaceJSONField(t, encoded, field, original, timestamp)

		var decoded RealtimeEnvelope
		err = json.Unmarshal(candidate, &decoded)
		if validWireTimestamp(timestamp) {
			if err == nil {
				if err := decoded.Validate(); err != nil {
					t.Fatalf("valid timestamp decoded into invalid envelope: %v", err)
				}
				if _, err := json.Marshal(decoded); err != nil {
					t.Fatalf("valid timestamp did not re-encode: %v", err)
				}
			}
			return
		}
		if err == nil {
			t.Fatalf("malformed timestamp %q was accepted", timestamp)
		}
	})
}

func FuzzRealtimeEnvelopeRejectsDuplicateNestedKeys(f *testing.F) {
	f.Add(uint8(0), "replacement")
	f.Add(uint8(1), "")
	f.Fuzz(func(t *testing.T, selector uint8, value string) {
		role := selector % 3
		envelope := hardeningEnvelopeForRole(t, role)
		encoded, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		field, original := hardeningDuplicateField(role)
		duplicate, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		needle := []byte(`"` + field + `":` + original)
		replacement := append(append([]byte{}, needle...), append([]byte(`,"`+field+`":`), duplicate...)...)
		candidate := bytes.Replace(encoded, needle, replacement, 1)
		if bytes.Equal(candidate, encoded) {
			t.Fatalf("fixture lacks nested field %q", field)
		}
		var decoded RealtimeEnvelope
		if err := json.Unmarshal(candidate, &decoded); err == nil {
			t.Fatalf("duplicate nested key %q was accepted", field)
		}
	})
}

func hardeningEnvelope(t testing.TB, tournamentID uuid.UUID, revision, sequence int64, payload any) RealtimeEnvelope {
	t.Helper()
	envelope, err := NewRealtimeEnvelope(RealtimeEnvelopeMetadata{
		SchemaVersion:      TournamentRealtimeSchemaVersion,
		TournamentID:       tournamentID,
		Sequence:           sequence,
		EventID:            testUUID("95000000-0000-4000-8000-000000000199"),
		OccurredAt:         time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC),
		ProjectionRevision: revision,
	}, payload)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func hardeningEnvelopeForRole(t *testing.T, role uint8) RealtimeEnvelope {
	t.Helper()
	tournamentID := testUUID("95000000-0000-4000-8000-000000000131")
	switch role {
	case 0:
		return hardeningEnvelope(t, tournamentID, 9, 12, testParticipantSnapshot(t, tournamentID))
	case 1:
		return hardeningEnvelope(t, tournamentID, 9, 12, testPublicSnapshot(t, tournamentID))
	default:
		access := OperatorSnapshotAccess{Authenticated: true, TournamentID: tournamentID, OperatorID: testUUID("95000000-0000-4000-8000-000000000132")}
		snapshot, err := NewOperatorSnapshot(access, testOperatorSnapshotInput(tournamentID))
		if err != nil {
			t.Fatal(err)
		}
		return hardeningEnvelope(t, tournamentID, snapshot.Revision, snapshot.LastSequence, snapshot)
	}
}

func hardeningTimestampField(role uint8) (string, string) {
	switch role {
	case 0:
		return "occurred_at", "2026-09-06T09:00:00Z"
	case 1:
		return "started_at", "2026-09-02T08:00:00Z"
	default:
		return "window_deadline", "2026-09-02T09:00:00Z"
	}
}

func hardeningDuplicateField(role uint8) (string, string) {
	switch role {
	case 0:
		return "title", `"Packet relay"`
	case 1:
		return "display_name", `"red"`
	default:
		return "state", `"ready_window_open"`
	}
}

func replaceJSONField(t testing.TB, encoded []byte, field, original, value string) []byte {
	t.Helper()
	replacement, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	needle := []byte(`"` + field + `":"` + original + `"`)
	candidate := bytes.Replace(encoded, needle, append([]byte(`"`+field+`":`), replacement...), 1)
	if bytes.Equal(candidate, encoded) {
		t.Fatalf("fixture lacks timestamp field %q", field)
	}
	return candidate
}

func validWireTimestamp(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && !parsed.IsZero() && parsed.Location() == time.UTC
}

func hardeningProbe(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "probe-" + hex.EncodeToString(digest[:])
}

func requireFrameWithoutProbe(t testing.TB, envelope RealtimeEnvelope, probe string) {
	t.Helper()
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), probe) {
		t.Fatalf("realtime frame retained mutated source value %q", probe)
	}
}
