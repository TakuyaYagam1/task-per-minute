package websocket

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/requestmeta"
	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (server *Server) prepareTournamentConnection(
	w http.ResponseWriter,
	r *http.Request,
) (tournamentConnectionScope, tournamentConnectionPrincipal, tournamentConnectionResume, bool) {
	if server == nil || r == nil {
		writeHandshakeProblem(w, r, http.StatusServiceUnavailable, "websocket server unavailable")
		return tournamentConnectionScope{}, tournamentConnectionPrincipal{}, tournamentConnectionResume{}, false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeHandshakeProblem(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return tournamentConnectionScope{}, tournamentConnectionPrincipal{}, tournamentConnectionResume{}, false
	}
	if server.isClosing() {
		writeHandshakeProblem(w, r, http.StatusServiceUnavailable, "websocket server is shutting down")
		return tournamentConnectionScope{}, tournamentConnectionPrincipal{}, tournamentConnectionResume{}, false
	}
	if hasUnsafeSessionTokenTransport(r) || hasTournamentQueryCredential(r) {
		server.logRequestSecurityEvent(r, "ws.auth", wsSecurityOutcomeFailure, wsAuthFailureFields(r))
		writeHandshakeProblem(w, r, http.StatusUnauthorized, "unsupported credential transport")
		return tournamentConnectionScope{}, tournamentConnectionPrincipal{}, tournamentConnectionResume{}, false
	}
	resume, err := tournamentResumeFromRequest(r)
	if err != nil {
		server.logRequestSecurityEvent(r, "ws.handshake", wsSecurityOutcomeFailure, logkit.Fields{
			"error_code": "invalid_resume_id",
			"reason":     "invalid_resume_id",
		})
		writeHandshakeProblem(w, r, http.StatusBadRequest, "invalid realtime resume")
		return tournamentConnectionScope{}, tournamentConnectionPrincipal{}, tournamentConnectionResume{}, false
	}

	scope, ok := tournamentScopeFromRequest(r)
	if !ok {
		writeHandshakeProblem(w, r, http.StatusNotFound, "tournament websocket endpoint not found")
		return tournamentConnectionScope{}, tournamentConnectionPrincipal{}, tournamentConnectionResume{}, false
	}
	if server.handshakeLimiter != nil {
		ip := server.resolveClientIP(r)
		if !server.handshakeLimiter.Allow(ip) {
			if retry := server.handshakeLimiter.RetryAfter(); retry != "" {
				w.Header().Set("Retry-After", retry)
			}
			server.logRequestSecurityEvent(r, "ws.handshake", wsSecurityOutcomeRateLimited, logkit.Fields{
				"error_code": "rate_limited",
				"reason":     "handshake_rate_limit",
			})
			writeHandshakeProblem(w, r, http.StatusTooManyRequests, "too many handshake attempts")
			return tournamentConnectionScope{}, tournamentConnectionPrincipal{}, tournamentConnectionResume{}, false
		}
	}
	if !server.acceptsOrigin(r) {
		server.logRequestSecurityEvent(r, "ws.handshake", wsSecurityOutcomeFailure, logkit.Fields{
			"error_code": "origin_not_allowed",
			"reason":     "origin_not_allowed",
		})
		writeHandshakeProblem(w, r, http.StatusForbidden, "origin not allowed")
		return tournamentConnectionScope{}, tournamentConnectionPrincipal{}, tournamentConnectionResume{}, false
	}

	principal, ok := server.authenticateRole(w, r, scope)
	if !ok {
		return tournamentConnectionScope{}, tournamentConnectionPrincipal{}, tournamentConnectionResume{}, false
	}
	return scope, principal, resume, true
}

type tournamentConnectionScope struct {
	Role         TournamentRole
	TournamentID uuid.UUID
}

type tournamentConnectionPrincipal struct {
	Player             *domain.Player
	ParticipantSession uuid.UUID
	OperatorSession    *TournamentOperatorSession
}

type tournamentConnectionResume struct {
	ID uuid.UUID
}

func tournamentResumeFromRequest(r *http.Request) (tournamentConnectionResume, error) {
	if r == nil || r.URL == nil {
		return tournamentConnectionResume{}, errors.New("invalid request")
	}
	values, present := r.URL.Query()["resume_id"]
	if !present {
		return tournamentConnectionResume{}, nil
	}
	if len(values) != 1 || len(values[0]) != 36 || strings.TrimSpace(values[0]) != values[0] {
		return tournamentConnectionResume{}, errors.New("invalid resume id")
	}
	id, err := uuid.Parse(values[0])
	if err != nil || id == uuid.Nil {
		return tournamentConnectionResume{}, errors.New("invalid resume id")
	}
	return tournamentConnectionResume{ID: id}, nil
}

func tournamentScopeFromRequest(r *http.Request) (tournamentConnectionScope, bool) {
	if r == nil || r.URL == nil {
		return tournamentConnectionScope{}, false
	}

	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	role, tournamentIDSegment, ok := tournamentScopeSegments(segments)
	if !ok {
		return tournamentConnectionScope{}, false
	}
	tournamentID, err := uuid.Parse(tournamentIDSegment)
	if err != nil || tournamentID == uuid.Nil {
		return tournamentConnectionScope{}, false
	}
	return tournamentConnectionScope{Role: role, TournamentID: tournamentID}, true
}

func tournamentScopeSegments(segments []string) (TournamentRole, string, bool) {
	if publicTournamentScope(segments) {
		return TournamentRolePublic, segments[3], true
	}
	if participantTournamentScope(segments) {
		return TournamentRoleParticipant, segments[3], true
	}
	if operatorTournamentScope(segments) {
		return TournamentRoleOperator, segments[4], true
	}
	return "", "", false
}

func publicTournamentScope(segments []string) bool {
	return len(segments) == 5 && segments[0] == "api" && segments[1] == "v1" &&
		segments[2] == "tournaments" && segments[4] == "realtime"
}

func participantTournamentScope(segments []string) bool {
	return len(segments) == 6 && segments[0] == "api" && segments[1] == "v1" &&
		segments[2] == "tournaments" && segments[4] == "participant" && segments[5] == "realtime"
}

func operatorTournamentScope(segments []string) bool {
	return len(segments) == 6 && segments[0] == "api" && segments[1] == "v1" &&
		segments[2] == "admin" && segments[3] == "tournaments" && segments[5] == "realtime"
}

func hasTournamentQueryCredential(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	for key := range r.URL.Query() {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "access_token", "authorization", "credential", "password", "session_token", "token":
			return true
		}
	}
	return false
}

func (server *Server) authenticateRole(
	w http.ResponseWriter,
	r *http.Request,
	scope tournamentConnectionScope,
) (tournamentConnectionPrincipal, bool) {
	switch scope.Role {
	case TournamentRolePublic:
		return tournamentConnectionPrincipal{}, true
	case TournamentRoleParticipant:
		player, token, ok := server.authenticateParticipant(w, r)
		if !ok {
			return tournamentConnectionPrincipal{}, false
		}
		return tournamentConnectionPrincipal{Player: player, ParticipantSession: token}, true
	case TournamentRoleOperator:
		if server.operatorResolve == nil {
			writeHandshakeProblem(w, r, http.StatusUnauthorized, "operator authentication unavailable")
			return tournamentConnectionPrincipal{}, false
		}
		session, ok := server.operatorResolve(r, scope.TournamentID)
		if !ok || !validOperatorSession(session, scope.TournamentID, time.Now().UTC()) {
			server.logRequestSecurityEvent(r, "ws.auth", wsSecurityOutcomeFailure, logkit.Fields{
				"error_code": "invalid_operator_session",
				"reason":     "invalid_operator_session",
			})
			writeHandshakeProblem(w, r, http.StatusUnauthorized, "invalid operator session")
			return tournamentConnectionPrincipal{}, false
		}
		return tournamentConnectionPrincipal{OperatorSession: &session}, true
	default:
		writeHandshakeProblem(w, r, http.StatusBadRequest, "invalid tournament websocket role")
		return tournamentConnectionPrincipal{}, false
	}
}

func validOperatorSession(session TournamentOperatorSession, tournamentID uuid.UUID, now time.Time) bool {
	principal := session.Principal
	return principal.Authenticated && principal.PrincipalID != uuid.Nil &&
		principal.Role == tournamentws.OperatorRealtimeRole && principal.TournamentID == tournamentID &&
		!session.ExpiresAt.IsZero() && session.ExpiresAt.After(now) && session.Validate != nil
}

func (server *Server) authenticateParticipant(
	w http.ResponseWriter,
	r *http.Request,
) (*domain.Player, uuid.UUID, bool) {
	if server.players == nil {
		writeHandshakeProblem(w, r, http.StatusUnauthorized, "participant authentication unavailable")
		return nil, uuid.Nil, false
	}
	token, ok := requestmeta.PlayerSessionTokenFromRequest(r)
	if !ok {
		server.logRequestSecurityEvent(r, "ws.auth", wsSecurityOutcomeFailure, wsAuthFailureFields(r))
		writeHandshakeProblem(w, r, http.StatusUnauthorized, "missing participant session")
		return nil, uuid.Nil, false
	}
	player, err := server.players.GetBySessionToken(r.Context(), token)
	if err != nil || player == nil || player.ID == uuid.Nil || player.SessionToken == nil ||
		*player.SessionToken != token || player.SessionExpiresAt == nil || !player.SessionExpiresAt.After(time.Now().UTC()) {
		server.logRequestSecurityEvent(r, "ws.auth", wsSecurityOutcomeFailure, logkit.Fields{
			"error_code": string(domain.ErrorCodeInvalidSession),
			"reason":     "invalid_session",
		})
		writeHandshakeProblem(w, r, http.StatusUnauthorized, "invalid participant session")
		return nil, uuid.Nil, false
	}
	clone := *player
	sessionToken := *player.SessionToken
	sessionExpiresAt := *player.SessionExpiresAt
	clone.SessionToken = &sessionToken
	clone.SessionExpiresAt = &sessionExpiresAt
	return &clone, token, true
}
