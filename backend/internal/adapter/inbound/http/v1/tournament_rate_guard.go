package v1

import (
	"net/http"
	"strings"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type tournamentRequestClass string

const (
	tournamentPublicRead          tournamentRequestClass = "public-read"
	tournamentOperatorRead        tournamentRequestClass = "operator-read"
	tournamentOperatorMutation    tournamentRequestClass = "operator-mutation"
	tournamentParticipantRead     tournamentRequestClass = "participant-read"
	tournamentParticipantMutation tournamentRequestClass = "participant-mutation"
)

type tournamentRequestPolicy struct {
	class   tournamentRequestClass
	limiter requestRateLimiter
}

type tournamentRateRoute struct {
	method  string
	class   tournamentRequestClass
	pattern []string
}

var tournamentRateRoutes = []tournamentRateRoute{
	{http.MethodGet, tournamentOperatorRead, []string{"api", "v1", "admin", "tournament-audit"}},
	{http.MethodGet, tournamentOperatorRead, []string{"api", "v1", "admin", "tournaments"}},
	{http.MethodGet, tournamentOperatorRead, []string{"api", "v1", "admin", "tournament-content"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments"}},
	{http.MethodGet, tournamentOperatorRead, []string{"api", "v1", "admin", "tournaments", "*", "configuration"}},
	{http.MethodPatch, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "configuration"}},
	{http.MethodPatch, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "series", "*", "configuration"}},
	{http.MethodPut, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "swiss", "rounds", "*"}},
	{http.MethodGet, tournamentOperatorRead, []string{"api", "v1", "admin", "tournaments", "*", "roster"}},
	{http.MethodPut, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "roster"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "roster", "preflight"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "roster", "lock"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "roster", "unlock"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "pairings"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "actions"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "waves", "*", "actions"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "waves", "*", "no-shows"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "series", "*", "assignments", "*", "operator-reserves"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "series", "*", "operator-forfeits"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "series", "*", "games", "*", "replays"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "series", "*", "games", "*", "corrections"}},
	{http.MethodGet, tournamentOperatorRead, []string{"api", "v1", "admin", "tournaments", "*", "incident-export"}},
	{http.MethodGet, tournamentOperatorRead, []string{"api", "v1", "admin", "tournaments", "*", "snapshot"}},
	{http.MethodGet, tournamentOperatorRead, []string{"api", "v1", "admin", "tournaments", "*", "golden"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "golden", "open"}},
	{http.MethodPost, tournamentOperatorMutation, []string{"api", "v1", "admin", "tournaments", "*", "golden", "attempts", "*", "start"}},
	{http.MethodGet, tournamentParticipantRead, []string{"api", "v1", "tournaments", "*", "participant", "lobby"}},
	{http.MethodGet, tournamentParticipantRead, []string{"api", "v1", "tournaments", "*", "participant", "assignments", "*"}},
	{http.MethodPost, tournamentParticipantMutation, []string{"api", "v1", "tournaments", "*", "participant", "waves", "*", "ready"}},
	{http.MethodPost, tournamentParticipantMutation, []string{"api", "v1", "tournaments", "*", "participant", "series", "*", "draft", "actions"}},
	{http.MethodPost, tournamentParticipantMutation, []string{"api", "v1", "tournaments", "*", "participant", "series", "*", "games", "*", "submissions"}},
	{http.MethodPost, tournamentParticipantMutation, []string{"api", "v1", "tournaments", "*", "participant", "series", "*", "surrender"}},
	{http.MethodPost, tournamentParticipantMutation, []string{"api", "v1", "tournaments", "*", "participant", "series", "*", "post-series"}},
	{http.MethodGet, tournamentParticipantRead, []string{"api", "v1", "tournaments", "*", "participant", "snapshot"}},
	{http.MethodGet, tournamentParticipantRead, []string{"api", "v1", "tournaments", "*", "participant", "golden"}},
	{http.MethodPost, tournamentParticipantMutation, []string{"api", "v1", "tournaments", "*", "participant", "golden", "ready"}},
	{http.MethodPost, tournamentParticipantMutation, []string{"api", "v1", "tournaments", "*", "participant", "golden", "submissions"}},
	{http.MethodGet, tournamentPublicRead, []string{"api", "v1", "tournaments", "*"}},
	{http.MethodGet, tournamentPublicRead, []string{"api", "v1", "tournaments", "*", "scoreboard"}},
	{http.MethodGet, tournamentPublicRead, []string{"api", "v1", "tournaments", "*", "bracket"}},
	{http.MethodGet, tournamentPublicRead, []string{"api", "v1", "tournaments", "*", "live-draft"}},
	{http.MethodGet, tournamentPublicRead, []string{"api", "v1", "tournaments", "*", "snapshot"}},
}

// tournamentRateGuard applies one shared rate-limit bucket after request
// authentication and before a tournament handler can invoke an application.
func (s *Server) tournamentRateGuard() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			policy, ok := s.tournamentRatePolicy(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			scope, ok := tournamentRateScope(r, policy.class)
			if !ok || !tournamentRequestAllowed(policy.limiter, scope) {
				setRetryAfter(w, policy.limiter)
				s.logSecurityEvent(r, "tournament.rate."+string(policy.class), securityOutcomeRateLimited, nil)
				errmap.HandleError(w, r, domain.ErrRateLimited)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) tournamentRatePolicy(r *http.Request) (tournamentRequestPolicy, bool) {
	if s == nil || r == nil || r.URL == nil {
		return tournamentRequestPolicy{}, false
	}
	for _, route := range tournamentRateRoutes {
		if !route.matches(r.Method, r.URL.Path) {
			continue
		}
		return tournamentRequestPolicy{class: route.class, limiter: s.tournamentRateLimiter(route.class)}, true
	}
	return tournamentRequestPolicy{}, false
}

func (route tournamentRateRoute) matches(method, path string) bool {
	if path == "" || strings.HasSuffix(path, "/") ||
		(method != route.method && (route.method != http.MethodGet || method != http.MethodHead)) {
		return false
	}
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segments) != len(route.pattern) {
		return false
	}
	for index, expected := range route.pattern {
		if expected == "*" {
			if segments[index] == "" {
				return false
			}
			continue
		}
		if segments[index] != expected {
			return false
		}
	}
	return true
}

func (s *Server) tournamentRateLimiter(class tournamentRequestClass) requestRateLimiter {
	if s == nil {
		return nil
	}
	switch class {
	case tournamentPublicRead:
		return s.publicTournamentReadLimiter
	case tournamentOperatorRead:
		return s.operatorTournamentReadLimiter
	case tournamentOperatorMutation:
		return s.operatorTournamentMutationLimiter
	case tournamentParticipantRead:
		return s.participantTournamentReadLimiter
	case tournamentParticipantMutation:
		return s.participantTournamentMutationLimiter
	default:
		return nil
	}
}

func tournamentRateScope(r *http.Request, class tournamentRequestClass) (string, bool) {
	if r == nil {
		return "", false
	}
	switch class {
	case tournamentPublicRead:
		clientIP := strings.TrimSpace(middleware.ClientIPFromRequest(r))
		if clientIP == "" {
			return "", false
		}
		return "public:" + clientIP, true
	case tournamentOperatorRead, tournamentOperatorMutation:
		claims, ok := middleware.GetAdminClaimsFromCtx(r.Context())
		if !ok || claims == nil || strings.TrimSpace(claims.Subject) == "" {
			return "", false
		}
		return "operator:" + claims.Subject, true
	case tournamentParticipantRead, tournamentParticipantMutation:
		player, ok := middleware.GetPlayerFromCtx(r.Context())
		if !ok || player == nil || player.ID == [16]byte{} {
			return "", false
		}
		return "participant:" + player.ID.String(), true
	default:
		return "", false
	}
}

func tournamentRequestAllowed(limiter requestRateLimiter, scope string) bool {
	return limiter != nil && scope != "" && limiter.Allow(scope)
}
