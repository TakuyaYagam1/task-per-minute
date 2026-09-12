package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	middlewaremocks "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware/mocks"
	inboundwebsocket "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inboundmocks "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound/mocks"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

func TestNewHandler_AuthenticatesBeforeOpenAPIBodyValidation(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	handler := NewHandler(New(Dependencies{}), HandlerOptions{
		AdminAuth:        middlewaremocks.NewMockAdminAccessVerifier(t),
		RequestValidator: validator,
	})
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/admin/players/2c754c2e-8458-4417-b049-44c5f92840c7",
		strings.NewReader(`{}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.CSRFHeaderName, "contract-presence")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestNewHandler_RejectsMissingOrMalformedTournamentContentRevision(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	validPrefix := `{"expected_revision":0,"preset":"tournament_v1","name":"September Invitational","public_id":"september-invitational","planned_roster_size":8`
	for _, test := range []struct {
		name      string
		content   string
		wantError string
	}{
		{name: "missing", content: `}`, wantError: "missing"},
		{name: "malformed", content: `,"content_revision":"latest"}`, wantError: "malformed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			service := inboundmocks.NewMockTournamentUseCase(t)
			server := New(Dependencies{
				Tournaments:                       service,
				OperatorTournamentMutationLimiter: newAllowingRateLimiter(t),
			})
			handler := NewHandler(server, HandlerOptions{
				RequestValidator: validator,
			})
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/admin/tournaments",
				strings.NewReader(validPrefix+test.content),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "7524f043-40d5-40a5-a549-344c4640401f")
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusBadRequest, recorder.Code, test.wantError)
			service.AssertNotCalled(t, "CreateTournament", mock.Anything, mock.Anything)
		})
	}
}

func TestNewHandler_LeavesManualRoutesOutsideOpenAPIValidation(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	router := chi.NewRouter()
	router.Get(inboundwebsocket.TournamentPublicWebSocketPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	handler := NewHandler(New(Dependencies{}), HandlerOptions{
		Router:           router,
		RequestValidator: validator,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tournaments/99fdf1b2-2397-485e-8b84-04f950cebe71/realtime", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusSwitchingProtocols, rr.Code)
}

func TestNewHandler_RateLimitsInvalidPublicBodiesBeforeOpenAPIValidation(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	tests := []struct {
		name  string
		path  string
		body  string
		event string
		deps  func(*testing.T, logkit.Logger) Dependencies
	}{
		{
			name:  "admin login",
			path:  "/api/v1/admin/login",
			body:  `{"password":""}`,
			event: "admin.login",
			deps: func(t *testing.T, log logkit.Logger) Dependencies {
				return Dependencies{
					LoginLimiter: newOneRequestRateLimiter(t, "3600"),
					Log:          log,
				}
			},
		},
		{
			name:  "player join",
			path:  "/api/v1/players/join",
			body:  `{}`,
			event: "player.join",
			deps: func(t *testing.T, log logkit.Logger) Dependencies {
				return Dependencies{
					JoinLimiter: newOneRequestRateLimiter(t, "3600"),
					Log:         log,
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer
			handler := NewHandler(New(tt.deps(t, newV1TestLogger(t, &logs))), HandlerOptions{RequestValidator: validator})
			request := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
				req.Header.Set("Content-Type", "application/json")
				req.RemoteAddr = "198.51.100.42:1234"
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
				return rr
			}

			require.Equal(t, http.StatusBadRequest, request().Code)
			second := request()
			require.Equal(t, http.StatusTooManyRequests, second.Code)
			require.Equal(t, "3600", second.Header().Get("Retry-After"))

			entries := decodeJSONLines(t, logs.String())
			require.True(t, hasSecurityOutcome(entries, tt.event, securityOutcomeFailure))
			require.True(t, hasSecurityOutcome(entries, tt.event, securityOutcomeRateLimited))
		})
	}
}

func TestNewHandler_DoesNotDoubleCountValidPublicRequest(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	limiter := newOneRequestRateLimiter(t, "3600")
	auth := NewMockAdminAuthService(t)
	auth.EXPECT().Login(mock.Anything, "valid-password").Return(nil, domain.ErrInvalidCredentials).Once()
	handler := NewHandler(New(Dependencies{
		AdminAuth:    auth,
		LoginLimiter: limiter,
	}), HandlerOptions{RequestValidator: validator})
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/admin/login",
			strings.NewReader(`{"password":"valid-password"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "198.51.100.42:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	require.Equal(t, http.StatusUnauthorized, request().Code)
	require.Equal(t, http.StatusTooManyRequests, request().Code)
}

func TestNewHandlerRunsTournamentRateGuardAfterAuthentication(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)
	limiter := middlewaremocks.NewMockRateLimiter(t)
	limiter.EXPECT().Allow("operator:operator-42").Return(false).Once()
	limiter.EXPECT().RetryAfter().Return("60").Once()
	verifier := middlewaremocks.NewMockAdminAccessVerifier(t)
	verifier.EXPECT().VerifyAccess(mock.Anything, "admin-token").Return(&authusecase.Claims{
		Subject: "operator-42", JTI: "session-42", Kind: authusecase.TokenKindAccess,
	}, nil).Once()
	handler := NewHandler(New(Dependencies{
		OperatorTournamentMutationLimiter: limiter,
	}), HandlerOptions{
		AdminAuth:        verifier,
		RequestValidator: validator,
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/tournaments/10000000-0000-0000-0000-000000000001/actions",
		strings.NewReader(`{}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(middleware.CSRFHeaderName, "contract-presence")
	request.Header.Set("Idempotency-Key", "20000000-0000-0000-0000-000000000002")
	request.RemoteAddr = "198.51.100.42:1234"
	request.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: "admin-token"})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "60", recorder.Header().Get("Retry-After"))
}

func decodeJSONLines(t *testing.T, raw string) []map[string]any {
	t.Helper()

	decoder := json.NewDecoder(strings.NewReader(raw))
	var entries []map[string]any
	for decoder.More() {
		var entry map[string]any
		require.NoError(t, decoder.Decode(&entry))
		entries = append(entries, entry)
	}
	return entries
}

func hasSecurityOutcome(entries []map[string]any, event, outcome string) bool {
	for _, entry := range entries {
		if entry["message"] == "security event" && entry["event"] == event && entry["outcome"] == outcome {
			return true
		}
	}
	return false
}
