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
	requireJSONKeys(t, encoded, "tournament_id", "revision", "last_sequence", "waves", "presence", "replays", "pause", "audit_links", "golden")
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	requireJSONKeys(t, object["pause"], "pause_id", "state", "reason", "paused_at", "graph_revision", "game_id", "frozen_remaining_ms", "reconnect_deadline")
	var golden []json.RawMessage
	if err := json.Unmarshal(object["golden"], &golden); err != nil {
		t.Fatal(err)
	}
	if len(golden) != 1 {
		t.Fatalf("Golden groups = %d, want 1", len(golden))
	}
	requireJSONKeys(t, golden[0], "group_id", "group_revision_id", "attempt_id", "runtime_revision", "ready_window_id", "state", "position_from", "position_to", "members")
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
		requireJSONKeys(t, body, "tournament_id", "revision", "last_sequence", "waves", "presence", "replays", "audit_links", "golden")
	})
	t.Run("game pause clock fields stay consistent", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mutate func(*OperatorPauseInput)
		}{
			{name: "missing game", mutate: func(pause *OperatorPauseInput) { pause.GameID = nil }},
			{name: "missing frozen duration", mutate: func(pause *OperatorPauseInput) { pause.FrozenRemainingMS = nil }},
			{name: "zero frozen duration", mutate: func(pause *OperatorPauseInput) { *pause.FrozenRemainingMS = 0 }},
		} {
			t.Run(test.name, func(t *testing.T) {
				candidate := testOperatorSnapshotInput(tournamentID)
				test.mutate(candidate.Pause)
				if _, err := NewOperatorSnapshot(access, candidate); err == nil {
					t.Fatalf("NewOperatorSnapshot() accepted %s", test.name)
				}
			})
		}
	})
	t.Run("Golden runtime fence is consecutive on the wire", func(t *testing.T) {
		firstInput := testOperatorSnapshotInput(tournamentID)
		first, err := NewOperatorSnapshot(access, firstInput)
		if err != nil {
			t.Fatal(err)
		}
		secondInput := testOperatorSnapshotInput(tournamentID)
		secondInput.Golden[0].RuntimeRevision = first.Golden[0].RuntimeRevision + 1
		second, err := NewOperatorSnapshot(access, secondInput)
		if err != nil {
			t.Fatal(err)
		}
		if second.Revision != first.Revision {
			t.Fatalf("generic projection revision changed with Golden runtime fence: first=%d second=%d", first.Revision, second.Revision)
		}
		if second.Golden[0].RuntimeRevision != first.Golden[0].RuntimeRevision+1 {
			t.Fatalf("Golden runtime revisions = %d then %d, want consecutive revisions", first.Golden[0].RuntimeRevision, second.Golden[0].RuntimeRevision)
		}
		firstBody, err := json.Marshal(first)
		if err != nil {
			t.Fatal(err)
		}
		secondBody, err := json.Marshal(second)
		if err != nil {
			t.Fatal(err)
		}
		for index, body := range [][]byte{firstBody, secondBody} {
			var envelope struct {
				Golden []struct {
					RuntimeRevision int64 `json:"runtime_revision"`
				} `json:"golden"`
			}
			if err := json.Unmarshal(body, &envelope); err != nil {
				t.Fatal(err)
			}
			want := first.Golden[0].RuntimeRevision + int64(index)
			if len(envelope.Golden) != 1 || envelope.Golden[0].RuntimeRevision != want {
				t.Fatalf("wire Golden runtime revision at index %d = %+v, want %d", index, envelope.Golden, want)
			}
		}
	})

	t.Run("Golden runtime identity is required", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mutate func(*OperatorSnapshotInput)
		}{
			{name: "zero revision", mutate: func(in *OperatorSnapshotInput) { in.Golden[0].RuntimeRevision = 0 }},
			{name: "nil ready window", mutate: func(in *OperatorSnapshotInput) { in.Golden[0].ReadyWindowID = uuid.Nil }},
		} {
			t.Run(test.name, func(t *testing.T) {
				candidate := testOperatorSnapshotInput(tournamentID)
				test.mutate(&candidate)
				if _, err := NewOperatorSnapshot(access, candidate); err == nil {
					t.Fatalf("NewOperatorSnapshot() accepted %s", test.name)
				}
			})
		}
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
		Presence: []OperatorPresenceInput{{TournamentID: tournamentID, ParticipantID: testUUID("00000000-0000-4000-8000-000000000061"), SeriesID: testUUID("00000000-0000-4000-8000-000000000022"), State: "connected", PresenceEpoch: 2, UpdatedAt: time.Date(2026, 9, 2, 8, 30, 0, 0, time.UTC)}},
		Replays:  []OperatorReplayInput{{TournamentID: tournamentID, SeriesID: testUUID("00000000-0000-4000-8000-000000000022"), SlotID: testUUID("00000000-0000-4000-8000-000000000063"), FailedGameID: testUUID("00000000-0000-4000-8000-000000000064"), ReplacementGameID: testUUID("00000000-0000-4000-8000-000000000065"), ReplacementWaveID: testUUID("00000000-0000-4000-8000-000000000066"), State: "planned", Revision: 2}},
		Pause: &OperatorPauseInput{
			TournamentID: tournamentID, PauseID: testUUID("00000000-0000-4000-8000-000000000067"), State: "active",
			Reason: "network maintenance", PausedAt: time.Date(2026, 9, 2, 8, 25, 0, 0, time.UTC), GraphRevision: 5,
			GameID:            func() *uuid.UUID { value := testUUID("00000000-0000-4000-8000-000000000076"); return &value }(),
			FrozenRemainingMS: func() *int64 { value := int64(120000); return &value }(),
			ReconnectDeadline: func() *time.Time { value := time.Date(2026, 9, 2, 8, 27, 0, 0, time.UTC); return &value }(),
		},
		AuditLinks: []OperatorAuditLinkInput{{TournamentID: tournamentID, AuditEventID: testUUID("00000000-0000-4000-8000-000000000068"), EntityKind: "series", EntityID: testUUID("00000000-0000-4000-8000-000000000022"), OfficialResultRevisionID: testUUID("00000000-0000-4000-8000-000000000069")}},
		Golden: []OperatorGoldenGroupInput{{
			GroupID:         testUUID("00000000-0000-4000-8000-000000000070"),
			GroupRevisionID: testUUID("00000000-0000-4000-8000-000000000071"),
			AttemptID:       testUUID("00000000-0000-4000-8000-000000000072"),
			RuntimeRevision: 1,
			ReadyWindowID:   testUUID("00000000-0000-4000-8000-000000000073"),
			State:           "ready", PositionFrom: 1, PositionTo: 2,
			Members: []OperatorGoldenMemberInput{
				{ParticipantID: testUUID("00000000-0000-4000-8000-000000000074"), Ready: true},
				{ParticipantID: testUUID("00000000-0000-4000-8000-000000000075")},
			},
		}},
	}
}
