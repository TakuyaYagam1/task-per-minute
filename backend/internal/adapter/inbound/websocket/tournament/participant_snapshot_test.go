package tournament

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/wirelimits"
)

func TestTournamentParticipantSnapshot(t *testing.T) {
	t.Parallel()

	tournamentID := testUUID("00000000-0000-4000-8000-000000000001")
	playerID := testUUID("00000000-0000-4000-8000-000000000010")
	input := testParticipantSnapshotInput(tournamentID, playerID)

	snapshot, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, input)
	if err != nil {
		t.Fatalf("NewParticipantSnapshot() error = %v", err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	requireJSONKeys(t, encoded, "tournament_id", "player_id", "revision", "last_sequence", "assignment", "opponent")
	requireNoSecretNames(t, encoded)

	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	requireJSONKeys(t, object["assignment"], "assignment_id", "attempt_id", "series_id", "game_id", "wave_id", "task")
	requireJSONKeys(t, object["opponent"], "display_name", "ready", "series_state", "score")

	t.Run("optional assignment and opponent are omitted", func(t *testing.T) {
		minimal := input
		minimal.Assignment = nil
		minimal.Opponent = nil
		got, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, minimal)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		requireJSONKeys(t, body, "tournament_id", "player_id", "revision", "last_sequence")
	})

	tests := []struct {
		name   string
		mutate func(*ParticipantSnapshotInput)
	}{
		{name: "zero revision", mutate: func(in *ParticipantSnapshotInput) { in.Revision = 0 }},
		{name: "cross tournament", mutate: func(in *ParticipantSnapshotInput) { in.TournamentID = testUUID("00000000-0000-4000-8000-000000000099") }},
		{name: "wrong player", mutate: func(in *ParticipantSnapshotInput) { in.PlayerID = testUUID("00000000-0000-4000-8000-000000000098") }},
		{name: "assignment belongs to another player", mutate: func(in *ParticipantSnapshotInput) {
			in.Assignment.PlayerID = testUUID("00000000-0000-4000-8000-000000000097")
		}},
		{name: "assignment belongs to another tournament", mutate: func(in *ParticipantSnapshotInput) {
			in.Assignment.TournamentID = testUUID("00000000-0000-4000-8000-000000000096")
		}},
		{name: "opponent is authenticated player", mutate: func(in *ParticipantSnapshotInput) { in.Opponent.PlayerID = playerID }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := testParticipantSnapshotInput(tournamentID, playerID)
			tt.mutate(&candidate)
			if _, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, candidate); err == nil {
				t.Fatalf("NewParticipantSnapshot() accepted %s", tt.name)
			}
		})
	}

	oversized := testParticipantSnapshotInput(tournamentID, playerID)
	oversized.Assignment.Task.Title = strings.Repeat("a", wirelimits.MaxStringBytes+1)
	if _, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, oversized); err == nil {
		t.Fatal("NewParticipantSnapshot() accepted an oversized string")
	}
}

func testParticipantSnapshot(t *testing.T, tournamentID uuid.UUID) ParticipantSnapshot {
	t.Helper()
	playerID := testUUID("00000000-0000-4000-8000-000000000010")
	snapshot, err := NewParticipantSnapshot(
		ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID},
		testParticipantSnapshotInput(tournamentID, playerID),
	)
	if err != nil {
		t.Fatalf("NewParticipantSnapshot() error = %v", err)
	}
	return snapshot
}

func testParticipantSnapshotInput(tournamentID, playerID uuid.UUID) ParticipantSnapshotInput {
	return ParticipantSnapshotInput{
		TournamentID: tournamentID,
		PlayerID:     playerID,
		Revision:     9,
		LastSequence: 12,
		Assignment: &ParticipantAssignmentInput{
			TournamentID: tournamentID,
			PlayerID:     playerID,
			AssignmentID: testUUID("00000000-0000-4000-8000-000000000020"),
			AttemptID:    testUUID("00000000-0000-4000-8000-000000000021"),
			SeriesID:     testUUID("00000000-0000-4000-8000-000000000022"),
			GameID:       testUUID("00000000-0000-4000-8000-000000000023"),
			WaveID:       testUUID("00000000-0000-4000-8000-000000000024"),
			Task: ParticipantTaskInput{
				SnapshotID:       testUUID("00000000-0000-4000-8000-000000000025"),
				TaskID:           testUUID("00000000-0000-4000-8000-000000000026"),
				Title:            "Packet relay",
				Category:         "web",
				Difficulty:       "medium",
				TimeLimitSeconds: 300,
			},
		},
		Opponent: &OpponentCompetitionInput{
			TournamentID: tournamentID,
			PlayerID:     testUUID("00000000-0000-4000-8000-000000000030"),
			DisplayName:  "blue",
			SeriesID:     testUUID("00000000-0000-4000-8000-000000000022"),
			Ready:        true,
			SeriesState:  "active",
			Score:        0,
		},
	}
}
