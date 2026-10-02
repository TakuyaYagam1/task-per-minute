//go:build testtools

package v1

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type botSessionReader map[uuid.UUID]*domain.Player

func (s botSessionReader) GetBySessionToken(_ context.Context, token uuid.UUID) (*domain.Player, error) {
	if player := s[token]; player != nil {
		return player, nil
	}
	return nil, errors.New("invalid session")
}

func TestBotGatewaySessionAndCSRF(t *testing.T) {
	owner, ownerSession, otherSession := uuid.New(), uuid.New(), uuid.New()
	key := strings.Repeat("a", 64)
	keyFile := filepath.Join(t.TempDir(), "control.key")
	if err := os.WriteFile(keyFile, []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Test-Bots-Key") != key || r.Header.Get("X-Test-Bots-Actor") != owner.String() {
			t.Error("untrusted proxy headers")
		}
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	t.Setenv("TEST_BOTS_CONTROLLER_PLAYER_ID", owner.String())
	t.Setenv("TEST_BOTS_URL", upstream.URL)
	t.Setenv("TEST_BOTS_KEY_FILE", keyFile)
	router := chi.NewRouter()
	registerTestBotRoutes(&Server{}, HandlerOptions{Router: router, PlayerRepo: botSessionReader{ownerSession: {ID: owner}, otherSession: {ID: uuid.New()}}})
	path := "/api/v1/players/test-bots/" + uuid.NewString() + "/actions"
	for _, tc := range []struct {
		name    string
		session uuid.UUID
		csrf    bool
		status  int
	}{
		{"guest", uuid.Nil, false, 401}, {"other player", otherSession, true, 403}, {"missing CSRF", ownerSession, false, 403}, {"owner", ownerSession, true, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", path, strings.NewReader(`{"action":"pause"}`))
			r.Header.Set("X-Test-Bots-Key", "spoofed")
			r.Header.Set("X-Test-Bots-Actor", uuid.NewString())
			if tc.session != uuid.Nil {
				r.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: tc.session.String()})
			}
			if tc.csrf {
				token, err := middleware.NewPlayerCSRFToken(tc.session)
				if err != nil {
					t.Fatal(err)
				}
				r.AddCookie(&http.Cookie{Name: middleware.PlayerCSRFCookieName, Value: token})
				r.Header.Set(middleware.CSRFHeaderName, token)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d, want %d", w.Code, tc.status)
			}
			if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
				t.Error("response can be cached")
			}
		})
	}
	if calls != 1 {
		t.Fatalf("unexpected upstream calls: %d", calls)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/internal/test-bots/"+uuid.NewString()+"/metadata", nil))
	if w.Code != 404 {
		t.Fatal("internal metadata exposed without key")
	}
}
