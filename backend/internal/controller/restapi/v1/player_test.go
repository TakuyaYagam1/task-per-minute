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
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	usecasemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/mocks"
)

func TestJoinPlayerSetsHttpOnlySessionCookie(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	playerID := uuid.New()
	stub := &playerSessionStub{
		joinPlayer: &domain.Player{
			ID:           playerID,
			Username:     "alice",
			SessionToken: &token,
			Status:       domain.PlayerStatusIdle,
		},
	}
	server := New(Dependencies{Players: stub})

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

	server := New(Dependencies{Players: &playerSessionStub{}})

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

	server := New(Dependencies{Players: &playerSessionStub{}})

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

func TestLogoutPlayerClearsCookieAndInvalidatesSession(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	stub := playerSessionStub{}
	server := New(Dependencies{Players: &stub})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/logout", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: token.String()})
	rr := httptest.NewRecorder()

	server.LogoutPlayer(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	require.Equal(t, token, stub.logoutToken)

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
		Status:       domain.PlayerStatusIdle,
		CreatedAt:    time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC),
	}
	stub := &playerSessionStub{
		me: &usecase.PlayerWithActiveDuel{
			Player: player,
		},
	}
	server := New(Dependencies{Players: stub})
	playersRepo := usecasemocks.NewMockPlayerRepo(t)
	playersRepo.EXPECT().
		GetBySessionToken(mock.Anything, token).
		Return(player, nil)
	handler := middleware.PlayerSession(playersRepo)(http.HandlerFunc(server.GetMe))

	req := httptest.NewRequest(http.MethodGet, "https://app.example.com/api/v1/players/me", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: token.String()})
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, token, stub.getMeToken)

	cookies := rr.Result().Cookies()
	require.Len(t, cookies, 1)
	csrfCookie := requireCookie(t, cookies, middleware.PlayerCSRFCookieName)
	require.NotEmpty(t, csrfCookie.Value)
	require.Equal(t, csrfCookie.Value, rr.Header().Get(middleware.CSRFHeaderName))
	require.False(t, csrfCookie.HttpOnly)
	require.True(t, csrfCookie.Secure)
}

type playerSessionStub struct {
	joinPlayer  *domain.Player
	me          *usecase.PlayerWithActiveDuel
	getMeToken  uuid.UUID
	logoutToken uuid.UUID
}

func (s *playerSessionStub) Join(context.Context, string) (*domain.Player, error) {
	return s.joinPlayer, nil
}

func (s *playerSessionStub) GetMe(_ context.Context, token uuid.UUID) (*usecase.PlayerWithActiveDuel, error) {
	s.getMeToken = token
	return s.me, nil
}

func (s *playerSessionStub) Logout(_ context.Context, token uuid.UUID) error {
	s.logoutToken = token
	return nil
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
