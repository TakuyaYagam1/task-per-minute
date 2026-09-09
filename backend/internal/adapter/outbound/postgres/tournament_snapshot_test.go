package postgres

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
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
