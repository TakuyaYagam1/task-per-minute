package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

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

	view, err := tournamentScoreboard(payload, map[uuid.UUID]string{participantID: "alice"})
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

	_, err := tournamentScoreboard(legacy, map[uuid.UUID]string{participantID: "alice"})
	require.ErrorIs(t, err, ErrTournamentSnapshotInvalid)
	_, err = tournamentScoreboard(unknown, map[uuid.UUID]string{participantID: "alice"})
	require.ErrorIs(t, err, ErrTournamentSnapshotInvalid)
}

func TestTournamentScoreboardBeforeFirstSwissResult(t *testing.T) {
	view, err := tournamentScoreboard([]byte(`{"entries":[]}`), nil)
	require.NoError(t, err)
	require.NotNil(t, view)
	require.Empty(t, view)

	for _, payload := range []string{`{}`, `{"entries":null}`} {
		_, err = tournamentScoreboard([]byte(payload), nil)
		require.ErrorIs(t, err, ErrTournamentSnapshotInvalid)
	}
}

func TestTournamentBracketReadsCanonicalRounds(t *testing.T) {
	firstID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	secondID := uuid.MustParse("10000000-0000-4000-8000-000000000002")
	payload := []byte(`{
		"rounds":[{
			"position":2,
			"first_participant_id":"10000000-0000-4000-8000-000000000001",
			"second_participant_id":"10000000-0000-4000-8000-000000000002",
			"state":"active",
			"first_wins":1,
			"second_wins":0
		}]
	}`)

	view, err := tournamentBracket(payload, map[uuid.UUID]string{
		firstID:  "alice",
		secondID: "bob",
	})
	require.NoError(t, err)
	require.Len(t, view, 1)
	require.Equal(t, tournamentBracketStageSemifinal, view[0].Stage)
	require.Equal(t, 2, view[0].Position)
	require.Equal(t, "alice", view[0].FirstDisplayName)
	require.Equal(t, "bob", view[0].SecondDisplayName)
}

func TestTournamentBracketAllowsCanonicalEmptyRoundsBeforePlayoffs(t *testing.T) {
	view, err := tournamentBracket([]byte(`{"rounds":[]}`), nil)

	require.NoError(t, err)
	require.NotNil(t, view)
	require.Empty(t, view)
}

func TestTournamentProjectionPayloadsRejectDuplicatePositions(t *testing.T) {
	firstID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	secondID := uuid.MustParse("10000000-0000-4000-8000-000000000002")
	names := map[uuid.UUID]string{firstID: "alice", secondID: "bob"}

	_, err := tournamentScoreboard([]byte(`{
		"entries":[
			{"participant_id":"10000000-0000-4000-8000-000000000001","position":1,"points":1},
			{"participant_id":"10000000-0000-4000-8000-000000000002","position":1,"points":0}
		]
	}`), names)
	require.ErrorIs(t, err, ErrTournamentSnapshotInvalid)

	_, err = tournamentBracket([]byte(`{
		"rounds":[
			{"position":1,"first_participant_id":"10000000-0000-4000-8000-000000000001","second_participant_id":"10000000-0000-4000-8000-000000000002","state":"planned"},
			{"position":1,"first_participant_id":"10000000-0000-4000-8000-000000000002","second_participant_id":"10000000-0000-4000-8000-000000000001","state":"planned"}
		]
	}`), names)
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
	}`), map[uuid.UUID]string{
		uuid.MustParse("10000000-0000-4000-8000-000000000001"): "alice",
	})
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
