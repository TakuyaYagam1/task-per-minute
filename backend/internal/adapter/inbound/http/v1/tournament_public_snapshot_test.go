package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	inboundmocks "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound/mocks"
)

func TestGetPublicSnapshotPropagatesCursorAndMapsAllowlistedCollections(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("20000000-0000-4000-8000-000000000001")
	requested := &api.PublicRecoveryCursor{ProjectionRevision: 5, EventSequence: 8}
	var gotQuery inbound.PublicSnapshotQuery
	reader := inboundmocks.NewMockTournamentSnapshotUseCase(t)
	reader.EXPECT().PublicSnapshot(mock.Anything, mock.Anything).
		Run(func(_ context.Context, query inbound.PublicSnapshotQuery) { gotQuery = query }).
		Return(publicSnapshotHTTPView(tournamentID), nil).
		Once()

	server := New(Dependencies{TournamentSnapshots: reader})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tournaments/"+tournamentID.String()+"/snapshot", nil)
	server.GetPublicSnapshot(recorder, request, tournamentID, api.GetPublicSnapshotParams{Cursor: requested})

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, gotQuery.Cursor)
	require.Equal(t, int64(5), gotQuery.Cursor.ProjectionRevision)
	require.Equal(t, int64(8), gotQuery.Cursor.EventSequence)

	var payload api.PublicRecoverySnapshot
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, tournamentID, payload.Tournament.TournamentId)
	require.Equal(t, int64(7), payload.NextCursor.ProjectionRevision)
	require.Equal(t, int64(12), payload.NextCursor.EventSequence)
	require.Len(t, payload.Scoreboard.Entries, 1)
	require.Equal(t, int32(1), payload.Scoreboard.Entries[0].Wins)
	require.Equal(t, int32(1), payload.Scoreboard.Entries[0].ByeCount)
	require.Equal(t, api.Pending, payload.Scoreboard.Entries[0].QualificationStatus)
	require.Len(t, payload.SwissRounds, 2)
	require.Equal(t, int32(1), payload.SwissRounds[0].RoundNumber)
	require.Equal(t, api.WaveStateActive, payload.SwissRounds[0].State)
	require.NotNil(t, payload.SwissRounds[0].Bye)
	require.Equal(t, "alice", payload.SwissRounds[0].Bye.DisplayName)
	require.Equal(t, int32(1), payload.SwissRounds[0].Bye.PointsAwarded)
	require.Nil(t, payload.SwissRounds[1].Bye)
	require.Len(t, payload.Bracket.Matches, 3)
	require.Equal(t, api.Bo1, payload.Bracket.Matches[0].Format)
	require.Equal(t, "alice", *payload.Bracket.Matches[0].FirstDisplayName)
	require.Equal(t, "alice", *payload.Bracket.Matches[0].WinnerDisplayName)
	require.Equal(t, api.Bo1, payload.Bracket.Matches[1].Format)
	require.Equal(t, api.Bo3, payload.Bracket.Matches[2].Format)
	require.Nil(t, payload.Bracket.Matches[2].FirstDisplayName)
	require.Nil(t, payload.Bracket.Matches[2].WinnerDisplayName)
	require.Len(t, payload.LiveSeries, 1)
	require.Equal(t, api.PublicLiveSeriesStageSwiss, payload.LiveSeries[0].Stage)
	require.NotNil(t, payload.LiveSeries[0].RoundNumber)
	require.Equal(t, int32(1), *payload.LiveSeries[0].RoundNumber)
	require.Nil(t, payload.LiveSeries[0].ScheduledAt)
	require.Equal(t, "bo3", string(payload.LiveSeries[0].Format))
	require.Equal(t, "alice", payload.LiveSeries[0].FirstDisplayName)
	require.NotNil(t, payload.LiveSeries[0].CurrentGamePosition)
	require.Equal(t, int32(2), *payload.LiveSeries[0].CurrentGamePosition)
	require.Len(t, payload.OfficialResults, 1)
	require.Equal(t, "completed", string(payload.OfficialResults[0].State))
	require.NotNil(t, payload.OfficialResults[0].WinnerDisplayName)
	require.Equal(t, "alice", *payload.OfficialResults[0].WinnerDisplayName)
	require.Equal(t, int32(2), payload.OfficialResults[0].Score.FirstWins)

	for _, privateField := range []string{
		"\"participant_id\"", "\"player_id\"", "\"assignment_id\"", "\"attempt_id\"",
		"\"task_id\"", "\"snapshot_id\"", "\"audit_event_id\"", "\"category_pool\"",
	} {
		require.NotContains(t, recorder.Body.String(), privateField)
	}
}

func TestGetPublicSnapshotWithoutCursorRequestsFreshSnapshot(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("20000000-0000-4000-8000-000000000002")
	reader := inboundmocks.NewMockTournamentSnapshotUseCase(t)
	reader.EXPECT().PublicSnapshot(mock.Anything, mock.MatchedBy(func(query inbound.PublicSnapshotQuery) bool {
		return query.TournamentID == tournamentID && query.Cursor == nil
	})).Return(publicSnapshotHTTPView(tournamentID), nil).Once()

	server := New(Dependencies{TournamentSnapshots: reader})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tournaments/"+tournamentID.String()+"/snapshot", nil)
	server.GetPublicSnapshot(recorder, request, tournamentID, api.GetPublicSnapshotParams{})

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload api.PublicRecoverySnapshot
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Len(t, payload.LiveSeries, 1)
	require.Len(t, payload.OfficialResults, 1)
}

func TestPublicBracketResponseRejectsIncompletePlayoffTopology(t *testing.T) {
	t.Parallel()

	view := publicSnapshotHTTPView(uuid.MustParse("20000000-0000-4000-8000-000000000020"))
	view.Bracket = view.Bracket[:2]

	_, err := publicBracketResponse(view)

	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestGetPublicSnapshotMapsFutureCursorConflictToHTTP409(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("20000000-0000-4000-8000-000000000003")
	reader := inboundmocks.NewMockTournamentSnapshotUseCase(t)
	reader.EXPECT().PublicSnapshot(mock.Anything, mock.Anything).
		Return(inbound.PublicSnapshotView{}, &inbound.PublicSnapshotCursorConflictError{
			TournamentID:                tournamentID,
			RequestedProjectionRevision: 8,
			RequestedEventSequence:      13,
			CurrentProjectionRevision:   7,
			CurrentEventSequence:        12,
		}).
		Once()

	server := New(Dependencies{TournamentSnapshots: reader})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tournaments/"+tournamentID.String()+"/snapshot", nil)
	server.GetPublicSnapshot(recorder, request, tournamentID, api.GetPublicSnapshotParams{
		Cursor: &api.PublicRecoveryCursor{ProjectionRevision: 8, EventSequence: 13},
	})

	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Equal(t, "application/problem+json", recorder.Header().Get("Content-Type"))
	var payload api.PublicRecoveryCursorConflictProblem
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, int32(http.StatusConflict), payload.Status)
	require.Equal(t, int64(8), payload.RequestedCursor.ProjectionRevision)
	require.Equal(t, int64(13), payload.RequestedCursor.EventSequence)
	require.Equal(t, int64(7), payload.CurrentCursor.ProjectionRevision)
	require.Equal(t, int64(12), payload.CurrentCursor.EventSequence)
	require.NotContains(t, recorder.Body.String(), "\"participant_id\"")
}

func publicSnapshotHTTPView(tournamentID uuid.UUID) inbound.PublicSnapshotView {
	recordedAt := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	seriesID := uuid.MustParse("20000000-0000-4000-8000-000000000010")
	return inbound.PublicSnapshotView{
		Cursor: inbound.SnapshotCursor{ProjectionRevision: 7, EventSequence: 12, ObservedAt: recordedAt},
		Tournament: inbound.PublicTournamentView{
			TournamentID: tournamentID,
			Preset:       "tournament_v1",
			State:        "swiss",
			RosterSize:   2,
		},
		Scoreboard: []inbound.PublicScoreboardEntryView{{
			Rank: 1, DisplayName: "alice", Points: 3, Wins: 1, Losses: 0, ByeCount: 1,
			Buchholz: 2, EffectiveTimeMS: 4100, QualificationStatus: "pending",
		}},
		SwissRounds: []inbound.PublicSwissRoundView{
			{RoundNumber: 1, State: "active", Bye: &inbound.PublicSwissByeView{DisplayName: "alice", PointsAwarded: 1}},
			{RoundNumber: 2, State: "planned"},
		},
		Bracket: []inbound.PublicBracketMatchView{
			{
				Stage: "semifinal", Position: 1, Format: "bo1",
				FirstDisplayName: httpStringPointer("alice"), SecondDisplayName: httpStringPointer("bob"),
				FirstWins: 1, SecondWins: 0, State: "completed", WinnerDisplayName: httpStringPointer("alice"),
			},
			{
				Stage: "semifinal", Position: 2, Format: "bo1",
				FirstDisplayName: httpStringPointer("carol"), SecondDisplayName: httpStringPointer("dave"),
				FirstWins: 0, SecondWins: 0, State: "planned",
			},
			{Stage: "final", Position: 1, Format: "bo3", State: "planned"},
		},
		LiveSeries: []inbound.PublicSeriesView{{
			SeriesID:            seriesID,
			Stage:               "swiss",
			RoundNumber:         func() *int { value := 1; return &value }(),
			Format:              "bo3",
			State:               "active",
			FirstDisplayName:    "alice",
			SecondDisplayName:   "bob",
			FirstWins:           1,
			SecondWins:          0,
			CurrentGamePosition: 2,
		}},
		OfficialResults: []inbound.PublicOfficialResultView{{
			RevisionID:        uuid.MustParse("20000000-0000-4000-8000-000000000011"),
			SeriesID:          seriesID,
			State:             "completed",
			WinnerDisplayName: "alice",
			FirstWins:         2,
			SecondWins:        0,
			RecordedAt:        recordedAt,
		}},
	}
}

func httpStringPointer(value string) *string {
	return &value
}
