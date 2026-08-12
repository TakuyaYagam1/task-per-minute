package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestPlayerSession_MissingCookieReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	players := &playerSessionReaderStub{}
	handler := middleware.PlayerSession(players)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/players/me", nil))

	requireUnauthorized(t, rr)
}

func TestPlayerSession_InvalidCookieReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	players := &playerSessionReaderStub{}
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
	players := &playerSessionReaderStub{err: errors.New("not found")}

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

	players := &playerSessionReaderStub{player: player}

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
	require.Equal(t, token, players.token)
}

func TestPlayerSession_HeaderTokenIsRejected(t *testing.T) {
	t.Parallel()

	token := uuid.New()
	players := &playerSessionReaderStub{}
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

	players := &playerSessionReaderStub{player: player}

	handler := middleware.PlayerSession(players)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/players/me", nil)
	req.Header.Set("X-Session-Token", "not-a-uuid")
	req.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: cookieToken.String()})

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	require.Equal(t, cookieToken, players.token)
}

type playerSessionReaderStub struct {
	player *domain.Player
	err    error
	token  uuid.UUID
}

func (s *playerSessionReaderStub) GetBySessionToken(_ context.Context, token uuid.UUID) (*domain.Player, error) {
	s.token = token
	return s.player, s.err
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
