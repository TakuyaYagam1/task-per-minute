package middleware_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecasemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/mocks"
)

func TestPlayerSession_MissingCookieReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	players := usecasemocks.NewMockPlayerRepo(t)
	handler := middleware.PlayerSession(players)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/players/me", nil))

	requireUnauthorized(t, rr)
}

func TestPlayerSession_InvalidCookieReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	players := usecasemocks.NewMockPlayerRepo(t)
	handler := middleware.PlayerSession(players)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/players/me", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: "not-a-uuid"})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	requireUnauthorized(t, rr)
}

func TestPlayerSession_RepoErrorReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	players := usecasemocks.NewMockPlayerRepo(t)
	players.EXPECT().
		GetBySessionToken(mock.Anything, token).
		Return(nil, errors.New("not found"))

	handler := middleware.PlayerSession(players)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/players/me", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: token.String()})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	requireUnauthorized(t, rr)
}

func TestPlayerSession_ValidCookieInjectsPlayer(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &token,
		Status:       domain.PlayerStatusIdle,
		CreatedAt:    time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	}

	players := usecasemocks.NewMockPlayerRepo(t)
	players.EXPECT().
		GetBySessionToken(mock.Anything, token).
		Return(player, nil)

	handler := middleware.PlayerSession(players)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := middleware.GetPlayerFromCtx(r.Context())
		require.True(t, ok)
		require.Same(t, player, got)
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/players/me", nil)
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: token.String()})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestPlayerSession_HeaderTokenIsRejected(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	players := usecasemocks.NewMockPlayerRepo(t)
	handler := middleware.PlayerSession(players)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/players/me", nil)
	req.Header.Set("X-Session-Token", token.String())

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	requireUnauthorized(t, rr)
}

func TestPlayerSession_InvalidHeaderDoesNotOverrideCookie(t *testing.T) {
	t.Parallel()

	cookieToken := uuid.New()
	player := &domain.Player{
		ID:           uuid.New(),
		Username:     "alice",
		SessionToken: &cookieToken,
		Status:       domain.PlayerStatusIdle,
		CreatedAt:    time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	}

	players := usecasemocks.NewMockPlayerRepo(t)
	players.EXPECT().
		GetBySessionToken(mock.Anything, cookieToken).
		Return(player, nil)

	handler := middleware.PlayerSession(players)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/players/me", nil)
	req.Header.Set("X-Session-Token", "not-a-uuid")
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: cookieToken.String()})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestSetAndClearPlayerSessionCookie(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "https://app.example.com/api/v1/players/join", nil)
	setRecorder := httptest.NewRecorder()

	middleware.SetPlayerSessionCookie(setRecorder, req, token)

	setCookie := setRecorder.Result().Cookies()[0]
	require.Equal(t, middleware.PlayerSessionCookieName, setCookie.Name)
	require.Equal(t, token.String(), setCookie.Value)
	require.Equal(t, "/", setCookie.Path)
	require.True(t, setCookie.HttpOnly)
	require.True(t, setCookie.Secure)
	require.Equal(t, http.SameSiteLaxMode, setCookie.SameSite)

	clearRecorder := httptest.NewRecorder()
	middleware.ClearPlayerSessionCookie(clearRecorder, req)

	clearCookie := clearRecorder.Result().Cookies()[0]
	require.Equal(t, middleware.PlayerSessionCookieName, clearCookie.Name)
	require.Equal(t, -1, clearCookie.MaxAge)
	require.True(t, clearCookie.Expires.Before(time.Now()))
}
