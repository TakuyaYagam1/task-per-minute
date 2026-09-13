//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
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
		})
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
