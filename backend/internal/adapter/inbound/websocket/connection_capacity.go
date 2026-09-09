package websocket

import (
	"net/http"
	"sync"

	"github.com/google/uuid"
	logkit "github.com/wahrwelt-kit/go-logkit"
)

type connectionLimit uint8

const (
	connectionAvailable connectionLimit = iota
	connectionServerUnavailable
	connectionGlobalLimit
	connectionPrincipalLimit
)

func (server *Server) reserveConnection(
	scope tournamentConnectionScope,
	principal tournamentConnectionPrincipal,
) (func(), connectionLimit) {
	if server == nil {
		return func() {}, connectionServerUnavailable
	}

	principalKey := connectionPrincipalKey(scope, principal)
	server.lifecycleMu.Lock()
	defer server.lifecycleMu.Unlock()
	if server.closing {
		return func() {}, connectionServerUnavailable
	}
	if server.active >= server.maxConnections {
		return func() {}, connectionGlobalLimit
	}
	if principalKey != "" && server.byPrincipal[principalKey] >= server.maxPerPrincipal {
		return func() {}, connectionPrincipalLimit
	}

	server.active++
	if principalKey != "" {
		server.byPrincipal[principalKey]++
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			server.releaseConnectionReservation(principalKey)
		})
	}, connectionAvailable
}

func (server *Server) releaseConnectionReservation(principalKey string) {
	server.lifecycleMu.Lock()
	defer server.lifecycleMu.Unlock()
	if server.active > 0 {
		server.active--
	}
	if principalKey == "" {
		return
	}
	remaining := server.byPrincipal[principalKey] - 1
	if remaining > 0 {
		server.byPrincipal[principalKey] = remaining
		return
	}
	delete(server.byPrincipal, principalKey)
}

func connectionPrincipalKey(
	scope tournamentConnectionScope,
	principal tournamentConnectionPrincipal,
) string {
	var principalID uuid.UUID
	switch scope.Role {
	case TournamentRoleParticipant:
		if principal.Player != nil {
			principalID = principal.Player.ID
		}
	case TournamentRoleOperator:
		if principal.OperatorSession != nil {
			principalID = principal.OperatorSession.Principal.PrincipalID
		}
	case TournamentRolePublic:
		return ""
	default:
		return ""
	}
	if principalID == uuid.Nil {
		return ""
	}
	return string(scope.Role) + ":" + principalID.String()
}

func (server *Server) writeConnectionLimitProblem(
	w http.ResponseWriter,
	r *http.Request,
	limit connectionLimit,
) {
	status := http.StatusServiceUnavailable
	message := "websocket capacity reached"
	reason := "global_connection_limit"
	switch limit {
	case connectionPrincipalLimit:
		status = http.StatusTooManyRequests
		message = "too many concurrent principal connections"
		reason = "principal_connection_limit"
	case connectionServerUnavailable:
		message = "websocket server is shutting down"
		reason = "server_shutdown"
	case connectionAvailable, connectionGlobalLimit:
	}
	server.logRequestSecurityEvent(r, "ws.capacity", wsSecurityOutcomeRateLimited, logkit.Fields{
		"error_code": reason,
		"reason":     reason,
	})
	writeHandshakeProblem(w, r, status, message)
}
