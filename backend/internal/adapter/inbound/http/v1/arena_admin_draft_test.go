package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestArenaDraftHandlers(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("b8cfbb4a-0b09-46e1-b15c-cc291c575c0a")
	seriesID := uuid.MustParse("e2c3fa6a-f5f9-4d60-a4d8-a436c5844af1")
	commandID := uuid.MustParse("8ce87f31-234b-4ad0-a6dc-f64249b49286")
	playerID := uuid.MustParse("7053b24d-67e0-4b11-a7fb-558227a6484c")
	draftID := uuid.MustParse("34d3096d-5161-4317-82d5-99ae3b2a8e64")
	waveID := uuid.MustParse("a76d2f58-f75f-4c8d-b229-545ea09cb2cd")

	t.Run("dispatches the authenticated player draft action", func(t *testing.T) {
		service := &arenaParticipantServiceStub{draftResult: api.ArenaDraft{
			Id: draftID, SeriesId: seriesID, State: api.ArenaDraftStateActive,
			Format: api.Bo1, FirstParticipantId: uuid.New(), SecondParticipantId: uuid.New(),
			Pool:               []api.ArenaCategory{api.ArenaCategoryWeb, api.ArenaCategoryCrypto},
			SelectedCategories: []api.ArenaCategory{}, Actions: []api.ArenaDraftAction{}, Revision: 5, Turn: 1,
		}}
		controller := newArenaParticipantController(service, nil)
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/draft/actions",
			`{"expected_projection_revision":17,"expected_draft_revision":4,"expected_turn":1,"action":"ban","category":"web"}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.SubmitArenaParticipantDraftAction(
					w, r, tournamentID, seriesID,
					api.SubmitArenaParticipantDraftActionParams{IdempotencyKey: commandID},
				)
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.draftCalls)
		require.Equal(t, playerID, service.draftCommand.Actor.PlayerID)
		require.Equal(t, tournamentID, service.draftCommand.TournamentID)
		require.Equal(t, seriesID, service.draftCommand.SeriesID)
		require.Equal(t, commandID, service.draftCommand.CommandID)
		require.Equal(t, int64(17), service.draftCommand.ExpectedProjectionRevision)
		require.Equal(t, int64(4), service.draftCommand.ExpectedDraftRevision)
		require.Equal(t, int32(1), service.draftCommand.ExpectedTurn)
		require.Equal(t, domain.ArenaDraftActionBan, service.draftCommand.Action)
		require.Equal(t, domain.CategoryWeb, service.draftCommand.Category)

		var result api.ArenaDraft
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
		require.Equal(t, draftID, result.Id)
	})

	t.Run("rejects invalid draft input before the application port", func(t *testing.T) {
		service := &arenaParticipantServiceStub{}
		controller := newArenaParticipantController(service, nil)
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/draft/actions",
			`{"expected_projection_revision":17,"expected_draft_revision":0,"expected_turn":1,"action":"client-choice","category":"web"}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.SubmitArenaParticipantDraftAction(
					w, r, tournamentID, seriesID,
					api.SubmitArenaParticipantDraftActionParams{IdempotencyKey: commandID},
				)
			})

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Zero(t, service.draftCalls)
	})

	t.Run("returns the stable revision conflict without internal detail", func(t *testing.T) {
		service := &arenaParticipantServiceStub{draftErr: &ArenaRevisionConflictError{
			ExpectedRevision: 4,
			CurrentRevision:  5,
		}}
		controller := newArenaParticipantController(service, nil)
		recorder := authenticatedArenaParticipantRequest(t, playerID, http.MethodPost, "/draft/actions",
			`{"expected_projection_revision":17,"expected_draft_revision":4,"expected_turn":1,"action":"ban","category":"web"}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.SubmitArenaParticipantDraftAction(
					w, r, tournamentID, seriesID,
					api.SubmitArenaParticipantDraftActionParams{IdempotencyKey: commandID},
				)
			})

		require.Equal(t, http.StatusConflict, recorder.Code)
		var conflict api.ArenaRevisionConflict
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &conflict))
		require.Equal(t, int64(4), conflict.ExpectedRevision)
		require.Equal(t, int64(5), conflict.CurrentRevision)
		require.NotContains(t, recorder.Body.String(), "turn deadline")
	})

	t.Run("forwards every server controlled category mode", func(t *testing.T) {
		tests := []struct {
			mode string
			want domain.ArenaCategoryMode
		}{
			{mode: "random", want: domain.ArenaCategoryModeRandom},
			{mode: "admin", want: domain.ArenaCategoryModeAdmin},
			{mode: "draft", want: domain.ArenaCategoryModeDraft},
		}
		for _, test := range tests {
			t.Run(test.mode, func(t *testing.T) {
				service := &arenaDraftAdminServiceStub{arenaAdminServiceStub: &arenaAdminServiceStub{}}
				controller := newArenaAdminController(service)
				recorder := authenticatedArenaRequest(t, http.MethodPost, "/pairings",
					`{"expected_projection_revision":12,"round_number":2,"pairing_mode":"automatic","category_mode":"`+test.mode+`","categories":["web"],"manual_bye_participant_id":null}`,
					func(w http.ResponseWriter, r *http.Request) {
						controller.ConfigureArenaTournamentPairings(
							w, r, tournamentID,
							api.ConfigureArenaTournamentPairingsParams{IdempotencyKey: commandID},
						)
					})

				require.Equal(t, http.StatusOK, recorder.Code)
				require.Equal(t, 1, service.pairingCalls)
				require.Equal(t, test.want, service.pairingCommand.CategoryMode)
				require.Equal(t, []domain.Category{domain.CategoryWeb}, service.pairingCommand.Categories)
			})
		}
	})

	t.Run("dispatches operator wave pause and resume commands", func(t *testing.T) {
		tests := []struct {
			name   string
			action string
			want   ArenaWaveControlAction
		}{
			{name: "pause", action: "pause", want: ArenaWaveControlActionPause},
			{name: "resume", action: "resume", want: ArenaWaveControlActionResume},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				revision := int64(31)
				service := &arenaDraftAdminServiceStub{
					arenaAdminServiceStub: &arenaAdminServiceStub{},
					waveResult: api.ArenaWave{
						Id: waveID, TournamentId: tournamentID, State: api.ArenaWaveStateActive,
						Members: []api.ArenaWaveMember{}, Revision: &revision,
					},
				}
				controller := newArenaAdminController(service)
				recorder := authenticatedArenaRequest(t, http.MethodPost, "/waves/actions",
					`{"expected_projection_revision":30,"action":"`+test.action+`","confirmed":true,"reason":"draft recovery"}`,
					func(w http.ResponseWriter, r *http.Request) {
						controller.ControlArenaTournamentWave(
							w, r, tournamentID, waveID,
							api.ControlArenaTournamentWaveParams{IdempotencyKey: commandID},
						)
					})

				require.Equal(t, http.StatusOK, recorder.Code)
				require.Equal(t, 1, service.waveCalls)
				require.Equal(t, test.want, service.waveCommand.Action)
				require.Equal(t, commandID, service.waveCommand.CommandID)
				require.Equal(t, int64(30), service.waveCommand.ExpectedRevision)
				require.Equal(t, "admin", service.waveCommand.Operator.Subject)
				require.Equal(t, "draft recovery", service.waveCommand.Reason)
			})
		}
	})

	t.Run("rejects an unknown wave recovery action before dispatch", func(t *testing.T) {
		service := &arenaDraftAdminServiceStub{arenaAdminServiceStub: &arenaAdminServiceStub{}}
		controller := newArenaAdminController(service)
		recorder := authenticatedArenaRequest(t, http.MethodPost, "/waves/actions",
			`{"expected_projection_revision":30,"action":"recover_draft","confirmed":true}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.ControlArenaTournamentWave(
					w, r, tournamentID, waveID,
					api.ControlArenaTournamentWaveParams{IdempotencyKey: commandID},
				)
			})

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Zero(t, service.waveCalls)
	})
}

type arenaDraftAdminServiceStub struct {
	*arenaAdminServiceStub

	waveCalls   int
	waveCommand ArenaWaveControlCommand
	waveResult  api.ArenaWave
	waveErr     error
}

func (s *arenaDraftAdminServiceStub) ControlWave(
	_ context.Context,
	command ArenaWaveControlCommand,
) (api.ArenaWave, error) {
	s.waveCalls++
	s.waveCommand = command
	return s.waveResult, s.waveErr
}

func (s *arenaAdminServiceStub) ControlWave(
	context.Context,
	ArenaWaveControlCommand,
) (api.ArenaWave, error) {
	return api.ArenaWave{}, nil
}

type arenaParticipantServiceStub struct {
	draftCalls   int
	draftCommand ArenaParticipantDraftActionCommand
	draftResult  api.ArenaDraft
	draftErr     error

	lobbyCalls   int
	lobbyCommand ArenaParticipantLobbyCommand
	lobbyResult  api.ArenaParticipantLobbyResponse
	lobbyErr     error

	assignmentCalls   int
	assignmentCommand ArenaParticipantAssignmentCommand
	assignmentResult  api.ArenaParticipantAssignmentResponse
	assignmentErr     error

	readyCalls   int
	readyCommand ArenaParticipantReadyCommand
	readyResult  api.ArenaReadinessEvent
	readyErr     error

	snapshotCalls   int
	snapshotCommand ArenaParticipantSnapshotCommand
	snapshotResult  api.ArenaParticipantRecoverySnapshot
	snapshotErr     error

	postSeriesCalls   int
	postSeriesCommand ArenaParticipantPostSeriesCommand
	postSeriesResult  api.ArenaParticipantPostSeriesResponse
	postSeriesErr     error

	submissionCalls   int
	submissionCommand ArenaParticipantSubmissionCommand
	submissionResult  api.ArenaParticipantSubmissionResponse
	submissionErr     error

	surrenderCalls   int
	surrenderCommand ArenaParticipantSurrenderCommand
	surrenderResult  api.ArenaOfficialResultRevision
	surrenderErr     error
}

func (s *arenaParticipantServiceStub) SubmitDraftAction(
	_ context.Context,
	command ArenaParticipantDraftActionCommand,
) (api.ArenaDraft, error) {
	s.draftCalls++
	s.draftCommand = command
	return s.draftResult, s.draftErr
}

func (s *arenaParticipantServiceStub) GetLobby(
	_ context.Context,
	command ArenaParticipantLobbyCommand,
) (api.ArenaParticipantLobbyResponse, error) {
	s.lobbyCalls++
	s.lobbyCommand = command
	return s.lobbyResult, s.lobbyErr
}

func (s *arenaParticipantServiceStub) GetAssignment(
	_ context.Context,
	command ArenaParticipantAssignmentCommand,
) (api.ArenaParticipantAssignmentResponse, error) {
	s.assignmentCalls++
	s.assignmentCommand = command
	return s.assignmentResult, s.assignmentErr
}

func (s *arenaParticipantServiceStub) SetReady(
	_ context.Context,
	command ArenaParticipantReadyCommand,
) (api.ArenaReadinessEvent, error) {
	s.readyCalls++
	s.readyCommand = command
	return s.readyResult, s.readyErr
}

func (s *arenaParticipantServiceStub) GetSnapshot(
	_ context.Context,
	command ArenaParticipantSnapshotCommand,
) (api.ArenaParticipantRecoverySnapshot, error) {
	s.snapshotCalls++
	s.snapshotCommand = command
	return s.snapshotResult, s.snapshotErr
}

func (s *arenaParticipantServiceStub) ApplyPostSeriesAction(
	_ context.Context,
	command ArenaParticipantPostSeriesCommand,
) (api.ArenaParticipantPostSeriesResponse, error) {
	s.postSeriesCalls++
	s.postSeriesCommand = command
	return s.postSeriesResult, s.postSeriesErr
}

func (s *arenaParticipantServiceStub) SubmitFlag(
	_ context.Context,
	command ArenaParticipantSubmissionCommand,
) (api.ArenaParticipantSubmissionResponse, error) {
	s.submissionCalls++
	s.submissionCommand = command
	return s.submissionResult, s.submissionErr
}

func (s *arenaParticipantServiceStub) Surrender(
	_ context.Context,
	command ArenaParticipantSurrenderCommand,
) (api.ArenaOfficialResultRevision, error) {
	s.surrenderCalls++
	s.surrenderCommand = command
	return s.surrenderResult, s.surrenderErr
}

func authenticatedArenaParticipantRequest(
	t *testing.T,
	playerID uuid.UUID,
	method string,
	path string,
	body string,
	handler http.HandlerFunc,
) *httptest.ResponseRecorder {
	t.Helper()

	sessionToken := uuid.MustParse("771bc43b-5744-4245-ad3e-9d680792bbd9")
	player := &domain.Player{ID: playerID, SessionToken: &sessionToken}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: sessionToken.String()})
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	middleware.PlayerSession(&playerSessionReaderStub{player: player})(handler).ServeHTTP(recorder, request)
	return recorder
}
