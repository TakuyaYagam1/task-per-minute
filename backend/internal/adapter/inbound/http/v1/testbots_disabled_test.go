//go:build !testtools

package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type productionBotSessionReader struct{}

func (productionBotSessionReader) GetBySessionToken(context.Context, uuid.UUID) (*domain.Player, error) {
	return &domain.Player{ID: uuid.MustParse("11111111-1111-4111-8111-111111111111")}, nil
}

func TestProductionBuildHasNoBotRoutes(t *testing.T) {
	t.Setenv("TEST_BOTS_CONTROLLER_PLAYER_ID", "11111111-1111-4111-8111-111111111111")
	t.Setenv("TEST_BOTS_URL", "http://127.0.0.1:8090")
	router := chi.NewRouter()
	registerTestBotRoutes(&Server{}, HandlerOptions{Router: router, PlayerRepo: productionBotSessionReader{}})
	for _, path := range []string{"/api/v1/players/test-bots/11111111-1111-4111-8111-111111111111/state", "/internal/test-bots/11111111-1111-4111-8111-111111111111/metadata"} {
		r := httptest.NewRecorder()
		request := httptest.NewRequest("GET", path, nil)
		request.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: uuid.NewString()})
		router.ServeHTTP(r, request)
		if r.Code != 404 {
			t.Fatalf("production test route returned %d", r.Code)
		}
	}
}
