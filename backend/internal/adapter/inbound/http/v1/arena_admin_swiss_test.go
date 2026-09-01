package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

func TestArenaSwissHandlers(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("a8ecbcf9-9e16-4239-941a-8564b3375696")
	roundID := uuid.MustParse("e65917cd-1907-4db9-9aee-d89b36962445")
	commandID := uuid.MustParse("6f7b95b1-7b0a-4525-80fd-cc06e11d52fb")
	participantIDs := []uuid.UUID{
		uuid.MustParse("96515079-cd58-411d-8e36-b1bafcc48934"),
		uuid.MustParse("f2286cf5-33bb-45f5-9689-d72e9782a5b1"),
		uuid.MustParse("2f5ce958-6a61-4898-b6a7-d40b6c83b961"),
		uuid.MustParse("f9dfced4-7c89-43b6-8a0a-af2dcd2a8ee2"),
	}

	t.Run("configures automatic pairings as one command", func(t *testing.T) {
		service := &arenaAdminServiceStub{pairingResult: swissRoundFixture(tournamentID, roundID, participantIDs, 20)}
		controller := newArenaAdminController(service)
		recorder := authenticatedArenaRequest(t, http.MethodPost, "/pairings",
			`{"expected_projection_revision":19,"round_number":2,"pairing_mode":"automatic","category_mode":"random","categories":["web"],"manual_bye_participant_id":null}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.ConfigureArenaTournamentPairings(w, r, tournamentID, api.ConfigureArenaTournamentPairingsParams{IdempotencyKey: commandID})
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.pairingCalls)
		command := service.pairingCommand
		require.Equal(t, tournamentID, command.TournamentID)
		require.Equal(t, commandID, command.CommandID)
		require.Equal(t, int64(19), command.ExpectedRevision)
		require.Equal(t, int32(2), command.RoundNumber)
		require.Equal(t, ArenaPairingModeAutomatic, command.PairingMode)
		require.Nil(t, command.ManualPairings)
		require.Nil(t, command.ManualByeParticipantID)
		require.Nil(t, command.RepeatOverride)
	})

	t.Run("forwards manual pairs and audited repeat override", func(t *testing.T) {
		service := &arenaAdminServiceStub{pairingResult: swissRoundFixture(tournamentID, roundID, participantIDs, 21)}
		controller := newArenaAdminController(service)
		body := `{"expected_projection_revision":20,"round_number":3,"pairing_mode":"manual","category_mode":"admin","categories":["web","crypto"],` +
			`"manual_pairings":[` +
			`{"first_participant_id":"` + participantIDs[0].String() + `","second_participant_id":"` + participantIDs[1].String() + `"},` +
			`{"first_participant_id":"` + participantIDs[2].String() + `","second_participant_id":"` + participantIDs[3].String() + `"}],` +
			`"manual_bye_participant_id":null,"repeat_override":{"confirmed":true,"reason":"no non-repeating matching remains"}}`
		recorder := authenticatedArenaRequest(t, http.MethodPost, "/pairings", body, func(w http.ResponseWriter, r *http.Request) {
			controller.ConfigureArenaTournamentPairings(w, r, tournamentID, api.ConfigureArenaTournamentPairingsParams{IdempotencyKey: commandID})
		})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.pairingCalls)
		command := service.pairingCommand
		require.Equal(t, ArenaPairingModeManual, command.PairingMode)
		require.Len(t, command.ManualPairings, 2)
		require.Equal(t, participantIDs[0], command.ManualPairings[0].FirstParticipantID)
		require.Equal(t, participantIDs[3], command.ManualPairings[1].SecondParticipantID)
		require.NotNil(t, command.RepeatOverride)
		require.True(t, command.RepeatOverride.Confirmed)
		require.Equal(t, "no non-repeating matching remains", command.RepeatOverride.Reason)
		require.Equal(t, "admin", command.Operator.Subject)
	})

	t.Run("returns all round participants and the current revision", func(t *testing.T) {
		service := &arenaAdminServiceStub{pairingResult: swissRoundFixture(tournamentID, roundID, participantIDs, 22)}
		controller := newArenaAdminController(service)
		recorder := authenticatedArenaRequest(t, http.MethodPost, "/pairings",
			`{"expected_projection_revision":21,"round_number":4,"pairing_mode":"automatic","category_mode":"random","categories":["web"],"manual_bye_participant_id":null}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.ConfigureArenaTournamentPairings(w, r, tournamentID, api.ConfigureArenaTournamentPairingsParams{IdempotencyKey: commandID})
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		var response api.ArenaSwissRound
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Equal(t, int64(22), *response.Revision)
		require.True(t, *response.Locked)
		require.ElementsMatch(t, participantIDs, response.RosterParticipantIds)
		require.Len(t, response.Standings, 4)
		require.ElementsMatch(t, participantIDs, standingParticipantIDs(response.Standings))
	})

	t.Run("returns a stable reason for invalid manual pairs", func(t *testing.T) {
		service := &arenaAdminServiceStub{pairingErr: &ArenaPairingValidationError{Reason: ArenaPairingReasonSelfPair}}
		controller := newArenaAdminController(service)
		body := `{"expected_projection_revision":20,"round_number":3,"pairing_mode":"manual","category_mode":"admin","categories":["web"],` +
			`"manual_pairings":[{"first_participant_id":"` + participantIDs[0].String() + `","second_participant_id":"` + participantIDs[0].String() + `"}],` +
			`"manual_bye_participant_id":null}`
		recorder := authenticatedArenaRequest(t, http.MethodPost, "/pairings", body, func(w http.ResponseWriter, r *http.Request) {
			controller.ConfigureArenaTournamentPairings(w, r, tournamentID, api.ConfigureArenaTournamentPairingsParams{IdempotencyKey: commandID})
		})

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		var response api.ProblemDetails
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.NotNil(t, response.Detail)
		require.Equal(t, string(ArenaPairingReasonSelfPair), *response.Detail)
	})

	t.Run("reads standings with every participant and current revision", func(t *testing.T) {
		entries := make([]api.ArenaPublicScoreboardEntry, len(participantIDs))
		for index := range entries {
			entries[index] = api.ArenaPublicScoreboardEntry{
				Rank: int32(index + 1), DisplayName: "participant-" + participantIDs[index].String(),
				Points: int32(3 - index), Buchholz: int32(index), EffectiveTimeMs: int64(index + 1),
			}
		}
		service := &arenaAdminServiceStub{standingsResult: api.ArenaPublicScoreboardResponse{
			TournamentId: tournamentID, ProjectionRevision: 25, Entries: entries,
		}}
		controller := newArenaAdminController(service)
		recorder := httptest.NewRecorder()
		controller.GetArenaPublicScoreboard(recorder, httptest.NewRequest(http.MethodGet, "/scoreboard", nil), tournamentID)

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.standingsCalls)
		require.Equal(t, tournamentID, service.standingsCommand.TournamentID)
		var response api.ArenaPublicScoreboardResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Equal(t, int64(25), response.ProjectionRevision)
		require.Len(t, response.Entries, 4)
	})

	t.Run("reads the bracket at the current revision", func(t *testing.T) {
		service := &arenaAdminServiceStub{bracketResult: api.ArenaPublicBracketResponse{
			TournamentId:       tournamentID,
			ProjectionRevision: 26,
			Matches: []api.ArenaPublicBracketMatch{{
				Position: 1, Stage: api.ArenaPublicBracketMatchStageSemifinal,
				FirstDisplayName: "first", SecondDisplayName: "second",
				State: api.ArenaSeriesStatePlanned,
			}},
		}}
		controller := newArenaAdminController(service)
		recorder := httptest.NewRecorder()
		controller.GetArenaPublicBracket(recorder, httptest.NewRequest(http.MethodGet, "/bracket", nil), tournamentID)

		require.Equal(t, http.StatusOK, recorder.Code)
		var response api.ArenaPublicBracketResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Equal(t, int64(26), response.ProjectionRevision)
		require.Len(t, response.Matches, 1)
	})
}

func swissRoundFixture(
	tournamentID uuid.UUID,
	roundID uuid.UUID,
	participantIDs []uuid.UUID,
	revision int64,
) api.ArenaSwissRound {
	standings := make([]api.ArenaSwissStanding, len(participantIDs))
	for index := range participantIDs {
		standings[index] = api.ArenaSwissStanding{
			ParticipantId: participantIDs[index], Position: int32(index + 1),
			Points: int32(3 - index), PointsLabel: api.Provisional,
			BuchholzStatus: api.ArenaSwissBuchholzStatusProvisional, StableSeed: int32(index + 1),
		}
	}
	locked := true
	return api.ArenaSwissRound{
		Id: roundID, TournamentId: tournamentID, RoundNumber: 2, Revision: &revision,
		RosterParticipantIds: participantIDs,
		Pairings: []api.ArenaSwissPairing{
			{
				Id: uuid.MustParse("fd78fc44-40bf-435a-92d0-0ef7253182fb"), RoundId: roundID,
				FirstParticipantId: participantIDs[0], SecondParticipantId: participantIDs[1],
				EvidenceId: uuid.MustParse("d0694799-b043-45fc-886f-bde51fe1ef78"),
			},
			{
				Id: uuid.MustParse("5224b050-0787-4732-884b-b7998c163e8e"), RoundId: roundID,
				FirstParticipantId: participantIDs[2], SecondParticipantId: participantIDs[3],
				EvidenceId: uuid.MustParse("26e4ea21-4caf-43cb-a27b-45d9984222fd"),
			},
		},
		Standings: standings,
		Locked:    &locked,
	}
}

func standingParticipantIDs(standings []api.ArenaSwissStanding) []uuid.UUID {
	ids := make([]uuid.UUID, len(standings))
	for index := range standings {
		ids[index] = standings[index].ParticipantId
	}
	return ids
}
