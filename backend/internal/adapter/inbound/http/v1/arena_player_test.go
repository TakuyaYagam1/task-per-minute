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

func TestArenaPlayerHandlers(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("5ae8310f-b5fa-444f-8184-76898605270d")
	seriesID := uuid.MustParse("21d2f532-c0fa-4ea0-a89f-c4ee73606a78")
	waveID := uuid.MustParse("8d694de6-b4d4-4694-ae87-f268b5480fea")
	assignmentID := uuid.MustParse("105f818e-5f11-4462-a32c-4ec87c1c5a8f")
	commandID := uuid.MustParse("ed631fcf-9293-4bbd-b0df-76aa7acc31c4")
	playerID := uuid.MustParse("ef9e82b9-ad9f-4c9e-a8fa-4fce54962f46")

	t.Run("reads the authenticated player lobby", func(t *testing.T) {
		service := &arenaParticipantServiceStub{lobbyResult: api.ArenaParticipantLobbyResponse{
			TournamentId: tournamentID, State: api.ArenaTournamentStateSwiss,
			ProjectionRevision: 22, RosterLocked: true,
			Series: []api.ArenaParticipantLobbySeries{{
				SeriesId: seriesID, WaveId: waveID, State: api.ArenaSeriesStateReady,
				Format: api.Bo1, OpponentDisplayName: "opponent",
			}},
		}}
		controller := newArenaParticipantController(service, nil)
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodGet, "/participant/lobby", "",
			func(w http.ResponseWriter, r *http.Request) {
				controller.GetArenaParticipantLobby(w, r, tournamentID)
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, playerID, service.lobbyCommand.Actor.PlayerID)
		require.Equal(t, tournamentID, service.lobbyCommand.TournamentID)
		var lobby api.ArenaParticipantLobbyResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &lobby))
		require.Len(t, lobby.Series, 1)
	})

	t.Run("requires an authenticated player", func(t *testing.T) {
		service := &arenaParticipantServiceStub{}
		controller := newArenaParticipantController(service, nil)
		recorder := httptest.NewRecorder()
		controller.GetArenaParticipantLobby(
			recorder,
			httptest.NewRequest(http.MethodGet, "/participant/lobby", nil),
			tournamentID,
		)

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
		require.Zero(t, service.lobbyCalls)
	})

	t.Run("reads a private assignment through the owner scoped port", func(t *testing.T) {
		service := &arenaParticipantServiceStub{assignmentResult: api.ArenaParticipantAssignmentResponse{
			TournamentId: tournamentID, ProjectionRevision: 23,
			Assignment: arenaAssignmentFixture(assignmentID),
		}}
		controller := newArenaParticipantController(service, nil)
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodGet, "/participant/assignment", "",
			func(w http.ResponseWriter, r *http.Request) {
				controller.GetArenaParticipantAssignment(w, r, tournamentID, assignmentID)
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, playerID, service.assignmentCommand.Actor.PlayerID)
		require.Equal(t, assignmentID, service.assignmentCommand.AssignmentID)
		require.NotContains(t, recorder.Body.String(), "participant_ids")
		require.NotContains(t, recorder.Body.String(), "submitted_flag")
		require.NotContains(t, recorder.Body.String(), "content_digest")
	})

	t.Run("dispatches readiness as one authenticated command", func(t *testing.T) {
		service := &arenaParticipantServiceStub{readyResult: api.ArenaReadinessEvent{
			CommandId: commandID, ParticipantId: uuid.New(), WaveId: waveID,
			WindowId: uuid.New(), Type: api.ArenaReadinessEventTypeReady,
		}}
		controller := newArenaParticipantController(service, nil)
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/participant/ready",
			`{"expected_projection_revision":23,"ready":true}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.SetArenaParticipantReady(
					w, r, tournamentID, waveID,
					api.SetArenaParticipantReadyParams{IdempotencyKey: commandID},
				)
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, playerID, service.readyCommand.Actor.PlayerID)
		require.Equal(t, commandID, service.readyCommand.CommandID)
		require.Equal(t, int64(23), service.readyCommand.ExpectedProjectionRevision)
		require.True(t, service.readyCommand.Ready)
	})

	t.Run("rejects an invalid readiness revision before dispatch", func(t *testing.T) {
		service := &arenaParticipantServiceStub{}
		controller := newArenaParticipantController(service, nil)
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/participant/ready",
			`{"expected_projection_revision":0,"ready":true}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.SetArenaParticipantReady(
					w, r, tournamentID, waveID,
					api.SetArenaParticipantReadyParams{IdempotencyKey: commandID},
				)
			})

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Zero(t, service.readyCalls)
	})

	t.Run("recovers the participant snapshot from the supplied cursor", func(t *testing.T) {
		cursor := api.ArenaParticipantRecoveryCursor{
			ProjectionRevision: 21, ParticipantViewRevision: 8, EventSequence: 34,
		}
		service := &arenaParticipantServiceStub{snapshotResult: api.ArenaParticipantRecoverySnapshot{
			TournamentId: tournamentID, ProjectionRevision: 24,
			Lobby: api.ArenaParticipantLobbyResponse{
				TournamentId: tournamentID, State: api.ArenaTournamentStateSwiss,
				ProjectionRevision: 24, RosterLocked: true, Series: []api.ArenaParticipantLobbySeries{},
			},
			NextCursor: api.ArenaParticipantRecoveryCursor{
				ProjectionRevision: 24, ParticipantViewRevision: 9, EventSequence: 35,
			},
		}}
		controller := newArenaParticipantController(service, nil)
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodGet, "/participant/snapshot", "",
			func(w http.ResponseWriter, r *http.Request) {
				controller.GetArenaParticipantSnapshot(
					w, r, tournamentID, api.GetArenaParticipantSnapshotParams{Cursor: &cursor},
				)
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, playerID, service.snapshotCommand.Actor.PlayerID)
		require.NotNil(t, service.snapshotCommand.Cursor)
		require.Equal(t, cursor, *service.snapshotCommand.Cursor)
	})

	t.Run("dispatches a legal next assignment action", func(t *testing.T) {
		service := &arenaParticipantServiceStub{postSeriesResult: api.ArenaParticipantPostSeriesResponse{
			SeriesId: seriesID, ProjectionRevision: 25,
			AcceptedAction: api.ArenaParticipantPostSeriesResponseAcceptedActionRequestNextAssignment,
		}}
		controller := newArenaParticipantController(service, nil)
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/participant/post-series",
			`{"expected_projection_revision":24,"action":"request_next_assignment"}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.ApplyArenaParticipantPostSeriesAction(
					w, r, tournamentID, seriesID,
					api.ApplyArenaParticipantPostSeriesActionParams{IdempotencyKey: commandID},
				)
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, ArenaParticipantPostSeriesActionRequestNextAssignment, service.postSeriesCommand.Action)
		require.Equal(t, playerID, service.postSeriesCommand.Actor.PlayerID)
	})

	t.Run("rejects an unknown next state before dispatch", func(t *testing.T) {
		service := &arenaParticipantServiceStub{}
		controller := newArenaParticipantController(service, nil)
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/participant/post-series",
			`{"expected_projection_revision":24,"action":"join_casual_queue"}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.ApplyArenaParticipantPostSeriesAction(
					w, r, tournamentID, seriesID,
					api.ApplyArenaParticipantPostSeriesActionParams{IdempotencyKey: commandID},
				)
			})

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Zero(t, service.postSeriesCalls)
	})

	t.Run("builds the participant controller from the optional service", func(t *testing.T) {
		service := &arenaParticipantServiceStub{lobbyResult: api.ArenaParticipantLobbyResponse{
			TournamentId: tournamentID, State: api.ArenaTournamentStateSwiss,
			ProjectionRevision: 26, RosterLocked: true, Series: []api.ArenaParticipantLobbySeries{},
		}}
		server := New(Dependencies{ArenaParticipantService: service})
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodGet, "/participant/lobby", "",
			func(w http.ResponseWriter, r *http.Request) {
				server.GetArenaParticipantLobby(w, r, tournamentID)
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.lobbyCalls)
	})
}

func arenaAssignmentFixture(assignmentID uuid.UUID) api.ArenaParticipantAssignment {
	attemptID := uuid.New()
	snapshotID := uuid.New()
	taskID := uuid.New()
	return api.ArenaParticipantAssignment{
		Id: assignmentID, AttemptId: attemptID, UndisclosedReserveCount: 1,
		Receipt: api.ArenaDeliveryReceipt{
			Id: uuid.New(), AssignmentId: assignmentID, AttemptId: attemptID,
			ParticipantId: uuid.New(), SnapshotId: snapshotID, TaskId: taskID,
		},
		ActiveSnapshot: api.ArenaTaskSnapshot{
			SnapshotId: snapshotID, TaskId: taskID, Version: 1, Kind: api.Normal,
			Title: "web task", Description: "solve the task", Category: api.ArenaCategoryWeb,
			Difficulty: api.ArenaDifficultyEasy, TimeLimit: 300, Hints: []string{},
		},
	}
}
