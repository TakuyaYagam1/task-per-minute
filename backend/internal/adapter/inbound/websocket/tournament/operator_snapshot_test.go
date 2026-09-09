package tournament

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/wirelimits"
)

func TestTournamentOperatorSnapshot(t *testing.T) {
	t.Parallel()

	tournamentID := testUUID("00000000-0000-4000-8000-000000000001")
	access := OperatorSnapshotAccess{Authenticated: true, TournamentID: tournamentID, OperatorID: testUUID("00000000-0000-4000-8000-000000000050")}
	input := testOperatorSnapshotInput(tournamentID)
	snapshot, err := NewOperatorSnapshot(access, input)
	if err != nil {
		t.Fatalf("NewOperatorSnapshot() error = %v", err)
	}
	input.Waves[0].Members[0].Ready = false
	input.AuditLinks[0].EntityKind = "mutated"

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	requireJSONKeys(t, encoded, "tournament_id", "revision", "last_sequence", "waves", "presence", "replays", "pause", "audit_links")
	requireNoSecretNames(t, encoded)
	if containsJSONText(encoded, "mutated") {
		t.Fatalf("snapshot retained mutable source slices: %s", encoded)
	}

	unauthenticated := access
	unauthenticated.Authenticated = false
	if _, err := NewOperatorSnapshot(unauthenticated, testOperatorSnapshotInput(tournamentID)); err == nil {
		t.Fatal("NewOperatorSnapshot() accepted unauthenticated access")
	}
	wrongTournament := testOperatorSnapshotInput(tournamentID)
	wrongTournament.Presence[0].TournamentID = testUUID("00000000-0000-4000-8000-000000000099")
	if _, err := NewOperatorSnapshot(access, wrongTournament); err == nil {
		t.Fatal("NewOperatorSnapshot() accepted cross-tournament presence")
	}
	oversizedCollection := testOperatorSnapshotInput(tournamentID)
	oversizedCollection.Waves = make([]OperatorWaveInput, wirelimits.MaxCollectionItems+1)
	if _, err := NewOperatorSnapshot(access, oversizedCollection); err == nil {
		t.Fatal("NewOperatorSnapshot() accepted an oversized collection")
	}
	t.Run("optional pause is omitted", func(t *testing.T) {
		minimal := testOperatorSnapshotInput(tournamentID)
		minimal.Pause = nil
		got, err := NewOperatorSnapshot(access, minimal)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		requireJSONKeys(t, body, "tournament_id", "revision", "last_sequence", "waves", "presence", "replays", "audit_links")
	})
}

func testOperatorSnapshotInput(tournamentID uuid.UUID) OperatorSnapshotInput {
	deadline := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	seriesID := testUUID("00000000-0000-4000-8000-000000000022")
	return OperatorSnapshotInput{
		TournamentID: tournamentID,
		Revision:     9,
		LastSequence: 12,
		Waves: []OperatorWaveInput{{
			TournamentID:   tournamentID,
			WaveID:         testUUID("00000000-0000-4000-8000-000000000060"),
			State:          "ready_window_open",
			WindowDeadline: &deadline,
			Members: []OperatorWaveMemberInput{
				{ParticipantID: testUUID("00000000-0000-4000-8000-000000000061"), SeriesID: &seriesID, Ready: true, ReadinessRevision: 3},
				{ParticipantID: testUUID("00000000-0000-4000-8000-000000000062"), SeriesID: &seriesID, Ready: false, ReadinessRevision: 2},
				{ParticipantID: testUUID("00000000-0000-4000-8000-000000000063"), Ready: true, ReadinessRevision: 1},
			},
		}},
		Presence:   []OperatorPresenceInput{{TournamentID: tournamentID, ParticipantID: testUUID("00000000-0000-4000-8000-000000000061"), SeriesID: testUUID("00000000-0000-4000-8000-000000000022"), State: "connected", PresenceEpoch: 2, UpdatedAt: time.Date(2026, 9, 2, 8, 30, 0, 0, time.UTC)}},
		Replays:    []OperatorReplayInput{{TournamentID: tournamentID, SeriesID: testUUID("00000000-0000-4000-8000-000000000022"), SlotID: testUUID("00000000-0000-4000-8000-000000000063"), FailedGameID: testUUID("00000000-0000-4000-8000-000000000064"), ReplacementGameID: testUUID("00000000-0000-4000-8000-000000000065"), ReplacementWaveID: testUUID("00000000-0000-4000-8000-000000000066"), State: "planned", Revision: 2}},
		Pause:      &OperatorPauseInput{TournamentID: tournamentID, PauseID: testUUID("00000000-0000-4000-8000-000000000067"), State: "active", Reason: "network maintenance", PausedAt: time.Date(2026, 9, 2, 8, 25, 0, 0, time.UTC), GraphRevision: 5},
		AuditLinks: []OperatorAuditLinkInput{{TournamentID: tournamentID, AuditEventID: testUUID("00000000-0000-4000-8000-000000000068"), EntityKind: "series", EntityID: testUUID("00000000-0000-4000-8000-000000000022"), OfficialResultRevisionID: testUUID("00000000-0000-4000-8000-000000000069")}},
	}
}
