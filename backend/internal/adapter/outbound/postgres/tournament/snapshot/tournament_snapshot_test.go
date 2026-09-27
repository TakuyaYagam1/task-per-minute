package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestTournamentScoreboardReadsCanonicalEntries(t *testing.T) {
	participantID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	payload := []byte(`{
		"entries":[{
			"participant_id":"10000000-0000-4000-8000-000000000001",
			"position":1,
			"points":3,
			"buchholz":7,
			"effective_time":2500000000
		}]
	}`)

	view, err := tournamentScoreboard(payload, nil, []sqlc.ListTournamentReadParticipantsRow{{ParticipantID: participantID, DisplayName: "alice", Seed: 1}})
	require.NoError(t, err)
	require.Len(t, view, 1)
	require.Equal(t, 2500, int(view[0].EffectiveTimeMS))
	require.Equal(t, "alice", view[0].DisplayName)
	require.Equal(t, 3, view[0].Points)
}

func TestTournamentScoreboardRejectsLegacyOrUnknownMembers(t *testing.T) {
	participantID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	legacy := []byte(`{"standings":[{"participant_id":"10000000-0000-4000-8000-000000000001"}]}`)
	unknown := []byte(`{
		"entries":[{
			"participant_id":"10000000-0000-4000-8000-000000000002",
			"position":1,
			"points":0,
			"buchholz":0,
			"effective_time":0
		}]
	}`)

	_, err := tournamentScoreboard(legacy, nil, []sqlc.ListTournamentReadParticipantsRow{{ParticipantID: participantID, DisplayName: "alice", Seed: 1}})
	require.ErrorIs(t, err, ErrTournamentSnapshotInvalid)
	_, err = tournamentScoreboard(unknown, nil, []sqlc.ListTournamentReadParticipantsRow{{ParticipantID: participantID, DisplayName: "alice", Seed: 1}})
	require.ErrorIs(t, err, ErrTournamentSnapshotInvalid)
}

func TestTournamentScoreboardBeforeFirstSwissResult(t *testing.T) {
	view, err := tournamentScoreboard([]byte(`{"entries":[]}`), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, view)
	require.Empty(t, view)

	for _, payload := range []string{`{}`, `{"entries":null}`} {
		_, err = tournamentScoreboard([]byte(payload), nil, nil)
		require.ErrorIs(t, err, ErrTournamentSnapshotInvalid)
	}
}

func TestTournamentScoreboardPublishesRosterStatusesAndGoldenOrder(t *testing.T) {
	ids := []uuid.UUID{
		uuid.MustParse("10000000-0000-4000-8000-000000000001"),
		uuid.MustParse("10000000-0000-4000-8000-000000000002"),
		uuid.MustParse("10000000-0000-4000-8000-000000000003"),
		uuid.MustParse("10000000-0000-4000-8000-000000000004"),
		uuid.MustParse("10000000-0000-4000-8000-000000000005"),
	}
	participants := []sqlc.ListTournamentReadParticipantsRow{
		{ParticipantID: ids[0], DisplayName: "alice", Seed: 1},
		{ParticipantID: ids[1], DisplayName: "bob", Seed: 2},
		{ParticipantID: ids[2], DisplayName: "carol", Seed: 3},
		{ParticipantID: ids[3], DisplayName: "dave", Seed: 4},
		{ParticipantID: ids[4], DisplayName: "erin", Seed: 5},
	}
	standings := []byte(`{"entries":[
		{"participant_id":"10000000-0000-4000-8000-000000000002","position":1,"points":6,"wins":2,"losses":0,"bye_count":0,"buchholz":4,"effective_time":1000000},
		{"participant_id":"10000000-0000-4000-8000-000000000001","position":2,"points":3,"wins":1,"losses":1,"bye_count":0,"buchholz":3,"effective_time":2000000},
		{"participant_id":"10000000-0000-4000-8000-000000000004","position":3,"points":3,"wins":0,"losses":1,"bye_count":1,"buchholz":2,"effective_time":3000000},
		{"participant_id":"10000000-0000-4000-8000-000000000003","position":4,"points":0,"wins":0,"losses":2,"bye_count":0,"buchholz":1,"effective_time":4000000},
		{"participant_id":"10000000-0000-4000-8000-000000000005","position":5,"points":0,"wins":0,"losses":2,"bye_count":0,"buchholz":0,"effective_time":5000000}
	],"tie_groups":[{"position_from":2,"position_to":3,"impactful":true,"participant_ids":["10000000-0000-4000-8000-000000000001","10000000-0000-4000-8000-000000000004"]}]}`)
	topFour := []byte(`{"participants":[
		"10000000-0000-4000-8000-000000000004",
		"10000000-0000-4000-8000-000000000002",
		"10000000-0000-4000-8000-000000000001",
		"10000000-0000-4000-8000-000000000003"
	]}`)

	view, err := tournamentScoreboard(standings, topFour, participants)
	require.NoError(t, err)
	require.Len(t, view, 5)
	require.Equal(t, []string{"dave", "bob", "alice", "carol", "erin"}, []string{view[0].DisplayName, view[1].DisplayName, view[2].DisplayName, view[3].DisplayName, view[4].DisplayName})
	for _, entry := range view[:4] {
		require.Equal(t, "qualified", entry.QualificationStatus)
		require.False(t, entry.ProvisionalTie)
	}
	require.Equal(t, "eliminated", view[4].QualificationStatus)
	require.False(t, view[4].ProvisionalTie)
	require.Equal(t, 1, view[0].ByeCount)
	require.Equal(t, 2, view[1].Wins)
}

func TestTournamentScoreboardSynthesizesZeroStateForRoster(t *testing.T) {
	participants := []sqlc.ListTournamentReadParticipantsRow{
		{ParticipantID: uuid.MustParse("10000000-0000-4000-8000-000000000001"), DisplayName: "alice", Seed: 1},
		{ParticipantID: uuid.MustParse("10000000-0000-4000-8000-000000000002"), DisplayName: "bob", Seed: 2},
	}
	view, err := tournamentScoreboard([]byte(`{"entries":[]}`), []byte(`{"participants":[]}`), participants)
	require.NoError(t, err)
	require.Len(t, view, 2)
	require.Equal(t, "alice", view[0].DisplayName)
	require.Equal(t, "bob", view[1].DisplayName)
	for _, entry := range view {
		require.Equal(t, 0, entry.Points)
		require.Equal(t, 0, entry.Wins)
		require.Equal(t, 0, entry.Losses)
		require.Equal(t, 0, entry.ByeCount)
		require.Equal(t, "pending", entry.QualificationStatus)
	}
}

func TestTournamentBracketReadsCanonicalRounds(t *testing.T) {
	firstID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	secondID := uuid.MustParse("10000000-0000-4000-8000-000000000002")
	thirdID := uuid.MustParse("10000000-0000-4000-8000-000000000003")
	fourthID := uuid.MustParse("10000000-0000-4000-8000-000000000004")
	payload := []byte(`{
		"rounds":[
			{
				"position":2,
				"first_participant_id":"10000000-0000-4000-8000-000000000001",
				"second_participant_id":"10000000-0000-4000-8000-000000000002",
				"state":"active",
				"first_wins":1,
				"second_wins":0
			},
			{
				"position":1,
				"first_participant_id":"10000000-0000-4000-8000-000000000003",
				"second_participant_id":"10000000-0000-4000-8000-000000000004",
				"state":"planned",
				"first_wins":0,
				"second_wins":0
			}
		]
	}`)

	view, err := tournamentBracket(payload, map[uuid.UUID]string{
		firstID:  "alice",
		secondID: "bob",
		thirdID:  "carol",
		fourthID: "dave",
	})
	require.NoError(t, err)
	require.Len(t, view, 3)
	require.Equal(t, tournamentBracketStageSemifinal, view[0].Stage)
	require.Equal(t, 1, view[0].Position)
	require.Equal(t, "bo1", view[0].Format)
	require.Equal(t, "carol", *view[0].FirstDisplayName)
	require.Equal(t, "dave", *view[0].SecondDisplayName)
	require.Equal(t, tournamentBracketStageSemifinal, view[1].Stage)
	require.Equal(t, 2, view[1].Position)
	require.Equal(t, tournamentBracketStageFinal, view[2].Stage)
	require.Equal(t, 1, view[2].Position)
	require.Equal(t, "bo3", view[2].Format)
	require.Nil(t, view[2].FirstDisplayName)
	require.Nil(t, view[2].SecondDisplayName)
}

func TestTournamentBracketReadsCorrectionMaterializationRounds(t *testing.T) {
	firstID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	secondID := uuid.MustParse("10000000-0000-4000-8000-000000000002")
	thirdID := uuid.MustParse("10000000-0000-4000-8000-000000000003")
	fourthID := uuid.MustParse("10000000-0000-4000-8000-000000000004")
	payload := []byte(`{
		"rounds":[
			{"Position":1,"SeriesID":"20000000-0000-4000-8000-000000000001","FirstParticipantID":"10000000-0000-4000-8000-000000000001","SecondParticipantID":"10000000-0000-4000-8000-000000000002","State":"ready","FirstWins":0,"SecondWins":0},
			{"Position":2,"SeriesID":"20000000-0000-4000-8000-000000000002","FirstParticipantID":"10000000-0000-4000-8000-000000000003","SecondParticipantID":"10000000-0000-4000-8000-000000000004","State":"ready","FirstWins":0,"SecondWins":0}
		]
	}`)

	view, err := tournamentBracket(payload, map[uuid.UUID]string{
		firstID:  "alice",
		secondID: "bob",
		thirdID:  "carol",
		fourthID: "dave",
	})

	require.NoError(t, err)
	require.Len(t, view, 3)
	require.Equal(t, tournamentBracketStageSemifinal, view[0].Stage)
	require.Equal(t, "alice", *view[0].FirstDisplayName)
	require.Equal(t, "bob", *view[0].SecondDisplayName)
	require.Equal(t, "ready", view[0].State)
	require.Equal(t, tournamentBracketStageFinal, view[2].Stage)
}

func TestTournamentBracketAllowsCanonicalEmptyRoundsBeforePlayoffs(t *testing.T) {
	view, err := tournamentBracket([]byte(`{"rounds":[]}`), nil)

	require.NoError(t, err)
	require.NotNil(t, view)
	require.Empty(t, view)
}

func TestTournamentBracketRejectsIncompletePlayoffTopology(t *testing.T) {
	firstID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	secondID := uuid.MustParse("10000000-0000-4000-8000-000000000002")
	payload := []byte(`{
		"rounds":[{
			"position":1,
			"first_participant_id":"10000000-0000-4000-8000-000000000001",
			"second_participant_id":"10000000-0000-4000-8000-000000000002",
			"state":"planned"
		}]
	}`)

	_, err := tournamentBracket(payload, map[uuid.UUID]string{firstID: "alice", secondID: "bob"})

	require.ErrorIs(t, err, ErrTournamentSnapshotInvalid)
}

func TestTournamentProjectionPayloadsRejectDuplicatePositions(t *testing.T) {
	firstID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	secondID := uuid.MustParse("10000000-0000-4000-8000-000000000002")
	participants := []sqlc.ListTournamentReadParticipantsRow{{ParticipantID: firstID, DisplayName: "alice", Seed: 1}, {ParticipantID: secondID, DisplayName: "bob", Seed: 2}}

	_, err := tournamentScoreboard([]byte(`{
		"entries":[
			{"participant_id":"10000000-0000-4000-8000-000000000001","position":1,"points":1},
			{"participant_id":"10000000-0000-4000-8000-000000000002","position":1,"points":0}
		]
	}`), nil, participants)
	require.ErrorIs(t, err, ErrTournamentSnapshotInvalid)

	_, err = tournamentBracket([]byte(`{
		"rounds":[
			{"position":1,"first_participant_id":"10000000-0000-4000-8000-000000000001","second_participant_id":"10000000-0000-4000-8000-000000000002","state":"planned"},
			{"position":1,"first_participant_id":"10000000-0000-4000-8000-000000000002","second_participant_id":"10000000-0000-4000-8000-000000000001","state":"planned"}
		]
	}`), map[uuid.UUID]string{firstID: "alice", secondID: "bob"})
	require.ErrorIs(t, err, ErrTournamentSnapshotInvalid)
}

func TestTournamentSnapshotHelpersPreserveEmptyAndNullableValues(t *testing.T) {
	values, err := tournamentStringList([]byte(`[]`), "draft selections")
	require.NoError(t, err)
	require.NotNil(t, values)
	require.Empty(t, values)

	seriesID, err := optionalTournamentUUID("")
	require.NoError(t, err)
	require.Nil(t, seriesID)

	_, err = optionalTournamentUUID("not-a-uuid")
	require.ErrorIs(t, err, ErrTournamentSnapshotInvalid)

	required, err := requiredTournamentUUID("10000000-0000-4000-8000-000000000001")
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, required)
	_, err = requiredTournamentUUID("")
	require.ErrorIs(t, err, ErrTournamentSnapshotInvalid)

	entry, err := tournamentScoreboard([]byte(`{
		"entries":[{
			"participant_id":"10000000-0000-4000-8000-000000000001",
			"position":1,
			"points":0,
			"buchholz":0,
			"effective_time":1000000
		}]
	}`), nil, []sqlc.ListTournamentReadParticipantsRow{{
		ParticipantID: uuid.MustParse("10000000-0000-4000-8000-000000000001"), DisplayName: "alice", Seed: 1,
	}})
	require.NoError(t, err)
	require.Equal(t, int64(1), entry[0].EffectiveTimeMS)
}

func TestPublicSnapshotCursorConflictSemantics(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("10000000-0000-4000-8000-000000000010")
	current := usecase.SnapshotCursor{ProjectionRevision: 7, EventSequence: 12}
	tests := []struct {
		name             string
		requested        *usecase.SnapshotCursor
		wantConflict     bool
		wantProjection   int64
		wantEvent        int64
		wantCurrentProj  int64
		wantCurrentEvent int64
	}{
		{name: "nil cursor"},
		{
			name:           "older cursor",
			requested:      &usecase.SnapshotCursor{ProjectionRevision: 6, EventSequence: 11},
			wantProjection: 6,
			wantEvent:      11,
		},
		{
			name:           "equal cursor",
			requested:      &usecase.SnapshotCursor{ProjectionRevision: 7, EventSequence: 12},
			wantProjection: 7,
			wantEvent:      12,
		},
		{
			name:             "future projection",
			requested:        &usecase.SnapshotCursor{ProjectionRevision: 8, EventSequence: 12},
			wantConflict:     true,
			wantProjection:   8,
			wantEvent:        12,
			wantCurrentProj:  7,
			wantCurrentEvent: 12,
		},
		{
			name:             "future event sequence",
			requested:        &usecase.SnapshotCursor{ProjectionRevision: 7, EventSequence: 13},
			wantConflict:     true,
			wantProjection:   7,
			wantEvent:        13,
			wantCurrentProj:  7,
			wantCurrentEvent: 12,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.requested == nil {
				require.NoError(t, publicSnapshotCursorConflict(tournamentID, usecase.SnapshotCursor{}, current))
				return
			}
			err := publicSnapshotCursorConflict(tournamentID, *tt.requested, current)
			if !tt.wantConflict {
				require.NoError(t, err)
				return
			}
			var conflict *usecase.PublicSnapshotCursorConflictError
			require.ErrorAs(t, err, &conflict)
			require.ErrorIs(t, err, usecase.ErrPublicSnapshotCursorConflict)
			require.ErrorIs(t, err, domain.ErrConflict)
			require.Equal(t, tournamentID, conflict.TournamentID)
			require.Equal(t, tt.wantProjection, conflict.RequestedProjectionRevision)
			require.Equal(t, tt.wantEvent, conflict.RequestedEventSequence)
			require.Equal(t, tt.wantCurrentProj, conflict.CurrentProjectionRevision)
			require.Equal(t, tt.wantCurrentEvent, conflict.CurrentEventSequence)
		})
	}
}

func TestTournamentReadCursorSQLUsesDurableOutboxWatermark(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "queries", "tournament_read.sql"))
	require.NoError(t, err)
	query := string(contents)
	projectionPayloads := strings.Index(query, "-- name: GetTournamentReadProjectionPayloads")
	require.Positive(t, projectionPayloads)
	cursorQuery := query[:projectionPayloads]
	require.Contains(t, cursorQuery, "COALESCE(outbox_cursor.next_sequence - 1, 0)::BIGINT AS event_sequence")
	require.Contains(t, cursorQuery, "LEFT JOIN tournament_outbox_cursors AS outbox_cursor")
	require.Contains(t, cursorQuery, "outbox_cursor.tournament_id = revision.tournament_id")
	require.NotContains(t, cursorQuery, "MAX(outbox_event.sequence)")
}

func TestPublicSeriesReadSQLUsesStoredStageLineageAndNullableSchedule(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "queries", "tournament_read.sql"))
	require.NoError(t, err)
	query := string(contents)
	start := strings.Index(query, "-- name: ListPublicTournamentReadSeries")
	require.NotEqual(t, -1, start)
	end := strings.Index(query[start+1:], "-- name:")
	if end == -1 {
		end = len(query) - start - 1
	}
	seriesQuery := query[start : start+1+end]
	require.Contains(t, seriesQuery, "COALESCE(series_stage.stage, 'golden')::TEXT AS stage")
	require.Contains(t, seriesQuery, "COALESCE(series_stage.round_number, 0)::SMALLINT AS round_number")
	require.Contains(t, seriesQuery, "FROM wave_series AS linked_series")
	require.Contains(t, seriesQuery, "FROM tournament_stage_playoff_semifinals AS semifinal")
	require.Contains(t, seriesQuery, "FROM tournament_stage_playoff_finals AS final_stage")
	require.Contains(t, seriesQuery, "NULL::TIMESTAMPTZ AS scheduled_at")
	require.NotContains(t, seriesQuery, "started_at AS scheduled_at")
	require.NotContains(t, seriesQuery, "created_at AS scheduled_at")
	require.Contains(t, seriesQuery, "current_game_solve.submission_received_at AS current_game_solve_submission_received_at")
	require.Contains(t, seriesQuery, "official_result_heads AS result_head")
	require.Contains(t, seriesQuery, "result_revision.result_reason = 'solved'")
	require.Contains(t, seriesQuery, "submission.status = 'accepted'")
	require.Contains(t, seriesQuery, "submission.received_at >= attempt.started_at")
	require.NotContains(t, seriesQuery, "current_game_finished_at - current_game_started_at")
}

func TestPublicTournamentReadSolveTimeUsesAcceptedSubmissionTimestamp(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	receivedAt := startedAt.Add(1234 * time.Millisecond)
	reason := string(domain.GameResultReasonSolved)
	value, err := publicTournamentReadSolveTime(&receivedAt, &startedAt, domain.GameStateCompleted, &reason)
	require.NoError(t, err)
	require.NotNil(t, value)
	require.Equal(t, int64(1234), *value)

	zero := startedAt
	value, err = publicTournamentReadSolveTime(&zero, &startedAt, domain.GameStateCompleted, &reason)
	require.NoError(t, err)
	require.NotNil(t, value)
	require.Equal(t, int64(0), *value)

	value, err = publicTournamentReadSolveTime(nil, &startedAt, domain.GameStateCompleted, &reason)
	require.NoError(t, err)
	require.Nil(t, value)

	_, err = publicTournamentReadSolveTime(&receivedAt, &startedAt, domain.GameStateCompleted, func() *string {
		value := string(domain.GameResultReasonSurrender)
		return &value
	}())
	require.ErrorIs(t, err, ErrTournamentSnapshotInvalid)
}

func TestPublicSeriesReadSQLUsesFixedTournamentTaskDurationForActiveDeadline(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "queries", "tournament_read.sql"))
	require.NoError(t, err)
	start := strings.Index(string(contents), "-- name: ListPublicTournamentReadSeries")
	require.NotEqual(t, -1, start)
	query := string(contents)[start:]
	require.Contains(t, query, "attempt.started_at + INTERVAL '180 seconds'")
	require.NotContains(t, query, "active_snapshot.time_limit")
}
