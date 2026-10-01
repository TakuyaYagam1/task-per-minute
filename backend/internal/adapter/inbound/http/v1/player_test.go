package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestJoinPlayerReturnsGoneWithoutIssuingCookies(t *testing.T) {
	t.Parallel()

	server := New(Dependencies{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/join", nil)
	rr := httptest.NewRecorder()

	server.JoinPlayer(rr, req)

	require.Equal(t, http.StatusGone, rr.Code)
	require.Empty(t, rr.Result().Cookies())
	require.NotContains(t, rr.Body.String(), "session_token")
	require.JSONEq(t, `{
		"code":"player_account.join_retired",
		"type":"about:blank",
		"title":"Gone",
		"status":410,
		"detail":"nickname-only player sessions are retired",
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

func TestGetMeClearsCookiesWhenAccountWasDeletedAfterMiddlewareLookup(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		CreatedAt:    time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC),
	}
	players := NewMockPlayerService(t)
	players.EXPECT().GetCurrentPlayer(mock.Anything, token).Return(nil, domain.ErrAccountDeleted).Once()
	server := New(Dependencies{Players: players})
	playersRepo := middlewaremocks.NewMockPlayerSessionReader(t)
	playersRepo.EXPECT().GetBySessionToken(mock.Anything, token).Return(player, nil).Once()
	handler := middleware.PlayerSession(playersRepo)(http.HandlerFunc(server.GetCurrentPlayer))

	req := httptest.NewRequest(http.MethodGet, "https://app.example.com/api/v1/players/me", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: token.String()})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnauthorized, rr.Code)
	var problem api.ProblemDetails
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &problem))
	require.NotNil(t, problem.Code)
	require.Equal(t, string(domain.ErrorCodeAccountDeleted), *problem.Code)
	cookies := rr.Result().Cookies()
	require.Len(t, cookies, 2)
	for _, name := range []string{middleware.PlayerSessionCookieName, middleware.PlayerCSRFCookieName} {
		cookie := requireCookie(t, cookies, name)
		require.Equal(t, -1, cookie.MaxAge)
		require.True(t, cookie.Expires.Before(time.Now()))
	}
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
