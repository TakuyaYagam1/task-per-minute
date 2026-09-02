package middleware

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

func TestArenaHTTPSecurityMiddleware(t *testing.T) {
	t.Parallel()

	allowedTournamentID := uuid.MustParse("80fd5c56-a668-4a54-bbd2-530721add556")
	foreignTournamentID := uuid.MustParse("bbf39d0c-4049-4b9a-870d-21d3e9a91a47")
	commandID := uuid.MustParse("c4dfc84f-a0fb-4f53-9959-1710653d905c")

	t.Run("denies cross-tournament operator access", func(t *testing.T) {
		authorizer := ArenaScopeAuthorizerFunc(func(_ context.Context, access ArenaAccess) bool {
			return access.Role == ArenaRoleOperator && access.ActorID == "admin" && access.TournamentID == allowedTournamentID
		})
		handler := ArenaAuthorization(authorizer)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("cross-tournament request reached handler")
		}))
		request := httptest.NewRequest(http.MethodGet, "/api/v1/arena/operator/tournaments/"+foreignTournamentID.String()+"/roster", nil)
		request = request.WithContext(withAdminClaims(request.Context(), &adminusecase.Claims{Subject: "admin", JTI: "session"}))
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusForbidden, recorder.Code)
		require.NotContains(t, recorder.Body.String(), "authorization backend")
	})

	t.Run("denies participant role confusion on operator endpoints", func(t *testing.T) {
		playerID := uuid.MustParse("313bb476-d92b-49bb-82d0-07c6a4119349")
		authorizer := ArenaScopeAuthorizerFunc(func(context.Context, ArenaAccess) bool { return true })
		handler := ArenaAuthorization(authorizer)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("wrong role reached handler")
		}))
		request := httptest.NewRequest(http.MethodGet, "/api/v1/arena/operator/audit?tournament_id="+allowedTournamentID.String(), nil)
		request = request.WithContext(withPlayer(request.Context(), &domain.Player{ID: playerID}))
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, request)

		require.Equal(t, http.StatusForbidden, recorder.Code)
	})

	t.Run("allows public Arena reads without an identity", func(t *testing.T) {
		called := 0
		handler := ArenaAuthorization(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called++
			w.WriteHeader(http.StatusNoContent)
		}))
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/arena/public/tournaments/"+allowedTournamentID.String(), nil))

		require.Equal(t, http.StatusNoContent, recorder.Code)
		require.Equal(t, 1, called)
	})

	t.Run("requires pause confirmation reason and idempotency", func(t *testing.T) {
		limits := NewArenaRequestLimits(ArenaLimitConfig{OperatorMutation: ArenaEndpointLimit{Requests: 10, Window: time.Hour}})
		handler := limits.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		request := func(key, body string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/arena/operator/tournaments/"+allowedTournamentID.String()+"/actions", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if key != "" {
				req.Header.Set("Idempotency-Key", key)
			}
			req = req.WithContext(withAdminClaims(req.Context(), &adminusecase.Claims{Subject: "admin", JTI: "session"}))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			return recorder
		}

		require.Equal(t, http.StatusBadRequest, request("", `{"expected_projection_revision":4,"action":"pause","confirmed":true,"reason":"network issue"}`).Code)
		require.Equal(t, http.StatusBadRequest, request(commandID.String(), `{"expected_projection_revision":4,"action":"pause","confirmed":false,"reason":""}`).Code)
		require.Equal(t, http.StatusNoContent, request(commandID.String(), `{"expected_projection_revision":4,"action":"pause","confirmed":true,"reason":"network issue"}`).Code)
	})

	t.Run("rejects replay before rate limiting and limits a new command", func(t *testing.T) {
		limits := NewArenaRequestLimits(ArenaLimitConfig{OperatorMutation: ArenaEndpointLimit{Requests: 1, Window: time.Hour}})
		handler := limits.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		request := func(key uuid.UUID) *httptest.ResponseRecorder {
			body := `{"expected_projection_revision":4,"action":"pause","confirmed":true,"reason":"network issue"}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/arena/operator/tournaments/"+allowedTournamentID.String()+"/actions", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", key.String())
			req = req.WithContext(withAdminClaims(req.Context(), &adminusecase.Claims{Subject: "admin", JTI: "session"}))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			return recorder
		}

		require.Equal(t, http.StatusNoContent, request(commandID).Code)
		replay := request(commandID)
		require.Equal(t, http.StatusConflict, replay.Code)
		require.NotContains(t, replay.Body.String(), commandID.String())
		overLimit := request(uuid.MustParse("2ae67d12-3a38-42ff-af5b-18ac91c1bc0c"))
		require.Equal(t, http.StatusTooManyRequests, overLimit.Code)
		require.Equal(t, "3600", overLimit.Header().Get("Retry-After"))
	})

	t.Run("allows the same command to retry after a failed handler", func(t *testing.T) {
		limits := NewArenaRequestLimits(ArenaLimitConfig{OperatorMutation: ArenaEndpointLimit{Requests: 10, Window: time.Hour}})
		calls := 0
		handler := limits.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		request := func() *httptest.ResponseRecorder {
			body := `{"expected_projection_revision":4,"action":"pause","confirmed":true,"reason":"network issue"}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/arena/operator/tournaments/"+allowedTournamentID.String()+"/actions", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", commandID.String())
			req = req.WithContext(withAdminClaims(req.Context(), &adminusecase.Claims{Subject: "admin", JTI: "session"}))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			return recorder
		}

		require.Equal(t, http.StatusInternalServerError, request().Code)
		require.Equal(t, http.StatusNoContent, request().Code)
		require.Equal(t, http.StatusConflict, request().Code)
		require.Equal(t, 2, calls)
	})

	t.Run("keeps casual endpoints outside Arena limits", func(t *testing.T) {
		limits := NewArenaRequestLimits(ArenaLimitConfig{PublicRead: ArenaEndpointLimit{Requests: 1, Window: time.Hour}})
		calls := 0
		handler := limits.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.WriteHeader(http.StatusNoContent)
		}))
		request := func(path string) int {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			return recorder.Code
		}

		require.Equal(t, http.StatusNoContent, request("/api/v1/arena/public/tournaments/"+allowedTournamentID.String()))
		require.Equal(t, http.StatusTooManyRequests, request("/api/v1/arena/public/tournaments/"+allowedTournamentID.String()))
		require.Equal(t, http.StatusNoContent, request("/api/v1/leaderboard"))
		require.Equal(t, 2, calls)
	})

	t.Run("strict ingress rejects unknown and oversized bodies without echo", func(t *testing.T) {
		validator, err := OpenAPIRequestValidator(context.Background(), logkit.Noop())
		require.NoError(t, err)
		handler := validator(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("invalid request reached handler")
		}))

		unknownRequest := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/arena/operator/tournaments",
			strings.NewReader(`{"expected_projection_revision":0,"preset":"arena_v1","roster_size":8,"operator_id":"private"}`),
		)
		unknownRequest.Header.Set("Content-Type", "application/json")
		unknownRequest.Header.Set("Idempotency-Key", commandID.String())
		unknownRecorder := httptest.NewRecorder()
		handler.ServeHTTP(unknownRecorder, unknownRequest)
		require.Equal(t, http.StatusBadRequest, unknownRecorder.Code)
		require.NotContains(t, unknownRecorder.Body.String(), "private")

		oversizedRequest := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/arena/operator/tournaments",
			bytes.NewReader(bytes.Repeat([]byte("x"), (1<<20)+1)),
		)
		oversizedRequest.Header.Set("Content-Type", "application/json")
		oversizedRequest.Header.Set("Idempotency-Key", commandID.String())
		oversizedRecorder := httptest.NewRecorder()
		handler.ServeHTTP(oversizedRecorder, oversizedRequest)
		require.Equal(t, http.StatusRequestEntityTooLarge, oversizedRecorder.Code)
		require.Less(t, oversizedRecorder.Body.Len(), 1024)
	})
}
