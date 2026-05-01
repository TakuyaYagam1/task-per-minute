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

func TestPlayerSession_MissingHeaderReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	players := usecasemocks.NewMockPlayerRepo(t)
	handler := middleware.PlayerSession(players)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/players/me", nil))

	requireUnauthorized(t, rr)
}

func TestPlayerSession_InvalidUUIDReturnsUnauthorized(t *testing.T) {
	t.Parallel()

	players := usecasemocks.NewMockPlayerRepo(t)
	handler := middleware.PlayerSession(players)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/players/me", nil)
	req.Header.Set("X-Session-Token", "not-a-uuid")

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
	req.Header.Set("X-Session-Token", token.String())

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	requireUnauthorized(t, rr)
}

func TestPlayerSession_ValidTokenInjectsPlayer(t *testing.T) {
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
	req.Header.Set("X-Session-Token", token.String())

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
}
