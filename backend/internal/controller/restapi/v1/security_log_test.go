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

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

func TestAdminLoginSecurityLogRedactsCredentialsAndTokens(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	server := New(Dependencies{
		AdminAuth:    adminLoginLogStub{},
		LoginLimiter: middleware.NewLoginRateLimiter(t.Context(), 10, time.Minute, time.Minute),
		Now:          func() time.Time { return time.Unix(100, 0).UTC() },
		Log:          newV1TestLogger(t, &logs),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"super-secret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.10:1234"
	rr := httptest.NewRecorder()

	server.AdminLogin(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	rawLogs := logs.String()
	require.NotContains(t, rawLogs, "super-secret")
	require.NotContains(t, rawLogs, "access-token")
	require.NotContains(t, rawLogs, "refresh-token")

	entry := requireSecurityLogEntry(t, rawLogs, "admin.login")
	require.Equal(t, "success", entry["outcome"])
	require.Equal(t, "198.51.100.10", entry["client_ip"])
	require.NotContains(t, entry, "error_code")
}

func TestAdminLoginFailureSecurityLogUsesErrorCodeOnly(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	server := New(Dependencies{
		AdminAuth:    adminLoginLogStub{err: apperr.ErrInvalidCredentials},
		LoginLimiter: middleware.NewLoginRateLimiter(t.Context(), 10, time.Minute, time.Minute),
		Log:          newV1TestLogger(t, &logs),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"wrong-password"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.AdminLogin(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
	rawLogs := logs.String()
	require.NotContains(t, rawLogs, "wrong-password")

	entry := requireSecurityLogEntry(t, rawLogs, "admin.login")
	require.Equal(t, "failure", entry["outcome"])
	require.Equal(t, string(apperr.CodeInvalidCredentials), entry["error_code"])
}

func TestPlayerJoinSecurityLogRedactsSessionToken(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	sessionToken := uuid.New()
	playerID := uuid.New()
	server := New(Dependencies{
		Players: &playerSessionStub{
			joinPlayer: &domain.Player{
				ID:           playerID,
				Username:     "alice",
				SessionToken: &sessionToken,
				Status:       domain.PlayerStatusIdle,
			},
		},
		JoinLimiter: middleware.NewJoinRateLimiter(t.Context(), 10, time.Minute, time.Minute),
		Log:         newV1TestLogger(t, &logs),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/join", strings.NewReader(`{"username":"alice"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.JoinPlayer(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	rawLogs := logs.String()
	require.NotContains(t, rawLogs, sessionToken.String())

	entry := requireSecurityLogEntry(t, rawLogs, "player.join")
	require.Equal(t, "success", entry["outcome"])
	require.Equal(t, playerID.String(), entry["player_id"])
}

type adminLoginLogStub struct {
	err error
}

func (s adminLoginLogStub) Login(context.Context, string) (*usecase.TokenPair, error) {
	if s.err != nil {
		return nil, s.err
	}
	now := time.Unix(100, 0).UTC()
	return &usecase.TokenPair{
		AccessToken:      "access-token",
		RefreshToken:     "refresh-token",
		AccessExpiresAt:  now.Add(time.Minute),
		RefreshExpiresAt: now.Add(time.Hour),
	}, nil
}

func (adminLoginLogStub) Refresh(context.Context, string) (*usecase.TokenPair, error) {
	panic("unused")
}

func (adminLoginLogStub) Logout(context.Context, string, ...string) error {
	panic("unused")
}

func newV1TestLogger(t *testing.T, buf *bytes.Buffer) logkit.Logger {
	t.Helper()

	log, err := logkit.New(
		logkit.WithLevel(logkit.DebugLevel),
		logkit.WithSyncWriter(buf),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, log.Close())
	})
	return log
}

func requireSecurityLogEntry(t *testing.T, raw, event string) map[string]any {
	t.Helper()

	raw = strings.TrimSpace(raw)
	require.NotEmpty(t, raw)
	for _, line := range strings.Split(raw, "\n") {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if entry["message"] == "security event" && entry["event"] == event {
			return entry
		}
	}
	t.Fatalf("security event %q not found in logs: %s", event, raw)
	return nil
}
