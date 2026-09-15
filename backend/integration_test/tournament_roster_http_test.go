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

func TestTournamentRosterPersistsThroughProductionHTTP(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(context.Background(), t) })

	catalog := prepareCreateToChampionContentForRosterSize(ctx, t, createToChampionNormalTaskCount, 4)
	fixture := newTournamentFlowRESTFixture(t)
	adminToken := fixture.adminAccessToken(t)
	created := createTournamentWithRosterSizeThroughREST(
		t, fixture, adminToken, catalog.revision, "roster-http", 4,
	)

	path := "/api/v1/admin/tournaments/" + created.Id.String() + "/roster"
	request, response := doTournamentFlowJSON(
		t, fixture, http.MethodGet, path, "", adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	emptyRoster := decodeJSON[api.Roster](t, response)
	require.Equal(t, created.Id, emptyRoster.TournamentId)
	require.Equal(t, created.RosterId, emptyRoster.Id)
	require.EqualValues(t, 1, emptyRoster.Revision)
	require.False(t, emptyRoster.Locked)
	require.False(t, emptyRoster.ExecutionStarted)
	require.Nil(t, emptyRoster.LockedAt)
	require.Nil(t, emptyRoster.ExecutionStartedAt)
	require.NotNil(t, emptyRoster.Participants)
	require.Empty(t, emptyRoster.Participants)

	players := joinTournamentFlowPlayers(t, fixture, 4)
	openRegistrationThroughREST(t, fixture, adminToken, created.Id, created.Revision)
	snapshot := tournamentAdminSnapshotThroughREST(t, fixture, adminToken, created.Id)
	require.Positive(t, snapshot.NextCursor.ProjectionRevision)

	inputs := make([]api.RosterParticipantInput, len(players))
	for index, player := range players {
		inputs[index] = api.RosterParticipantInput{
			PlayerId:   player.id,
			Seed:       int32(index + 1),
			Attendance: api.CheckedIn,
		}
	}
	body, err := json.Marshal(api.ReplaceRosterRequest{
		ExpectedProjectionRevision: snapshot.NextCursor.ProjectionRevision,
		Participants:               inputs,
	})
	require.NoError(t, err)
	request, response = doTournamentFlowJSON(
		t, fixture, http.MethodPut, path, string(body), adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	putRoster := decodeJSON[api.Roster](t, response)
	assertTournamentRoster(t, putRoster, created.Id, emptyRoster.Id, players)
	require.EqualValues(t, 2, putRoster.Revision)

	request, response = doTournamentFlowJSON(
		t, fixture, http.MethodGet, path, "", adminSession(adminToken), uuid.New(), "",
	)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	fixture.validateResponse(t, request, response)
	got := decodeJSON[api.Roster](t, response)
	assertTournamentRoster(t, got, created.Id, putRoster.Id, players)
	require.Equal(t, putRoster, got)
}

func assertTournamentRoster(
	t *testing.T,
	roster api.Roster,
	tournamentID, rosterID uuid.UUID,
	players []tournamentFlowPlayer,
) {
	t.Helper()
	require.Equal(t, tournamentID, roster.TournamentId)
	require.Equal(t, rosterID, roster.Id)
	require.False(t, roster.Locked)
	require.False(t, roster.ExecutionStarted)
	require.Len(t, roster.Participants, len(players))
	seenPlayerIDs := make(map[uuid.UUID]struct{}, len(players))
	seenParticipantIDs := make(map[uuid.UUID]struct{}, len(players))
	for index, player := range players {
		participant := roster.Participants[index]
		require.Equal(t, uuid.NewSHA1(roster.Id, player.id[:]), participant.Id)
		require.Equal(t, roster.Id, participant.RosterId)
		require.Equal(t, roster.TournamentId, participant.TournamentId)
		require.Equal(t, player.id, participant.PlayerId)
		require.EqualValues(t, index+1, participant.Seed)
		require.Equal(t, api.CheckedIn, participant.Attendance)
		require.NotContains(t, seenPlayerIDs, participant.PlayerId)
		require.NotContains(t, seenParticipantIDs, participant.Id)
		seenPlayerIDs[participant.PlayerId] = struct{}{}
		seenParticipantIDs[participant.Id] = struct{}{}
	}
}
