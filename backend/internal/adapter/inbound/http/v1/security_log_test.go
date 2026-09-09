package v1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

func TestAdminLoginSecurityLogRedactsCredentialsAndTokens(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	now := time.Unix(100, 0).UTC()
	pair := &authusecase.TokenPair{
		AccessToken:      "access-token",
		RefreshToken:     "refresh-token",
		AccessExpiresAt:  now.Add(time.Minute),
		RefreshExpiresAt: now.Add(time.Hour),
	}
	auth := NewMockAdminAuthService(t)
	auth.EXPECT().Login(mock.Anything, "super-secret").Return(pair, nil)
	server := New(Dependencies{
		AdminAuth:    auth,
		LoginLimiter: newAllowingRateLimiter(t),
		Now:          func() time.Time { return time.Unix(100, 0).UTC() },
		Log:          newV1TestLogger(t, &logs),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"super-secret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.10:1234"
	rr := httptest.NewRecorder()

	server.LoginAdmin(rr, req)

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
	auth := NewMockAdminAuthService(t)
	auth.EXPECT().Login(mock.Anything, "wrong-password").Return(nil, domain.ErrInvalidCredentials)
	server := New(Dependencies{
		AdminAuth:    auth,
		LoginLimiter: newAllowingRateLimiter(t),
		Log:          newV1TestLogger(t, &logs),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"wrong-password"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.LoginAdmin(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
	rawLogs := logs.String()
	require.NotContains(t, rawLogs, "wrong-password")

	entry := requireSecurityLogEntry(t, rawLogs, "admin.login")
	require.Equal(t, "failure", entry["outcome"])
	require.Equal(t, string(domain.ErrorCodeInvalidCredentials), entry["error_code"])
}

func TestPlayerJoinSecurityLogRedactsSessionToken(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	sessionToken := uuid.New()
	playerID := uuid.New()
	players := NewMockPlayerService(t)
	players.EXPECT().Join(mock.Anything, "alice").Return(&domain.Player{
		ID:           playerID,
		Username:     "alice",
		SessionToken: &sessionToken,
	}, nil)
	server := New(Dependencies{
		Players:     players,
		JoinLimiter: newAllowingRateLimiter(t),
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
