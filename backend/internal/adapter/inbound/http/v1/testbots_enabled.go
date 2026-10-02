//go:build testtools

package v1

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// The explicit test capability permits these reads only in a testtools build.
// It does not create an admin session or expose an operator snapshot to players.
//
//nolint:gocyclo // Test routes are installed together with their session, CSRF and internal capability boundaries.
func registerTestBotRoutes(s *Server, opts HandlerOptions) {
	controller, err := uuid.Parse(os.Getenv("TEST_BOTS_CONTROLLER_PLAYER_ID"))
	target, parseErr := url.Parse(os.Getenv("TEST_BOTS_URL"))
	if s == nil || opts.Router == nil || opts.PlayerRepo == nil || err != nil || controller == uuid.Nil || parseErr != nil || target.Host == "" || target.Scheme != "http" || target.User != nil || target.Path != "" {
		return
	}
	keyFile := os.Getenv("TEST_BOTS_KEY_FILE")
	key := func() string {
		data, e := os.ReadFile(keyFile) //nolint:gosec // Deployment-configured control volume path, never derived from a request.
		if e != nil || len(data) != 64 {
			return ""
		}
		return string(data)
	}
	proxy := &httputil.ReverseProxy{Rewrite: func(p *httputil.ProxyRequest) {
		p.SetURL(target)
		p.Out.Header.Del("Authorization")
		p.Out.Header.Del("Cookie")
		if strings.HasPrefix(p.In.URL.Path, "/api/v1/players/") {
			if session, err := p.In.Cookie(middleware.PlayerSessionCookieName); err == nil {
				p.Out.AddCookie(session)
			}
		}
		p.Out.Header.Del("X-Test-Bots-Actor")
		p.Out.Header.Del("X-Test-Bots-Key")
		p.Out.Header.Set("X-Test-Bots-Key", key())
		p.Out.Header.Set("X-Test-Bots-Actor", controller.String())
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "test service unavailable", http.StatusServiceUnavailable)
	}}
	wrap := func(handler http.Handler) http.Handler {
		for i := len(opts.Middlewares) - 1; i >= 0; i-- {
			handler = opts.Middlewares[i](handler)
		}
		return middleware.NoStoreSensitiveResponses()(handler)
	}
	var playerHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player, ok := middleware.GetPlayerFromCtx(r.Context())
		if !ok || player.ID != controller {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if key() == "" {
			http.Error(w, "test service unavailable", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		proxy.ServeHTTP(w, r)
	})
	playerHandler = wrap(middleware.PlayerSession(opts.PlayerRepo)(middleware.CSRFGuard()(playerHandler)))
	opts.Router.Handle("/api/v1/players/test-bots/{tournament_id}/*", playerHandler)
	if opts.AdminAuth != nil {
		admin := wrap(middleware.AdminSession(opts.AdminAuth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/pairings") {
				http.NotFound(w, r)
				return
			}
			proxy.ServeHTTP(w, r)
		})))
		opts.Router.Handle("/api/v1/admin/test-bots/{tournament_id}/pairings", admin)
	}
	opts.Router.Get("/internal/test-bots/{tournament_id}/metadata", func(w http.ResponseWriter, r *http.Request) {
		expected := key()
		if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(r.Header.Get("X-Test-Bots-Key"))) != 1 {
			http.NotFound(w, r)
			return
		}
		id, e := uuid.Parse(chi.URLParam(r, "tournament_id"))
		if e != nil {
			http.NotFound(w, r)
			return
		}
		if s.tournamentController == nil || s.admin == nil || s.configuration == nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		view, e := s.admin.GetOperatorSnapshot(r.Context(), inbound.AdminSnapshotQuery{Operator: inbound.AdminOperatorIdentity{ActorID: controller}, TournamentID: id})
		if e != nil {
			http.NotFound(w, r)
			return
		}
		snapshot, e := tournamentOperatorSnapshotResponse(view)
		if e != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		cfg, e := s.configuration.GetTournamentConfiguration(r.Context(), inbound.AdminTournamentConfigurationQuery{Operator: inbound.AdminOperatorIdentity{ActorID: controller}, TournamentID: id})
		if e != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		configuration, e := tournamentConfigurationResponse(cfg)
		if e != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(struct {
			Tournament    any `json:"tournament"`
			Roster        any `json:"roster"`
			Configuration any `json:"configuration"`
			Series        any `json:"series"`
			Waves         any `json:"waves"`
		}{snapshot.Tournament, snapshot.Roster, configuration, snapshot.Series, snapshot.Waves})
	})
}
