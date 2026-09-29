package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	middlewaremocks "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	admissionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admission"
)

type tournamentAdmissionStub struct {
	join    func(context.Context, inbound.TournamentAdmissionJoinCommand) (inbound.TournamentAdmissionMutation, error)
	checkIn func(context.Context, inbound.TournamentAdmissionCheckInCommand) (inbound.TournamentAdmissionMutation, error)
	status  func(context.Context, inbound.TournamentAdmissionStatusQuery) (inbound.TournamentAdmissionView, error)
	cancel  func(context.Context, inbound.TournamentAdmissionCancelCommand) (inbound.TournamentAdmissionMutation, error)
}

func (s *tournamentAdmissionStub) Join(ctx context.Context, command inbound.TournamentAdmissionJoinCommand) (inbound.TournamentAdmissionMutation, error) {
	if s.join == nil {
		return inbound.TournamentAdmissionMutation{}, nil
	}
	return s.join(ctx, command)
}

func (s *tournamentAdmissionStub) CheckIn(ctx context.Context, command inbound.TournamentAdmissionCheckInCommand) (inbound.TournamentAdmissionMutation, error) {
	if s.checkIn == nil {
		return inbound.TournamentAdmissionMutation{}, nil
	}
	return s.checkIn(ctx, command)
}

func (s *tournamentAdmissionStub) GetStatus(ctx context.Context, query inbound.TournamentAdmissionStatusQuery) (inbound.TournamentAdmissionView, error) {
	if s.status == nil {
		return inbound.TournamentAdmissionView{}, nil
	}
	return s.status(ctx, query)
}

func (s *tournamentAdmissionStub) Cancel(ctx context.Context, command inbound.TournamentAdmissionCancelCommand) (inbound.TournamentAdmissionMutation, error) {
	if s.cancel == nil {
		return inbound.TournamentAdmissionMutation{}, nil
	}
	return s.cancel(ctx, command)
}

func TestGetTournamentAdmissionStatusMapsNotRegisteredView(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	tournamentID := uuid.New()
	stub := &tournamentAdmissionStub{
		status: func(_ context.Context, query inbound.TournamentAdmissionStatusQuery) (inbound.TournamentAdmissionView, error) {
			require.Equal(t, playerID, query.Actor.PlayerID)
			require.Equal(t, tournamentID, query.TournamentID)
			return inbound.TournamentAdmissionView{
				TournamentID:      tournamentID,
				PlayerID:          playerID,
				Status:            inbound.TournamentAdmissionStatusNotRegistered,
				TournamentState:   domain.TournamentStateRegistration,
				RosterRevision:    1,
				PlannedRosterSize: 8,
				RosterSize:        0,
			}, nil
		},
	}
	server := New(Dependencies{TournamentAdmission: stub})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tournaments/"+tournamentID.String()+"/participant/queue", nil)
	serveWithPlayer(t, playerID, recorder, req, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.GetTournamentAdmissionStatus(w, r, tournamentID)
	}))

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload api.TournamentAdmissionView
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, tournamentID, payload.TournamentId)
	require.Equal(t, playerID, payload.PlayerId)
	require.Equal(t, api.TournamentAdmissionStatusNotRegistered, payload.Status)
	require.Nil(t, payload.ParticipantId)
	require.Nil(t, payload.Seed)
	require.Nil(t, payload.Attendance)
	require.Equal(t, int32(0), payload.RosterSize)
}

func TestJoinTournamentAdmissionPreservesConflictReason(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	tournamentID := uuid.New()
	commandID := uuid.New()
	stub := &tournamentAdmissionStub{
		join: func(_ context.Context, command inbound.TournamentAdmissionJoinCommand) (inbound.TournamentAdmissionMutation, error) {
			require.Equal(t, playerID, command.Actor.PlayerID)
			require.Equal(t, tournamentID, command.TournamentID)
			require.Equal(t, commandID, command.CommandID)
			return inbound.TournamentAdmissionMutation{}, admissionusecase.ErrTournamentAdmissionFull
		},
	}
	server := New(Dependencies{TournamentAdmission: stub})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tournaments/"+tournamentID.String()+"/participant/queue", nil)
	serveWithPlayer(t, playerID, recorder, req, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.JoinTournamentAdmission(w, r, tournamentID, api.JoinTournamentAdmissionParams{IdempotencyKey: commandID})
	}))

	require.Equal(t, http.StatusConflict, recorder.Code)
	var payload api.TournamentAdmissionConflictProblem
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, api.TournamentAdmissionConflictReasonFull, payload.Reason)
	require.Equal(t, int32(http.StatusConflict), payload.Status)
	require.Contains(t, *payload.Detail, "full")
}

func TestCancelTournamentAdmissionMapsCurrentMutation(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	tournamentID := uuid.New()
	participantID := uuid.New()
	commandID := uuid.New()
	stub := &tournamentAdmissionStub{
		cancel: func(_ context.Context, command inbound.TournamentAdmissionCancelCommand) (inbound.TournamentAdmissionMutation, error) {
			require.Equal(t, playerID, command.Actor.PlayerID)
			require.Equal(t, tournamentID, command.TournamentID)
			require.Equal(t, commandID, command.CommandID)
			return inbound.TournamentAdmissionMutation{
				Changed: false,
				View: inbound.TournamentAdmissionView{
					TournamentID:      tournamentID,
					PlayerID:          playerID,
					ParticipantID:     participantID,
					Seed:              2,
					Status:            inbound.TournamentAdmissionStatusWithdrawn,
					Attendance:        domain.AttendanceStateWithdrawn,
					TournamentState:   domain.TournamentStateRegistration,
					RosterRevision:    3,
					PlannedRosterSize: 8,
					RosterSize:        1,
				},
			}, nil
		},
	}
	server := New(Dependencies{TournamentAdmission: stub})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/tournaments/"+tournamentID.String()+"/participant/queue", nil)
	serveWithPlayer(t, playerID, recorder, req, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.CancelTournamentAdmission(w, r, tournamentID, api.CancelTournamentAdmissionParams{IdempotencyKey: commandID})
	}))

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload api.TournamentAdmissionMutation
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.False(t, payload.Changed)
	require.Equal(t, api.TournamentAdmissionStatusWithdrawn, payload.View.Status)
	require.Equal(t, participantID, *payload.View.ParticipantId)
	require.Equal(t, api.TournamentAdmissionViewAttendanceWithdrawn, *payload.View.Attendance)
}

func TestTournamentAdmissionViewResponseRejectsInvalidNotRegisteredShape(t *testing.T) {
	t.Parallel()

	_, err := tournamentAdmissionViewResponse(inbound.TournamentAdmissionView{
		TournamentID:      uuid.New(),
		PlayerID:          uuid.New(),
		Status:            inbound.TournamentAdmissionStatusNotRegistered,
		TournamentState:   domain.TournamentStateRegistration,
		RosterRevision:    1,
		PlannedRosterSize: 8,
		RosterSize:        0,
		Seed:              1,
	})
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestTournamentAdmissionRoutesRequirePlayerSession(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			players := middlewaremocks.NewMockPlayerSessionReader(t)
			server := New(Dependencies{TournamentAdmission: &tournamentAdmissionStub{}})
			handler := newTournamentAdmissionHTTPHandler(t, server, players, newAllowingRateLimiter(t))
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(method, "/api/v1/tournaments/10000000-0000-0000-0000-000000000001/participant/queue", nil)
			request.Header.Set("Idempotency-Key", uuid.NewString())
			request.Header.Set(middleware.CSRFHeaderName, "request-without-session")

			handler.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusUnauthorized, recorder.Code)
		})
	}
}

func TestTournamentAdmissionRoutesRejectInvalidPlayerCSRF(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		csrfCookie bool
		header     string
	}{
		{name: "missing", header: ""},
		{name: "mismatch", csrfCookie: true, header: "different-token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sessionToken := uuid.New()
			players := middlewaremocks.NewMockPlayerSessionReader(t)
			server := New(Dependencies{TournamentAdmission: &tournamentAdmissionStub{}})
			handler := newTournamentAdmissionHTTPHandler(t, server, players, newAllowingRateLimiter(t))
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tournaments/10000000-0000-0000-0000-000000000001/participant/queue", nil)
			request.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: sessionToken.String()})
			request.Header.Set("Idempotency-Key", uuid.NewString())
			if test.csrfCookie {
				csrf, err := middleware.NewPlayerCSRFToken(sessionToken)
				require.NoError(t, err)
				request.AddCookie(&http.Cookie{Name: middleware.PlayerCSRFCookieName, Value: csrf})
			}
			if test.header != "" {
				request.Header.Set(middleware.CSRFHeaderName, test.header)
			}

			handler.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusForbidden, recorder.Code)
		})
	}
}

func TestTournamentAdmissionRoutesValidateHeadersAfterSession(t *testing.T) {
	t.Parallel()

	sessionToken := uuid.New()
	players := middlewaremocks.NewMockPlayerSessionReader(t)
	server := New(Dependencies{TournamentAdmission: &tournamentAdmissionStub{}})
	handler := newTournamentAdmissionHTTPHandler(t, server, players, newAllowingRateLimiter(t))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tournaments/10000000-0000-0000-0000-000000000001/participant/queue", nil)
	setPlayerSessionAndCSRF(t, request, sessionToken)

	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestTournamentAdmissionRoutesRateLimitAuthenticatedPlayer(t *testing.T) {
	t.Parallel()

	playerID := uuid.New()
	sessionToken := uuid.New()
	players := middlewaremocks.NewMockPlayerSessionReader(t)
	players.EXPECT().GetBySessionToken(mock.Anything, sessionToken).Return(&domain.Player{ID: playerID}, nil).Once()
	limiter := middlewaremocks.NewMockRateLimiter(t)
	limiter.EXPECT().Allow(mock.Anything).Return(false).Once()
	limiter.EXPECT().RetryAfter().Return("17").Once()
	server := New(Dependencies{TournamentAdmission: &tournamentAdmissionStub{}})
	handler := newTournamentAdmissionHTTPHandler(t, server, players, limiter)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tournaments/10000000-0000-0000-0000-000000000001/participant/queue", nil)
	setPlayerSessionAndCSRF(t, request, sessionToken)
	request.Header.Set("Idempotency-Key", uuid.NewString())

	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "17", recorder.Header().Get("Retry-After"))
}

func newTournamentAdmissionHTTPHandler(
	t *testing.T,
	server *Server,
	players *middlewaremocks.MockPlayerSessionReader,
	limiter middleware.RateLimiter,
) http.Handler {
	t.Helper()
	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)
	server.participantTournamentReadLimiter = limiter
	server.participantTournamentMutationLimiter = limiter
	return middleware.CSRFGuard()(NewHandler(server, HandlerOptions{
		PlayerRepo:       players,
		RequestValidator: validator,
	}))
}

func setPlayerSessionAndCSRF(t *testing.T, request *http.Request, sessionToken uuid.UUID) {
	t.Helper()
	csrf, err := middleware.NewPlayerCSRFToken(sessionToken)
	require.NoError(t, err)
	request.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: sessionToken.String()})
	request.AddCookie(&http.Cookie{Name: middleware.PlayerCSRFCookieName, Value: csrf})
	request.Header.Set(middleware.CSRFHeaderName, csrf)
}

func serveWithPlayer(t *testing.T, playerID uuid.UUID, recorder *httptest.ResponseRecorder, request *http.Request, handler http.Handler) {
	t.Helper()
	token := uuid.New()
	players := middlewaremocks.NewMockPlayerSessionReader(t)
	players.EXPECT().GetBySessionToken(mock.Anything, token).Return(&domain.Player{ID: playerID, SessionToken: &token}, nil).Once()
	request.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: token.String()})
	middleware.PlayerSession(players)(handler).ServeHTTP(recorder, request)
}

var _ inbound.TournamentAdmissionUseCase = (*tournamentAdmissionStub)(nil)
