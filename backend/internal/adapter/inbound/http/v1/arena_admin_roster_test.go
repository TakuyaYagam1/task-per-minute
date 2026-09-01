package v1

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

func TestArenaAdminRosterHandlers(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("c65aca26-bd1d-4c54-a349-75a002699e8d")
	rosterID := uuid.MustParse("79e75729-99c2-4985-be84-998683424c98")
	commandID := uuid.MustParse("9faee0c9-3499-4136-8dbf-e971794fb1d6")
	preflightID := uuid.MustParse("76da66fe-46d6-442a-8889-b2ab34338007")
	participantIDs := []uuid.UUID{
		uuid.MustParse("0b5b3551-b099-4e14-b182-b50e446af23a"),
		uuid.MustParse("ee24f393-2a78-4638-8dad-f3b1532ebdc2"),
		uuid.MustParse("5d358c1d-ef9a-4cf9-b883-a5543a7a5883"),
		uuid.MustParse("fca6b913-48e2-4d6f-9570-0ed171941506"),
	}
	playerIDs := []uuid.UUID{
		uuid.MustParse("c29850e0-0f96-41cf-9e02-f99afdfcb724"),
		uuid.MustParse("a85e5ad2-e734-49d4-a909-71c1d514590b"),
		uuid.MustParse("6ab02643-9ffc-4628-a9ab-bb4dcd9718a4"),
		uuid.MustParse("861fb89f-1a51-4e2b-b347-cf401b3d75ac"),
	}

	t.Run("reads every roster participant at the current revision", func(t *testing.T) {
		service := &arenaAdminServiceStub{getRosterResult: arenaRosterFixture(tournamentID, rosterID, participantIDs, playerIDs, 12)}
		controller := newArenaAdminController(service)
		recorder := authenticatedArenaRequest(t, http.MethodGet, "/roster", "", func(w http.ResponseWriter, r *http.Request) {
			controller.GetArenaOperatorRoster(w, r, tournamentID)
		})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.getRosterCalls)
		require.Equal(t, tournamentID, service.getRosterCommand.TournamentID)
		require.Equal(t, "admin", service.getRosterCommand.Operator.Subject)

		var response api.ArenaRoster
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Equal(t, int64(12), *response.Revision)
		require.Len(t, response.Participants, 4)
		require.ElementsMatch(t, participantIDs, rosterParticipantIDs(response.Participants))
	})

	t.Run("replaces registration and check-in state with one command", func(t *testing.T) {
		service := &arenaAdminServiceStub{replaceRosterResult: arenaRosterFixture(tournamentID, rosterID, participantIDs, playerIDs, 13)}
		controller := newArenaAdminController(service)
		body := `{"expected_projection_revision":12,"participants":[` +
			`{"player_id":"` + playerIDs[0].String() + `","seed":1,"attendance":"registered"},` +
			`{"player_id":"` + playerIDs[1].String() + `","seed":2,"attendance":"checked_in"},` +
			`{"player_id":"` + playerIDs[2].String() + `","seed":3,"attendance":"checked_in"},` +
			`{"player_id":"` + playerIDs[3].String() + `","seed":4,"attendance":"checked_in"}]}`
		recorder := authenticatedArenaRequest(t, http.MethodPut, "/roster", body, func(w http.ResponseWriter, r *http.Request) {
			controller.ReplaceArenaTournamentRoster(w, r, tournamentID, api.ReplaceArenaTournamentRosterParams{IdempotencyKey: commandID})
		})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.replaceRosterCalls)
		command := service.replaceRosterCommand
		require.Equal(t, commandID, command.CommandID)
		require.Equal(t, int64(12), command.ExpectedRevision)
		require.Len(t, command.Participants, 4)
		require.Equal(t, ArenaAttendanceRegistered, command.Participants[0].Attendance)
		require.Equal(t, ArenaAttendanceCheckedIn, command.Participants[1].Attendance)
	})

	t.Run("returns a durable preflight revision with every source revision", func(t *testing.T) {
		evaluatedAt := time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC)
		passed := true
		algorithm := api.ArenaPreflightReportV1
		revisions := []api.ArenaPreflightSourceRevision{
			{Source: "roster", Value: "12"},
			{Source: "pairing", Value: "7"},
			{Source: "category_pool", Value: "category-rev"},
			{Source: "task_pool", Value: "task-rev"},
			{Source: "reservation", Value: "reservation-rev"},
		}
		service := &arenaAdminServiceStub{preflightResult: api.ArenaPreflightReport{
			Id: &preflightID, TournamentId: &tournamentID, AlgorithmVersion: &algorithm,
			EvaluatedAt: &evaluatedAt, Revisions: &revisions, Passed: &passed,
		}}
		controller := newArenaAdminController(service)
		recorder := authenticatedArenaRequest(t, http.MethodPost, "/roster/preflight",
			`{"expected_projection_revision":12}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.RunArenaRosterPreflight(w, r, tournamentID, api.RunArenaRosterPreflightParams{IdempotencyKey: commandID})
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.preflightCalls)
		require.Equal(t, commandID, service.preflightCommand.CommandID)
		require.Equal(t, int64(12), service.preflightCommand.ExpectedRevision)

		var response api.ArenaPreflightReport
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Equal(t, preflightID, *response.Id)
		require.Len(t, *response.Revisions, 5)
		require.Equal(t, revisions, *response.Revisions)
	})

	t.Run("locks the exact preflight revision and checked-in players", func(t *testing.T) {
		lockedAt := time.Date(2026, 9, 2, 11, 5, 0, 0, time.UTC)
		locked := true
		result := arenaRosterFixture(tournamentID, rosterID, participantIDs, playerIDs, 13)
		result.Locked = &locked
		result.LockedAt = &lockedAt
		service := &arenaAdminServiceStub{lockRosterResult: result}
		controller := newArenaAdminController(service)
		body, err := json.Marshal(api.ArenaLockRosterRequest{
			ExpectedProjectionRevision: 12,
			PreflightRevisionId:        preflightID,
			CheckedInPlayerIds:         playerIDs,
		})
		require.NoError(t, err)
		recorder := authenticatedArenaRequest(t, http.MethodPost, "/roster/lock", string(body), func(w http.ResponseWriter, r *http.Request) {
			controller.LockArenaTournamentRoster(w, r, tournamentID, api.LockArenaTournamentRosterParams{IdempotencyKey: commandID})
		})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.lockRosterCalls)
		require.Equal(t, preflightID, service.lockRosterCommand.PreflightRevisionID)
		require.Equal(t, playerIDs, service.lockRosterCommand.CheckedInPlayerIDs)
	})

	t.Run("unlocks with the authenticated operator and reason", func(t *testing.T) {
		service := &arenaAdminServiceStub{unlockRosterResult: arenaRosterFixture(tournamentID, rosterID, participantIDs, playerIDs, 14)}
		controller := newArenaAdminController(service)
		recorder := authenticatedArenaRequest(t, http.MethodPost, "/roster/unlock",
			`{"expected_projection_revision":13,"confirmed":true,"reason":"replace withdrawn participant"}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.UnlockArenaTournamentRoster(w, r, tournamentID, api.UnlockArenaTournamentRosterParams{IdempotencyKey: commandID})
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.unlockRosterCalls)
		require.Equal(t, int64(13), service.unlockRosterCommand.ExpectedRevision)
		require.Equal(t, "replace withdrawn participant", service.unlockRosterCommand.Reason)
		require.True(t, service.unlockRosterCommand.Confirmed)
		require.Equal(t, "access-session", service.unlockRosterCommand.Operator.SessionID)
	})

	t.Run("maps stale reservation and post-start unlock conflicts", func(t *testing.T) {
		tests := []struct {
			name  string
			error error
		}{
			{
				name:  "stale reservation",
				error: &ArenaRevisionConflictError{ExpectedRevision: 12, CurrentRevision: 15},
			},
			{
				name: "post-start unlock",
				error: &ArenaRevisionConflictError{
					ExpectedRevision: 12,
					CurrentRevision:  15,
					CurrentState:     string(api.ArenaTournamentStateSwiss),
				},
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				service := &arenaAdminServiceStub{unlockRosterErr: test.error}
				controller := newArenaAdminController(service)
				recorder := authenticatedArenaRequest(t, http.MethodPost, "/roster/unlock",
					`{"expected_projection_revision":12,"confirmed":true,"reason":"operator request"}`,
					func(w http.ResponseWriter, r *http.Request) {
						controller.UnlockArenaTournamentRoster(w, r, tournamentID, api.UnlockArenaTournamentRosterParams{IdempotencyKey: commandID})
					})

				require.Equal(t, http.StatusConflict, recorder.Code)
				var response api.ArenaRevisionConflict
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				require.Equal(t, int64(12), response.ExpectedRevision)
				require.Equal(t, int64(15), response.CurrentRevision)
			})
		}
	})
}

func arenaRosterFixture(
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	playerIDs []uuid.UUID,
	revision int64,
) api.ArenaRoster {
	participants := make([]api.ArenaParticipant, len(participantIDs))
	for index := range participantIDs {
		participants[index] = api.ArenaParticipant{
			Id: participantIDs[index], RosterId: rosterID, TournamentId: tournamentID,
			PlayerId: playerIDs[index], Seed: int32(index + 1), Attendance: api.CheckedIn,
		}
	}
	locked := false
	executionStarted := false
	return api.ArenaRoster{
		Id: rosterID, TournamentId: tournamentID, Revision: &revision,
		Participants: participants, Locked: &locked, ExecutionStarted: &executionStarted,
	}
}

func rosterParticipantIDs(participants []api.ArenaParticipant) []uuid.UUID {
	ids := make([]uuid.UUID, len(participants))
	for index := range participants {
		ids[index] = participants[index].Id
	}
	return ids
}
