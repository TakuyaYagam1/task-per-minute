package tournament

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/wirelimits"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestTournamentPublicSnapshot(t *testing.T) {
	t.Parallel()

	tournamentID := testUUID("00000000-0000-4000-8000-000000000001")
	input := testPublicSnapshotInput(tournamentID)
	snapshot, err := NewPublicSnapshot(tournamentID, input)
	if err != nil {
		t.Fatalf("NewPublicSnapshot() error = %v", err)
	}
	input.Scoreboard[0].DisplayName = "mutated"
	input.Draft.Actions[0].Category = "mutated"

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	requireJSONKeys(t, encoded, "revision", "last_sequence", "tournament", "scoreboard", "bracket", "live_series", "official_results", "draft")
	requireNoSecretNames(t, encoded)

	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	requireJSONKeys(t, object["tournament"], "tournament_id", "preset", "state", "roster_size", "started_at", "finished_at")
	var decoded PublicSnapshot
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Len(t, decoded.Bracket, 1)
	require.Nil(t, decoded.Bracket[0].ScheduledAt)
	require.Len(t, decoded.LiveSeries, 1)
	require.Equal(t, "swiss", decoded.LiveSeries[0].Stage)
	require.NotNil(t, decoded.LiveSeries[0].RoundNumber)
	require.Equal(t, 1, *decoded.LiveSeries[0].RoundNumber)
	require.Nil(t, decoded.LiveSeries[0].ScheduledAt)
	if string(encoded) == "" || containsJSONText(encoded, "mutated") {
		t.Fatalf("snapshot retained mutable source slices: %s", encoded)
	}

	t.Run("nil collections become empty allowlisted arrays", func(t *testing.T) {
		minimal := testPublicSnapshotInput(tournamentID)
		minimal.Scoreboard = nil
		minimal.Bracket = nil
		minimal.LiveSeries = nil
		minimal.OfficialResults = nil
		minimal.Draft = nil
		got, err := NewPublicSnapshot(tournamentID, minimal)
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		requireJSONKeys(t, body, "revision", "last_sequence", "tournament", "scoreboard", "bracket", "live_series", "official_results")
	})

	wrongTournament := testPublicSnapshotInput(tournamentID)
	wrongTournament.LiveSeries[0].TournamentID = testUUID("00000000-0000-4000-8000-000000000099")
	if _, err := NewPublicSnapshot(tournamentID, wrongTournament); err == nil {
		t.Fatal("NewPublicSnapshot() accepted cross-tournament series")
	}

	nonUTC := testPublicSnapshotInput(tournamentID)
	nonUTC.OfficialResults[0].RecordedAt = time.Date(2026, 9, 2, 12, 0, 0, 0, time.FixedZone("MSK", 3*60*60))
	if _, err := NewPublicSnapshot(tournamentID, nonUTC); err == nil {
		t.Fatal("NewPublicSnapshot() accepted non-UTC result timestamp")
	}

	for _, state := range []domain.TournamentState{
		domain.TournamentStateDraft,
		domain.TournamentStateRegistration,
		domain.TournamentStateRosterLocked,
		domain.TournamentStateSwiss,
		domain.TournamentStateGolden,
		domain.TournamentStatePlayoffs,
		domain.TournamentStateTechnicalPause,
		domain.TournamentStateCompleted,
		domain.TournamentStateCancelled,
	} {
		t.Run("accepts state "+state.String(), func(t *testing.T) {
			candidate := testPublicSnapshotInput(tournamentID)
			candidate.Tournament.Preset = domain.TournamentPresetV1.String()
			candidate.Tournament.State = state.String()
			if _, err := NewPublicSnapshot(tournamentID, candidate); err != nil {
				t.Fatalf("NewPublicSnapshot() rejected domain state %q: %v", state, err)
			}
		})
	}

	for _, mutation := range []struct {
		name   string
		preset string
		state  string
	}{
		{name: "unknown preset", preset: "custom", state: "swiss"},
		{name: "unknown state", preset: "tournament_v1", state: "active"},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			candidate := testPublicSnapshotInput(tournamentID)
			candidate.Tournament.Preset = mutation.preset
			candidate.Tournament.State = mutation.state
			if _, err := NewPublicSnapshot(tournamentID, candidate); err == nil {
				t.Fatalf("NewPublicSnapshot() accepted preset %q and state %q", mutation.preset, mutation.state)
			}
		})
	}

	oversizedLabel := testPublicSnapshotInput(tournamentID)
	oversizedLabel.Scoreboard[0].DisplayName = strings.Repeat("a", wirelimits.MaxStringBytes+1)
	if _, err := NewPublicSnapshot(tournamentID, oversizedLabel); err == nil {
		t.Fatal("NewPublicSnapshot() accepted an oversized string")
	}

	oversizedCollection := testPublicSnapshotInput(tournamentID)
	oversizedCollection.Scoreboard = make([]PublicScoreboardEntryInput, wirelimits.MaxCollectionItems+1)
	if _, err := NewPublicSnapshot(tournamentID, oversizedCollection); err == nil {
		t.Fatal("NewPublicSnapshot() accepted an oversized collection")
	}
}

func testPublicSnapshot(t *testing.T, tournamentID uuid.UUID) PublicSnapshot {
	t.Helper()
	snapshot, err := NewPublicSnapshot(tournamentID, testPublicSnapshotInput(tournamentID))
	if err != nil {
		t.Fatalf("NewPublicSnapshot() error = %v", err)
	}
	return snapshot
}

func testPublicSnapshotInput(tournamentID uuid.UUID) PublicSnapshotInput {
	startedAt := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	roundNumber := 1
	return PublicSnapshotInput{
		Revision:     9,
		LastSequence: 12,
		Tournament: PublicTournamentInput{
			TournamentID: tournamentID,
			Preset:       "tournament_v1",
			State:        "swiss",
			RosterSize:   8,
			StartedAt:    &startedAt,
		},
		Scoreboard:      []PublicScoreboardEntryInput{{TournamentID: tournamentID, Rank: 1, DisplayName: "red", Points: 3, Buchholz: 2, EffectiveTimeMS: 4100}},
		Bracket:         []PublicBracketMatchInput{{TournamentID: tournamentID, Stage: "semifinal", Position: 1, FirstDisplayName: "red", SecondDisplayName: "blue", FirstWins: 1, SecondWins: 0, State: "active"}},
		LiveSeries:      []PublicSeriesInput{{TournamentID: tournamentID, SeriesID: testUUID("00000000-0000-4000-8000-000000000022"), Stage: "swiss", RoundNumber: &roundNumber, Format: "bo3", State: "active", FirstDisplayName: "red", SecondDisplayName: "blue", FirstWins: 1, SecondWins: 0, CurrentGamePosition: 2}},
		OfficialResults: []PublicOfficialResultInput{{TournamentID: tournamentID, RevisionID: testUUID("00000000-0000-4000-8000-000000000040"), SeriesID: testUUID("00000000-0000-4000-8000-000000000022"), State: "completed", WinnerDisplayName: "red", FirstWins: 2, SecondWins: 0, RecordedAt: time.Date(2026, 9, 2, 8, 20, 0, 0, time.UTC)}},
		Draft: &PublicDraftInput{
			TournamentID:       tournamentID,
			SeriesID:           testUUID("00000000-0000-4000-8000-000000000022"),
			Format:             "bo3",
			State:              "active",
			Pool:               []string{"web", "crypto"},
			SelectedCategories: []string{"web"},
			Actions:            []PublicDraftActionInput{{Turn: 1, Action: "pick", Category: "web", ActorDisplayName: "red", OccurredAt: time.Date(2026, 9, 2, 8, 10, 0, 0, time.UTC)}},
		},
	}
}

func containsJSONText(encoded []byte, text string) bool {
	var value any
	if json.Unmarshal(encoded, &value) != nil {
		return false
	}
	return containsText(value, text)
}

func containsText(value any, text string) bool {
	switch typed := value.(type) {
	case string:
		return typed == text
	case []any:
		for _, item := range typed {
			if containsText(item, text) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsText(item, text) {
				return true
			}
		}
	}
	return false
}
