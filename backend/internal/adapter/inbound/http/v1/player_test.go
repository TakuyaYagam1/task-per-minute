package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	middlewaremocks "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestJoinPlayerSetsHttpOnlySessionCookie(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	playerID := uuid.New()
	player := &domain.Player{
		ID:           playerID,
		Username:     "alice",
		SessionToken: &token,
	}
	players := NewMockPlayerService(t)
	players.EXPECT().Join(mock.Anything, "alice").Return(player, nil)
	server := New(Dependencies{Players: players})

	req := httptest.NewRequest(http.MethodPost, "https://app.example.com/api/v1/players/join", strings.NewReader(`{"username":"alice"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.JoinPlayer(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.NotContains(t, rr.Body.String(), "session_token")

	var body struct {
		PlayerID uuid.UUID `json:"player_id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, playerID, body.PlayerID)

	cookies := rr.Result().Cookies()
	require.Len(t, cookies, 2)

	sessionCookie := requireCookie(t, cookies, middleware.PlayerSessionCookieName)
	require.Equal(t, token.String(), sessionCookie.Value)
	require.True(t, sessionCookie.HttpOnly)
	require.True(t, sessionCookie.Secure)
	require.Equal(t, http.SameSiteLaxMode, sessionCookie.SameSite)

	csrfCookie := requireCookie(t, cookies, middleware.PlayerCSRFCookieName)
	require.NotEmpty(t, csrfCookie.Value)
	require.Equal(t, csrfCookie.Value, rr.Header().Get(middleware.CSRFHeaderName))
	require.False(t, csrfCookie.HttpOnly)
	require.True(t, csrfCookie.Secure)
	require.Equal(t, http.SameSiteLaxMode, csrfCookie.SameSite)
}

func TestJoinPlayerRejectsUnsupportedMediaType(t *testing.T) {
	t.Parallel()

	server := New(Dependencies{Players: NewMockPlayerService(t)})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/join", strings.NewReader(`{"username":"alice"}`))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()

	server.JoinPlayer(rr, req)

	require.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
	require.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
	require.JSONEq(t, `{
		"type":"about:blank",
		"title":"Unsupported Media Type",
		"status":415,
		"detail":"content type must be application/json or application/*+json",
		"instance":"/api/v1/players/join",
		"request_id":""
	}`, rr.Body.String())
	require.Empty(t, rr.Result().Cookies())
}

func TestJoinPlayerRejectsOversizedBody(t *testing.T) {
	t.Parallel()

	server := New(Dependencies{Players: NewMockPlayerService(t)})

	body := `{"username":"` + strings.Repeat("a", 1<<20) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/join", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.JoinPlayer(rr, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
	require.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
	require.JSONEq(t, `{
		"type":"about:blank",
		"title":"Request Entity Too Large",
		"status":413,
		"detail":"request body is too large",
		"instance":"/api/v1/players/join",
		"request_id":""
	}`, rr.Body.String())
	require.Empty(t, rr.Result().Cookies())
}

func TestJoinPlayerRejectsActiveUsernameWithoutIssuingCookies(t *testing.T) {
	t.Parallel()

	players := NewMockPlayerService(t)
	players.EXPECT().Join(mock.Anything, "alice").Return(nil, domain.ErrUsernameTaken)
	server := New(Dependencies{Players: players})

	req := httptest.NewRequest(
		http.MethodPost,
		"https://app.example.com/api/v1/players/join",
		strings.NewReader(`{"username":"alice"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	server.JoinPlayer(rr, req)

	require.Equal(t, http.StatusConflict, rr.Code)
	require.Empty(t, rr.Result().Cookies())
	require.NotContains(t, rr.Body.String(), "player_id")
	require.NotContains(t, rr.Body.String(), "session_token")
	require.JSONEq(t, `{
		"type":"about:blank",
		"title":"Conflict",
		"status":409,
		"detail":"username already taken",
		"instance":"/api/v1/players/join",
		"request_id":""
	}`, rr.Body.String())
}

func TestLogoutPlayerClearsCookieAndInvalidatesSession(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	players := NewMockPlayerService(t)
	players.EXPECT().Logout(mock.Anything, token).Return(nil)
	server := New(Dependencies{Players: players})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/logout", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: token.String()})
	rr := httptest.NewRecorder()

	server.LogoutPlayer(rr, req, api.LogoutPlayerParams{})

	require.Equal(t, http.StatusNoContent, rr.Code)

	cookies := rr.Result().Cookies()
	require.Len(t, cookies, 2)

	sessionCookie := requireCookie(t, cookies, middleware.PlayerSessionCookieName)
	require.Equal(t, -1, sessionCookie.MaxAge)
	require.True(t, sessionCookie.Expires.Before(time.Now()))

	csrfCookie := requireCookie(t, cookies, middleware.PlayerCSRFCookieName)
	require.Equal(t, -1, csrfCookie.MaxAge)
	require.True(t, csrfCookie.Expires.Before(time.Now()))
}

func TestGetMeSetsCSRFCookieWhenMissing(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	playerID := uuid.New()
	player := &domain.Player{
		ID:           playerID,
		Username:     "alice",
		SessionToken: &token,
		CreatedAt:    time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC),
	}
	players := NewMockPlayerService(t)
	players.EXPECT().GetCurrentPlayer(mock.Anything, token).Return(player, nil)
	server := New(Dependencies{Players: players})
	playersRepo := middlewaremocks.NewMockPlayerSessionReader(t)
	playersRepo.EXPECT().GetBySessionToken(mock.Anything, token).Return(player, nil)
	handler := middleware.PlayerSession(playersRepo)(http.HandlerFunc(server.GetCurrentPlayer))

	req := httptest.NewRequest(http.MethodGet, "https://app.example.com/api/v1/players/me", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: token.String()})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	cookies := rr.Result().Cookies()
	require.Len(t, cookies, 1)
	csrfCookie := requireCookie(t, cookies, middleware.PlayerCSRFCookieName)
	require.NotEmpty(t, csrfCookie.Value)
	require.Equal(t, csrfCookie.Value, rr.Header().Get(middleware.CSRFHeaderName))
	require.False(t, csrfCookie.HttpOnly)
	require.True(t, csrfCookie.Secure)
}

func requireCookie(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	require.Failf(t, "cookie not found", "missing cookie %q", name)
	return nil
}
