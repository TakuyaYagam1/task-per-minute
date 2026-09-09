package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	middlewaremocks "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
)

func TestTournamentRatePolicyClassifiesCurrentHTTPRoutes(t *testing.T) {
	t.Parallel()

	server := New(Dependencies{})
	tests := []struct {
		name   string
		method string
		path   string
		want   tournamentRequestClass
		ok     bool
	}{
		{"public read", http.MethodGet, "/api/v1/tournaments/10000000-0000-0000-0000-000000000001", tournamentPublicRead, true},
		{"public head", http.MethodHead, "/api/v1/tournaments/10000000-0000-0000-0000-000000000001/scoreboard", tournamentPublicRead, true},
		{"operator list", http.MethodGet, "/api/v1/admin/tournaments", tournamentOperatorRead, true},
		{"operator audit", http.MethodGet, "/api/v1/admin/tournament-audit", tournamentOperatorRead, true},
		{"operator mutation", http.MethodPost, "/api/v1/admin/tournaments/10000000-0000-0000-0000-000000000001/actions", tournamentOperatorMutation, true},
		{"participant read", http.MethodGet, "/api/v1/tournaments/10000000-0000-0000-0000-000000000001/participant/lobby", tournamentParticipantRead, true},
		{"participant mutation", http.MethodPost, "/api/v1/tournaments/10000000-0000-0000-0000-000000000001/participant/waves/20000000-0000-0000-0000-000000000002/ready", tournamentParticipantMutation, true},
		{"websocket remains outside HTTP classes", http.MethodGet, "/api/v1/tournaments/10000000-0000-0000-0000-000000000001/realtime", "", false},
		{"unsupported public mutation", http.MethodPost, "/api/v1/tournaments/10000000-0000-0000-0000-000000000001", "", false},
		{"neighboring route remains unclassified", http.MethodGet, "/api/v1/leaderboard", "", false},
		{"unknown tournament suffix remains unclassified", http.MethodPost, "/api/v1/admin/tournaments/10000000-0000-0000-0000-000000000001/unknown", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, ok := server.tournamentRatePolicy(httptest.NewRequest(tt.method, tt.path, nil))
			require.Equal(t, tt.ok, ok)
			if ok {
				require.Equal(t, tt.want, policy.class)
			}
		})
	}
}

func TestTournamentRatePolicyMatchesCanonicalOpenAPISpec(t *testing.T) {
	t.Parallel()

	server := New(Dependencies{})
	expected := map[string]tournamentRequestClass{
		"listTournamentAudit":              tournamentOperatorRead,
		"listTournaments":                  tournamentOperatorRead,
		"createTournament":                 tournamentOperatorMutation,
		"getTournamentRoster":              tournamentOperatorRead,
		"replaceTournamentRoster":          tournamentOperatorMutation,
		"runTournamentRosterPreflight":     tournamentOperatorMutation,
		"lockTournamentRoster":             tournamentOperatorMutation,
		"unlockTournamentRoster":           tournamentOperatorMutation,
		"configureTournamentPairings":      tournamentOperatorMutation,
		"applyTournamentAction":            tournamentOperatorMutation,
		"controlTournamentWave":            tournamentOperatorMutation,
		"resolveTournamentNoShow":          tournamentOperatorMutation,
		"assignOperatorReserve":            tournamentOperatorMutation,
		"recordTournamentForfeit":          tournamentOperatorMutation,
		"replayTournamentGame":             tournamentOperatorMutation,
		"correctTournamentGameResult":      tournamentOperatorMutation,
		"exportTournamentIncident":         tournamentOperatorRead,
		"getOperatorSnapshot":              tournamentOperatorRead,
		"getParticipantLobby":              tournamentParticipantRead,
		"getParticipantAssignment":         tournamentParticipantRead,
		"setParticipantReady":              tournamentParticipantMutation,
		"submitParticipantDraftAction":     tournamentParticipantMutation,
		"submitParticipantFlag":            tournamentParticipantMutation,
		"surrenderParticipantSeries":       tournamentParticipantMutation,
		"applyParticipantPostSeriesAction": tournamentParticipantMutation,
		"getParticipantSnapshot":           tournamentParticipantRead,
		"getPublicTournament":              tournamentPublicRead,
		"getPublicScoreboard":              tournamentPublicRead,
		"getPublicBracket":                 tournamentPublicRead,
		"getPublicLiveDraft":               tournamentPublicRead,
		"getPublicSnapshot":                tournamentPublicRead,
	}

	registrations := openAPITournamentHTTPRegistrations(t)
	found := make(map[string]struct{}, len(expected))
	for _, registration := range registrations {
		want, ok := expected[registration.operation]
		if !ok {
			t.Fatalf("unclassified current tournament route %s %s (%s)", registration.method, registration.path, registration.operation)
		}
		if _, duplicate := found[registration.operation]; duplicate {
			t.Fatalf("duplicate generated tournament operation %s", registration.operation)
		}
		found[registration.operation] = struct{}{}

		path := tournamentRateSamplePath(registration.path)
		classes := tournamentRateClasses(registration.method, path)
		require.Len(t, classes, 1, "%s %s", registration.method, registration.path)
		require.Equal(t, want, classes[0], "%s %s", registration.method, registration.path)
		policy, classified := server.tournamentRatePolicy(httptest.NewRequest(registration.method, path, nil))
		require.True(t, classified, "%s %s", registration.method, registration.path)
		require.Equal(t, want, policy.class, "%s %s", registration.method, registration.path)

		if registration.method == http.MethodGet {
			headClasses := tournamentRateClasses(http.MethodHead, path)
			require.Equal(t, []tournamentRequestClass{want}, headClasses, "HEAD %s", registration.path)
		}
	}
	require.Len(t, found, len(expected))
	for operation := range expected {
		_, ok := found[operation]
		require.True(t, ok, "missing generated tournament operation %s", operation)
	}

	for _, path := range []string{
		"/api/v1/tournaments/10000000-0000-0000-0000-000000000001/realtime",
		"/api/v1/tournaments/10000000-0000-0000-0000-000000000001/participant/realtime",
		"/api/v1/admin/tournaments/10000000-0000-0000-0000-000000000001/realtime",
	} {
		classes := tournamentRateClasses(http.MethodGet, path)
		require.Empty(t, classes, "websocket route must stay outside the HTTP limiter: %s", path)
		_, classified := server.tournamentRatePolicy(httptest.NewRequest(http.MethodGet, path, nil))
		require.False(t, classified, "websocket route must stay outside the HTTP limiter: %s", path)
	}
}

type openAPITournamentHTTPRegistration struct {
	method    string
	path      string
	operation string
}

func openAPITournamentHTTPRegistrations(t *testing.T) []openAPITournamentHTTPRegistration {
	t.Helper()
	spec, err := api.GetSwagger()
	require.NoError(t, err)

	registrations := make([]openAPITournamentHTTPRegistration, 0)
	for path, item := range spec.Paths.Map() {
		if !isTournamentHTTPRoute(path) {
			continue
		}
		for method, operation := range item.Operations() {
			require.NotNil(t, operation)
			registrations = append(registrations, openAPITournamentHTTPRegistration{
				method: strings.ToUpper(method), path: path, operation: operation.OperationID,
			})
		}
	}
	return registrations
}

func isTournamentHTTPRoute(path string) bool {
	return path == "/api/v1/admin/tournament-audit" ||
		strings.HasPrefix(path, "/api/v1/admin/tournaments") ||
		strings.HasPrefix(path, "/api/v1/tournaments/")
}

func tournamentRateSamplePath(path string) string {
	replacer := strings.NewReplacer(
		"{tournament_id}", "10000000-0000-0000-0000-000000000001",
		"{wave_id}", "20000000-0000-0000-0000-000000000002",
		"{series_id}", "30000000-0000-0000-0000-000000000003",
		"{assignment_id}", "40000000-0000-0000-0000-000000000004",
		"{game_id}", "50000000-0000-0000-0000-000000000005",
	)
	return replacer.Replace(path)
}

func tournamentRateClasses(method, path string) []tournamentRequestClass {
	classes := make([]tournamentRequestClass, 0, 1)
	for _, route := range tournamentRateRoutes {
		if route.matches(method, path) {
			classes = append(classes, route.class)
		}
	}
	return classes
}

func TestTournamentRateGuardUsesClassScopedPrincipals(t *testing.T) {
	t.Parallel()

	t.Run("public uses client address", func(t *testing.T) {
		limiter := middlewaremocks.NewMockRateLimiter(t)
		limiter.EXPECT().Allow("public:198.51.100.42").Return(true).Once()
		server := New(Dependencies{PublicTournamentReadLimiter: limiter})
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/tournaments/10000000-0000-0000-0000-000000000001", nil)
		request.RemoteAddr = "198.51.100.42:1234"

		server.tournamentRateGuard()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(recorder, request)

		require.Equal(t, http.StatusNoContent, recorder.Code)
	})

	t.Run("operator never falls back to address", func(t *testing.T) {
		limiter := middlewaremocks.NewMockRateLimiter(t)
		limiter.EXPECT().Allow("operator:operator-42").Return(true).Once()
		verifier := middlewaremocks.NewMockAdminAccessVerifier(t)
		verifier.EXPECT().VerifyAccess(mock.Anything, "admin-token").Return(&authusecase.Claims{
			Subject: "operator-42", JTI: "session-42", Kind: authusecase.TokenKindAccess,
		}, nil).Once()
		server := New(Dependencies{OperatorTournamentReadLimiter: limiter})
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tournament-audit", nil)
		request.RemoteAddr = "198.51.100.42:1234"
		request.AddCookie(&http.Cookie{Name: middleware.AdminAccessCookieName, Value: "admin-token"})

		middleware.AdminSession(verifier)(server.tournamentRateGuard()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))).ServeHTTP(recorder, request)

		require.Equal(t, http.StatusNoContent, recorder.Code)
	})

	t.Run("participant uses player identity once", func(t *testing.T) {
		playerID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
		sessionID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
		limiter := middlewaremocks.NewMockRateLimiter(t)
		limiter.EXPECT().Allow("participant:" + playerID.String()).Return(true).Once()
		players := middlewaremocks.NewMockPlayerSessionReader(t)
		players.EXPECT().GetBySessionToken(mock.Anything, sessionID).Return(&domain.Player{ID: playerID}, nil).Once()
		server := New(Dependencies{ParticipantTournamentMutationLimiter: limiter})
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/tournaments/30000000-0000-0000-0000-000000000003/participant/waves/40000000-0000-0000-0000-000000000004/ready",
			nil,
		)
		request.RemoteAddr = "198.51.100.42:1234"
		request.AddCookie(&http.Cookie{Name: middleware.PlayerSessionCookieName, Value: sessionID.String()})

		middleware.PlayerSession(players)(server.tournamentRateGuard()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))).ServeHTTP(recorder, request)

		require.Equal(t, http.StatusNoContent, recorder.Code)
	})
}

func TestTournamentRateGuardFailsClosedForMissingProtectedPrincipal(t *testing.T) {
	t.Parallel()

	limiter := middlewaremocks.NewMockRateLimiter(t)
	limiter.EXPECT().RetryAfter().Return("60").Once()
	server := New(Dependencies{OperatorTournamentMutationLimiter: limiter})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/tournaments/10000000-0000-0000-0000-000000000001/actions",
		nil,
	)
	request.RemoteAddr = "198.51.100.42:1234"

	server.tournamentRateGuard()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("missing protected principal must not reach the application")
	})).ServeHTTP(recorder, request)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "60", recorder.Header().Get("Retry-After"))
	limiter.AssertNotCalled(t, "Allow", mock.Anything)
}

func TestTournamentRateGuardReturnsRetryAfterAfterOneBucketHit(t *testing.T) {
	t.Parallel()

	limiter := newOneRequestRateLimiter(t, "60")
	server := New(Dependencies{PublicTournamentReadLimiter: limiter})
	guard := server.tournamentRateGuard()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tournaments/10000000-0000-0000-0000-000000000001", nil)
		req.RemoteAddr = "198.51.100.42:1234"
		guard.ServeHTTP(recorder, req)
		return recorder
	}

	require.Equal(t, http.StatusNoContent, request().Code)
	second := request()
	require.Equal(t, http.StatusTooManyRequests, second.Code)
	require.Equal(t, "60", second.Header().Get("Retry-After"))
}
