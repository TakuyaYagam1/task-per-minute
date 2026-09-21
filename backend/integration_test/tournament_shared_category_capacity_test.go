//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	inboundws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
)

func TestSharedCategoryCapacityBoundariesThroughProductionHandlers(t *testing.T) {
	for _, test := range []struct {
		name        string
		rosterSize  int
		normalCount int
	}{
		{name: "four_players", rosterSize: 4, normalCount: 27},
		{name: "eight_players", rosterSize: 8, normalCount: 45},
		{name: "nine_players", rosterSize: 9, normalCount: 57},
		{name: "sixteen_players", rosterSize: 16, normalCount: 105},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			truncateRoundProofTables(ctx, t)
			t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

			prepareCreateToChampionContentForRosterSize(ctx, t, test.normalCount, test.rosterSize)
			fixture := newTournamentFlowRESTFixture(t)
			adminToken := fixture.adminAccessToken(t)
			content := getTournamentContentThroughREST(t, fixture, adminToken)
			players := joinTournamentFlowPlayers(t, fixture, test.rosterSize)
			created := createTournamentWithRosterSizeThroughREST(
				t, fixture, adminToken, content.ContentRevision, test.name, test.rosterSize,
			)
			openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
			roster := replaceTournamentRosterThroughREST(t, fixture, adminToken, created.Id, players)
			report := runTournamentRosterPreflightThroughREST(t, fixture, adminToken, created.Id)
			require.True(t, report.Passed)
			require.True(t, requireSharedCategoryCapacityCheck(t, report).Passed)
			locked := lockTournamentRosterThroughREST(t, fixture, adminToken, created.Id, roster, report)
			require.True(t, locked.Locked)
			require.Len(t, locked.Participants, test.rosterSize)
			if test.rosterSize == 16 {
				assertZeroStatePublicScoreboardThroughProduction(t, fixture, created.Id)
			}
		})
	}
}

func assertZeroStatePublicScoreboardThroughProduction(
	t *testing.T,
	fixture *restFixture,
	tournamentID uuid.UUID,
) {
	t.Helper()
	path := "/api/v1/tournaments/" + tournamentID.String() + "/snapshot"
	req, resp := fixture.doJSON(t, http.MethodGet, path, "", "")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	snapshot := decodeJSON[api.PublicRecoverySnapshot](t, resp)
	require.Len(t, snapshot.Scoreboard.Entries, 16)
	for index, entry := range snapshot.Scoreboard.Entries {
		require.Equal(t, int32(index+1), entry.Rank)
		require.NotEmpty(t, entry.DisplayName)
		require.Zero(t, entry.Points)
		require.Zero(t, entry.Wins)
		require.Zero(t, entry.Losses)
		require.Zero(t, entry.ByeCount)
		require.False(t, entry.ProvisionalTie)
		require.Equal(t, api.Pending, entry.QualificationStatus)
	}
	for _, privateField := range []string{`"participant_id"`, `"seed"`, `"task"`, `"evidence"`} {
		require.NotContains(t, resp.Body.String(), privateField)
	}

	runtime := tournamentFlowRuntimeForFixture(t, fixture)
	require.NotNil(t, runtime.webSocket)
	server := httptest.NewServer(runtime.webSocket)
	t.Cleanup(server.Close)
	connection := dialTournamentFlowWebSocket(
		t, server.URL+"/api/v1/tournaments/"+tournamentID.String()+"/realtime", nil,
	)
	data := readTournamentFlowWebSocket(t, connection)
	require.NoError(t, connection.CloseNow())
	message, err := inboundws.DecodeTournamentPublicMessage(data)
	require.NoError(t, err)
	require.NotNil(t, message.Public, string(data))
	public := message.Public.Envelope.Public
	require.Equal(t, snapshot.NextCursor.ProjectionRevision, message.Public.Envelope.ProjectionRevision)
	require.Len(t, public.Scoreboard, len(snapshot.Scoreboard.Entries))
	for index, entry := range snapshot.Scoreboard.Entries {
		realtime := public.Scoreboard[index]
		require.Equal(t, int(entry.Rank), realtime.Rank)
		require.Equal(t, entry.DisplayName, realtime.DisplayName)
		require.Equal(t, int(entry.Points), realtime.Points)
		require.Equal(t, int(entry.Wins), realtime.Wins)
		require.Equal(t, int(entry.Losses), realtime.Losses)
		require.Equal(t, int(entry.ByeCount), realtime.ByeCount)
		require.Equal(t, int(entry.Buchholz), realtime.Buchholz)
		require.Equal(t, entry.EffectiveTimeMs, realtime.EffectiveTimeMS)
		require.Equal(t, entry.ProvisionalTie, realtime.ProvisionalTie)
		require.Equal(t, string(entry.QualificationStatus), realtime.QualificationStatus)
	}
	for _, privateField := range []string{`"participant_id"`, `"seed"`, `"task"`, `"evidence"`} {
		require.NotContains(t, string(data), privateField)
	}
}

func runSharedCategoryPreflightThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) api.PreflightReport {
	t.Helper()
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
	body, err := json.Marshal(api.PreflightRequest{
		ExpectedProjectionRevision: snapshot.NextCursor.ProjectionRevision,
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/roster/preflight"
	req, resp := doTournamentFlowJSON(
		t, fixture, http.MethodPost, path, string(body), adminSession(adminToken), commandID, "",
	)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
	report := decodeJSON[api.PreflightReport](t, resp)
	require.Equal(t, tournamentID, report.TournamentId)
	return report
}

func requireSharedCategoryCapacityCheck(t *testing.T, report api.PreflightReport) api.PreflightCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.Code == api.TournamentPreflightRuntimeCapacity {
			return check
		}
	}
	require.FailNow(t, "preflight capacity check missing")
	return api.PreflightCheck{}
}

func lockSharedCategoryRosterExpectingRejectionThroughREST(
	t *testing.T,
	fixture *restFixture,
	adminToken string,
	tournamentID uuid.UUID,
	roster api.Roster,
	report api.PreflightReport,
) {
	t.Helper()
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, tournamentID)
	playerIDs := make([]uuid.UUID, len(roster.Participants))
	for index, participant := range roster.Participants {
		playerIDs[index] = participant.PlayerId
	}
	body, err := json.Marshal(api.LockRosterRequest{
		ExpectedProjectionRevision: snapshot.NextCursor.ProjectionRevision,
		PreflightRevisionId:        report.Id,
		CheckedInPlayerIds:         playerIDs,
	})
	require.NoError(t, err)
	path := "/api/v1/admin/tournaments/" + tournamentID.String() + "/roster/lock"
	req, resp := doTournamentFlowJSON(
		t, fixture, http.MethodPost, path, string(body), adminSession(adminToken), uuid.New(), "",
	)
	// The transport-neutral admin boundary normalizes an untyped lock conflict
	// to an internal error; rejection is still durable because the roster stays
	// unlocked and no lock operation is recorded.
	require.Equal(t, http.StatusInternalServerError, resp.Code, resp.Body.String())
	fixture.validateResponse(t, req, resp)
}
