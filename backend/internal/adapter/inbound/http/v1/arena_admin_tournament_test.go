package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

func TestArenaAdminTournamentHandlers(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("37d75cce-8f91-4ff1-bf13-b40fb69a7331")
	rosterID := uuid.MustParse("0cfbad45-738c-490b-bd4a-0a8c74c5c8bf")
	commandID := uuid.MustParse("7524f043-40d5-40a5-a549-344c4640401f")
	finishedAt := time.Date(2026, 9, 2, 10, 30, 0, 0, time.UTC)

	t.Run("requires an authenticated operator", func(t *testing.T) {
		service := &arenaAdminServiceStub{}
		controller := newArenaAdminController(service)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/arena/operator/tournaments", nil)

		controller.ListArenaOperatorTournaments(recorder, request, api.ListArenaOperatorTournamentsParams{})

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
		require.Zero(t, service.listCalls)
	})

	t.Run("lists tournaments through one scoped query", func(t *testing.T) {
		state := api.ArenaTournamentStateSwiss
		cursor := "next-page"
		pageSize := int32(10)
		service := &arenaAdminServiceStub{listResult: api.ArenaOperatorTournamentList{
			Items: []api.ArenaTournament{{
				Id: tournamentID, RosterId: rosterID, Preset: api.ArenaV1,
				State: state, Revision: int64Pointer(7), RosterSize: 8,
			}},
		}}
		controller := newArenaAdminController(service)
		recorder := authenticatedArenaRequest(t, http.MethodGet, "/api/v1/arena/operator/tournaments", "", func(w http.ResponseWriter, r *http.Request) {
			controller.ListArenaOperatorTournaments(w, r, api.ListArenaOperatorTournamentsParams{
				State: &state, Cursor: &cursor, PageSize: &pageSize,
			})
		})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.listCalls)
		require.Equal(t, ArenaOperatorIdentity{Subject: "admin", SessionID: "access-session"}, service.listCommand.Operator)
		require.Equal(t, domain.ArenaTournamentState(state), service.listCommand.State)
		require.Equal(t, cursor, service.listCommand.Cursor)
		require.Equal(t, pageSize, service.listCommand.PageSize)

		var response api.ArenaOperatorTournamentList
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Len(t, response.Items, 1)
		require.Equal(t, int64(7), *response.Items[0].Revision)
	})

	t.Run("creates a tournament with operator and idempotency scope", func(t *testing.T) {
		service := &arenaAdminServiceStub{createResult: api.ArenaTournament{
			Id: tournamentID, RosterId: rosterID, Preset: api.ArenaV1,
			State: api.ArenaTournamentStateDraft, Revision: int64Pointer(1), RosterSize: 8,
		}}
		controller := newArenaAdminController(service)
		recorder := authenticatedArenaRequest(t, http.MethodPost, "/api/v1/arena/operator/tournaments",
			`{"expected_projection_revision":4,"preset":"arena_v1","roster_size":8}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.CreateArenaTournament(w, r, api.CreateArenaTournamentParams{IdempotencyKey: commandID})
			})

		require.Equal(t, http.StatusCreated, recorder.Code)
		require.Equal(t, 1, service.createCalls)
		require.Equal(t, commandID, service.createCommand.CommandID)
		require.Equal(t, int64(4), service.createCommand.ExpectedRevision)
		require.Equal(t, domain.ArenaPresetV1, service.createCommand.Preset)
		require.Equal(t, 8, service.createCommand.RosterSize)
		require.Equal(t, "admin", service.createCommand.Operator.Subject)
	})

	t.Run("requires a cancellation reason before dispatch", func(t *testing.T) {
		service := &arenaAdminServiceStub{}
		controller := newArenaAdminController(service)
		recorder := authenticatedArenaRequest(t, http.MethodPost, "/api/v1/arena/operator/tournaments/"+tournamentID.String()+"/actions",
			`{"expected_projection_revision":9,"action":"cancel","confirmed":true}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.ApplyArenaTournamentAction(w, r, tournamentID, api.ApplyArenaTournamentActionParams{IdempotencyKey: commandID})
			})

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Zero(t, service.actionCalls)
	})

	t.Run("returns the preserved terminal state from cancellation", func(t *testing.T) {
		service := &arenaAdminServiceStub{actionResult: api.ArenaTournament{
			Id: tournamentID, RosterId: rosterID, Preset: api.ArenaV1,
			State: api.ArenaTournamentStateCancelled, Revision: int64Pointer(10), RosterSize: 8,
			FinishedAt: &finishedAt,
		}}
		controller := newArenaAdminController(service)
		recorder := authenticatedArenaRequest(t, http.MethodPost, "/api/v1/arena/operator/tournaments/"+tournamentID.String()+"/actions",
			`{"expected_projection_revision":9,"action":"cancel","confirmed":true,"reason":"operator decision"}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.ApplyArenaTournamentAction(w, r, tournamentID, api.ApplyArenaTournamentActionParams{IdempotencyKey: commandID})
			})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.actionCalls)
		require.Equal(t, tournamentID, service.actionCommand.TournamentID)
		require.Equal(t, commandID, service.actionCommand.CommandID)
		require.Equal(t, int64(9), service.actionCommand.ExpectedRevision)
		require.Equal(t, ArenaTournamentActionCancel, service.actionCommand.Action)
		require.Equal(t, "operator decision", service.actionCommand.Reason)
		require.True(t, service.actionCommand.Confirmed)

		var response api.ArenaTournament
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Equal(t, api.ArenaTournamentStateCancelled, response.State)
		require.Equal(t, int64(10), *response.Revision)
		require.Equal(t, finishedAt, *response.FinishedAt)
	})

	t.Run("dispatches start pause and resume as one command each", func(t *testing.T) {
		tests := []struct {
			name   string
			action string
			want   ArenaTournamentAction
		}{
			{name: "start", action: "start_swiss", want: ArenaTournamentActionStartSwiss},
			{name: "pause", action: "pause", want: ArenaTournamentActionPause},
			{name: "resume", action: "resume", want: ArenaTournamentActionResume},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				service := &arenaAdminServiceStub{actionResult: api.ArenaTournament{
					Id: tournamentID, RosterId: rosterID, Preset: api.ArenaV1,
					State: api.ArenaTournamentStateSwiss, Revision: int64Pointer(6), RosterSize: 8,
				}}
				controller := newArenaAdminController(service)
				body := `{"expected_projection_revision":5,"action":"` + test.action + `","confirmed":true,"reason":"scheduled operation"}`
				recorder := authenticatedArenaRequest(t, http.MethodPost, "/actions", body, func(w http.ResponseWriter, r *http.Request) {
					controller.ApplyArenaTournamentAction(w, r, tournamentID, api.ApplyArenaTournamentActionParams{IdempotencyKey: commandID})
				})

				require.Equal(t, http.StatusOK, recorder.Code)
				require.Equal(t, 1, service.actionCalls)
				require.Equal(t, test.want, service.actionCommand.Action)
			})
		}
	})

	t.Run("maps revision conflicts without losing the current state", func(t *testing.T) {
		currentState := api.ArenaTournamentStateCompleted
		service := &arenaAdminServiceStub{actionErr: &ArenaRevisionConflictError{
			ExpectedRevision: 5,
			CurrentRevision:  8,
			CurrentState:     string(currentState),
		}}
		controller := newArenaAdminController(service)
		recorder := authenticatedArenaRequest(t, http.MethodPost, "/actions",
			`{"expected_projection_revision":5,"action":"start_swiss","confirmed":true}`,
			func(w http.ResponseWriter, r *http.Request) {
				controller.ApplyArenaTournamentAction(w, r, tournamentID, api.ApplyArenaTournamentActionParams{IdempotencyKey: commandID})
			})

		require.Equal(t, http.StatusConflict, recorder.Code)
		var response api.ArenaRevisionConflict
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Equal(t, int64(5), response.ExpectedRevision)
		require.Equal(t, int64(8), response.CurrentRevision)
		require.NotNil(t, response.CurrentState)
		require.Equal(t, currentState, *response.CurrentState)
	})
}

type arenaAdminServiceStub struct {
	listCalls   int
	listCommand ArenaListTournamentsCommand
	listResult  api.ArenaOperatorTournamentList
	listErr     error

	createCalls   int
	createCommand ArenaCreateTournamentCommand
	createResult  api.ArenaTournament
	createErr     error

	actionCalls   int
	actionCommand ArenaTournamentActionCommand
	actionResult  api.ArenaTournament
	actionErr     error

	getRosterCalls   int
	getRosterCommand ArenaGetRosterCommand
	getRosterResult  api.ArenaRoster
	getRosterErr     error

	replaceRosterCalls   int
	replaceRosterCommand ArenaReplaceRosterCommand
	replaceRosterResult  api.ArenaRoster
	replaceRosterErr     error

	preflightCalls   int
	preflightCommand ArenaRosterPreflightCommand
	preflightResult  api.ArenaPreflightReport
	preflightErr     error

	lockRosterCalls   int
	lockRosterCommand ArenaLockRosterCommand
	lockRosterResult  api.ArenaRoster
	lockRosterErr     error

	unlockRosterCalls   int
	unlockRosterCommand ArenaUnlockRosterCommand
	unlockRosterResult  api.ArenaRoster
	unlockRosterErr     error

	pairingCalls   int
	pairingCommand ArenaConfigurePairingsCommand
	pairingResult  api.ArenaSwissRound
	pairingErr     error

	standingsCalls   int
	standingsCommand ArenaTournamentReadCommand
	standingsResult  api.ArenaPublicScoreboardResponse
	standingsErr     error

	bracketCalls   int
	bracketCommand ArenaTournamentReadCommand
	bracketResult  api.ArenaPublicBracketResponse
	bracketErr     error
}

func (s *arenaAdminServiceStub) ListTournaments(_ context.Context, command ArenaListTournamentsCommand) (api.ArenaOperatorTournamentList, error) {
	s.listCalls++
	s.listCommand = command
	return s.listResult, s.listErr
}

func (s *arenaAdminServiceStub) CreateTournament(_ context.Context, command ArenaCreateTournamentCommand) (api.ArenaTournament, error) {
	s.createCalls++
	s.createCommand = command
	return s.createResult, s.createErr
}

func (s *arenaAdminServiceStub) ApplyTournamentAction(_ context.Context, command ArenaTournamentActionCommand) (api.ArenaTournament, error) {
	s.actionCalls++
	s.actionCommand = command
	return s.actionResult, s.actionErr
}

func (s *arenaAdminServiceStub) GetRoster(_ context.Context, command ArenaGetRosterCommand) (api.ArenaRoster, error) {
	s.getRosterCalls++
	s.getRosterCommand = command
	return s.getRosterResult, s.getRosterErr
}

func (s *arenaAdminServiceStub) ReplaceRoster(_ context.Context, command ArenaReplaceRosterCommand) (api.ArenaRoster, error) {
	s.replaceRosterCalls++
	s.replaceRosterCommand = command
	return s.replaceRosterResult, s.replaceRosterErr
}

func (s *arenaAdminServiceStub) RunRosterPreflight(_ context.Context, command ArenaRosterPreflightCommand) (api.ArenaPreflightReport, error) {
	s.preflightCalls++
	s.preflightCommand = command
	return s.preflightResult, s.preflightErr
}

func (s *arenaAdminServiceStub) LockRoster(_ context.Context, command ArenaLockRosterCommand) (api.ArenaRoster, error) {
	s.lockRosterCalls++
	s.lockRosterCommand = command
	return s.lockRosterResult, s.lockRosterErr
}

func (s *arenaAdminServiceStub) UnlockRoster(_ context.Context, command ArenaUnlockRosterCommand) (api.ArenaRoster, error) {
	s.unlockRosterCalls++
	s.unlockRosterCommand = command
	return s.unlockRosterResult, s.unlockRosterErr
}

func (s *arenaAdminServiceStub) ConfigurePairings(_ context.Context, command ArenaConfigurePairingsCommand) (api.ArenaSwissRound, error) {
	s.pairingCalls++
	s.pairingCommand = command
	return s.pairingResult, s.pairingErr
}

func (s *arenaAdminServiceStub) GetStandings(_ context.Context, command ArenaTournamentReadCommand) (api.ArenaPublicScoreboardResponse, error) {
	s.standingsCalls++
	s.standingsCommand = command
	return s.standingsResult, s.standingsErr
}

func (s *arenaAdminServiceStub) GetBracket(_ context.Context, command ArenaTournamentReadCommand) (api.ArenaPublicBracketResponse, error) {
	s.bracketCalls++
	s.bracketCommand = command
	return s.bracketResult, s.bracketErr
}

type arenaAdminVerifier struct{}

func (arenaAdminVerifier) VerifyAccess(context.Context, string) (*adminusecase.Claims, error) {
	return &adminusecase.Claims{
		JTI: "access-session", Subject: "admin", Kind: adminusecase.TokenKindAccess,
		IssuedAt:  time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC),
	}, nil
}

func authenticatedArenaRequest(
	t *testing.T,
	method string,
	path string,
	body string,
	handler http.HandlerFunc,
) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer access-token")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	middleware.AdminJWT(arenaAdminVerifier{})(handler).ServeHTTP(recorder, request)
	return recorder
}

func int64Pointer(value int64) *int64 { return &value }
