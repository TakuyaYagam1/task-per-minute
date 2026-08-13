package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	adminusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

func TestNewHandler_AuthenticatesBeforeOpenAPIBodyValidation(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	handler := NewHandler(New(Dependencies{}), HandlerOptions{
		AdminAuth:        unusedAdminAccessVerifier{},
		RequestValidator: validator,
	})
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/admin/players/2c754c2e-8458-4417-b049-44c5f92840c7",
		strings.NewReader(`{}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestNewHandler_LeavesManualRoutesOutsideOpenAPIValidation(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	router := chi.NewRouter()
	router.Get("/ws", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	handler := NewHandler(New(Dependencies{}), HandlerOptions{
		Router:           router,
		RequestValidator: validator,
	})
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
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
					LoginLimiter: middleware.NewLoginRateLimiter(t.Context(), 1, time.Hour, time.Hour),
					Log:          log,
				}
			},
		},
		{
			name:  "admin refresh",
			path:  "/api/v1/admin/refresh",
			body:  `{}`,
			event: "admin.refresh",
			deps: func(t *testing.T, log logkit.Logger) Dependencies {
				return Dependencies{
					RefreshLimiter: middleware.NewLoginRateLimiter(t.Context(), 1, time.Hour, time.Hour),
					Log:            log,
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
					JoinLimiter: middleware.NewJoinRateLimiter(t.Context(), 1, time.Hour, time.Hour),
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

	limiter := middleware.NewLoginRateLimiter(t.Context(), 1, time.Hour, time.Hour)
	handler := NewHandler(New(Dependencies{
		AdminAuth:    adminLoginLogStub{err: apperr.ErrInvalidCredentials},
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

type unusedAdminAccessVerifier struct{}

func (unusedAdminAccessVerifier) VerifyAccess(context.Context, string) (*adminusecase.Claims, error) {
	panic("must not verify a missing token")
}
