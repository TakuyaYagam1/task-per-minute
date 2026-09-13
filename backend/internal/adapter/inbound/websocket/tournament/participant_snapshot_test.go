package tournament

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

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

	t.Run("Golden ready state is taskless until started timestamps are committed", func(t *testing.T) {
		ready := input
		ready.Assignment = nil
		ready.Opponent = nil
		ready.Golden = &ParticipantGoldenInput{
			GroupID:         testUUID("00000000-0000-4000-8000-000000000040"),
			GroupRevisionID: testUUID("00000000-0000-4000-8000-000000000041"),
			AttemptID:       testUUID("00000000-0000-4000-8000-000000000042"),
			RuntimeRevision: 3,
			ReadyWindowID:   testUUID("00000000-0000-4000-8000-000000000043"),
			State:           "ready", Ready: true,
		}
		got, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, ready)
		require.NoError(t, err)
		require.NotNil(t, got.Golden)
		require.Nil(t, got.Golden.Task)
		body, err := json.Marshal(got)
		require.NoError(t, err)
		var object map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &object))
		var golden map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(object["golden"], &golden))
		require.NotContains(t, golden, "task")
	})

	t.Run("Golden task requires committed start and exact deadline", func(t *testing.T) {
		started := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
		active := input
		active.Assignment = nil
		active.Opponent = nil
		active.Golden = &ParticipantGoldenInput{
			GroupID:         testUUID("00000000-0000-4000-8000-000000000050"),
			GroupRevisionID: testUUID("00000000-0000-4000-8000-000000000051"),
			AttemptID:       testUUID("00000000-0000-4000-8000-000000000052"),
			RuntimeRevision: 4,
			ReadyWindowID:   testUUID("00000000-0000-4000-8000-000000000053"),
			State:           "active", Ready: true,
			StartedAt: &started,
			Deadline:  func() *time.Time { value := started.Add(180 * time.Second); return &value }(),
			Task: &ParticipantGoldenTaskInput{
				AssignmentID: testUUID("00000000-0000-4000-8000-000000000054"),
				SnapshotID:   testUUID("00000000-0000-4000-8000-000000000055"),
				TaskID:       testUUID("00000000-0000-4000-8000-000000000056"),
				Version:      2, Title: "Golden task", Description: "Inspect the immutable task",
				Category: "web", Difficulty: "medium", TimeLimitSeconds: 180,
				TaskURL:             func() *string { value := "https://golden.example/tasks/56"; return &value }(),
				SourceFileAvailable: true,
			},
		}
		got, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, active)
		require.NoError(t, err)
		require.NotNil(t, got.Golden)
		require.NotNil(t, got.Golden.Task)
		body, err := json.Marshal(got)
		require.NoError(t, err)
		var object map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(body, &object))
		var golden map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(object["golden"], &golden))
		requireJSONKeys(t, golden["task"], "assignment_id", "snapshot_id", "task_id", "version", "title", "description", "category", "difficulty", "time_limit_seconds", "task_url", "source_file_available")
		requireNoSecretNames(t, golden["task"])

		t.Run("optional task URL may be omitted", func(t *testing.T) {
			candidate := active
			goldenCopy := *active.Golden
			taskCopy := *active.Golden.Task
			taskCopy.TaskURL = nil
			goldenCopy.Task = &taskCopy
			candidate.Golden = &goldenCopy
			got, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, candidate)
			require.NoError(t, err)
			body, err := json.Marshal(got)
			require.NoError(t, err)
			require.NotContains(t, string(body), `"task_url"`)
		})

		invalidTasks := []struct {
			name   string
			mutate func(*ParticipantSnapshotInput)
		}{
			{name: "non-positive version", mutate: func(input *ParticipantSnapshotInput) { input.Golden.Task.Version = 0 }},
			{name: "blank description", mutate: func(input *ParticipantSnapshotInput) { input.Golden.Task.Description = "   " }},
			{name: "blank task URL", mutate: func(input *ParticipantSnapshotInput) {
				input.Golden.Task.TaskURL = func() *string { value := "  "; return &value }()
			}},
			{name: "deadline is not exactly 180 seconds", mutate: func(input *ParticipantSnapshotInput) {
				value := input.Golden.Deadline.Add(time.Second)
				input.Golden.Deadline = &value
			}},
		}
		for _, test := range invalidTasks {
			t.Run("rejects "+test.name, func(t *testing.T) {
				candidate := active
				golden := *active.Golden
				goldenTask := *active.Golden.Task
				candidate.Golden = &golden
				candidate.Golden.Task = &goldenTask
				test.mutate(&candidate)
				if _, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, candidate); err == nil {
					t.Fatalf("NewParticipantSnapshot() accepted invalid Golden task: %s", test.name)
				}
			})
		}
	})

	t.Run("Golden runtime fence is consecutive on the wire", func(t *testing.T) {
		firstInput := input
		firstInput.Assignment = nil
		firstInput.Opponent = nil
		firstInput.Golden = &ParticipantGoldenInput{
			GroupID:         testUUID("00000000-0000-4000-8000-000000000080"),
			GroupRevisionID: testUUID("00000000-0000-4000-8000-000000000081"),
			AttemptID:       testUUID("00000000-0000-4000-8000-000000000082"),
			RuntimeRevision: 1,
			ReadyWindowID:   testUUID("00000000-0000-4000-8000-000000000083"),
			State:           "ready", Ready: true,
		}
		first, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, firstInput)
		require.NoError(t, err)
		secondInput := firstInput
		secondInput.Golden = &ParticipantGoldenInput{}
		*secondInput.Golden = *firstInput.Golden
		secondInput.Golden.RuntimeRevision = first.Golden.RuntimeRevision + 1
		second, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, secondInput)
		require.NoError(t, err)
		require.Equal(t, first.Revision, second.Revision)
		require.Equal(t, first.Golden.RuntimeRevision+1, second.Golden.RuntimeRevision)

		for index, snapshot := range []ParticipantSnapshot{first, second} {
			body, marshalErr := json.Marshal(snapshot)
			require.NoError(t, marshalErr)
			var envelope struct {
				Golden struct {
					RuntimeRevision int64 `json:"runtime_revision"`
				} `json:"golden"`
			}
			require.NoError(t, json.Unmarshal(body, &envelope))
			require.Equal(t, first.Golden.RuntimeRevision+int64(index), envelope.Golden.RuntimeRevision)
		}
	})

	t.Run("rejects active Golden state without a task", func(t *testing.T) {
		started := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
		activeWithoutTask := input
		activeWithoutTask.Assignment = nil
		activeWithoutTask.Opponent = nil
		activeWithoutTask.Golden = &ParticipantGoldenInput{
			GroupID:         testUUID("00000000-0000-4000-8000-000000000060"),
			GroupRevisionID: testUUID("00000000-0000-4000-8000-000000000061"),
			AttemptID:       testUUID("00000000-0000-4000-8000-000000000062"),
			RuntimeRevision: 5,
			ReadyWindowID:   testUUID("00000000-0000-4000-8000-000000000063"),
			State:           "active", Ready: true, StartedAt: &started,
			Deadline: func() *time.Time {
				value := started.Add(180 * time.Second)
				return &value
			}(),
		}
		_, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, activeWithoutTask)
		require.Error(t, err)
	})

	t.Run("rejects pre-start Golden state with a task", func(t *testing.T) {
		preStartWithTask := input
		preStartWithTask.Assignment = nil
		preStartWithTask.Opponent = nil
		preStartWithTask.Golden = &ParticipantGoldenInput{
			GroupID:         testUUID("00000000-0000-4000-8000-000000000070"),
			GroupRevisionID: testUUID("00000000-0000-4000-8000-000000000071"),
			AttemptID:       testUUID("00000000-0000-4000-8000-000000000072"),
			RuntimeRevision: 6,
			ReadyWindowID:   testUUID("00000000-0000-4000-8000-000000000073"),
			State:           "ready", Ready: true,
			Task: &ParticipantGoldenTaskInput{
				AssignmentID: testUUID("00000000-0000-4000-8000-000000000074"),
				SnapshotID:   testUUID("00000000-0000-4000-8000-000000000075"),
				TaskID:       testUUID("00000000-0000-4000-8000-000000000076"),
				Version:      1, Title: "Golden task", Description: "Inspect the immutable task",
				Category: "web", Difficulty: "medium", TimeLimitSeconds: 180,
			},
		}
		_, err := NewParticipantSnapshot(ParticipantSnapshotScope{TournamentID: tournamentID, PlayerID: playerID}, preStartWithTask)
		require.Error(t, err)
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
