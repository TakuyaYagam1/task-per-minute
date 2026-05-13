package websocket

import (
	"net/http"
	"strings"

	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	restmw "github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
)

const (
	wsSecurityOutcomeSuccess     = "success"
	wsSecurityOutcomeFailure     = "failure"
	wsSecurityOutcomeRateLimited = "rate_limited"
)

func (s *Server) logRequestSecurityEvent(r *http.Request, event, outcome string, fields logkit.Fields) {
	if s == nil || s.log == nil {
		return
	}

	merged := logkit.Fields{
		"event":   event,
		"outcome": outcome,
	}
	if r != nil {
		if requestID := restmw.GetRequestIDFromCtx(r.Context()); requestID != "" {
			merged["request_id"] = requestID
		}
		if clientIP := s.resolveClientIP(r); clientIP != "" {
			merged["client_ip"] = clientIP
		}
	}
	for key, value := range fields {
		if value != nil {
			merged[key] = value
		}
	}

	switch outcome {
	case wsSecurityOutcomeSuccess:
		s.log.Info("security event", merged)
	default:
		s.log.Warn("security event", merged)
	}
}

func (s *Server) logClientSecurityEvent(c *client, event, outcome string, fields logkit.Fields) {
	if s == nil || s.log == nil {
		return
	}

	merged := logkit.Fields{
		"event":   event,
		"outcome": outcome,
	}
	if c != nil {
		if c.player != nil {
			merged["player_id"] = c.player.ID.String()
		}
		if duelID, ok := c.currentDuel(); ok {
			merged["duel_id"] = duelID.String()
		}
	}
	for key, value := range fields {
		if value != nil {
			merged[key] = value
		}
	}

	switch outcome {
	case wsSecurityOutcomeSuccess:
		s.log.Info("security event", merged)
	default:
		s.log.Warn("security event", merged)
	}
}

func wsAuthFailureFields(r *http.Request) logkit.Fields {
	return logkit.Fields{
		"error_code": string(apperr.CodeInvalidSession),
		"reason":     wsAuthFailureReason(r),
	}
}

func wsAuthFailureReason(r *http.Request) string {
	if r == nil {
		return "missing_session"
	}
	if strings.TrimSpace(r.URL.Query().Get("token")) != "" {
		return "query_token_rejected"
	}
	if strings.TrimSpace(r.Header.Get("X-Session-Token")) != "" {
		return "header_token_rejected"
	}
	if hasLegacyBearerSubprotocol(r.Header.Values("Sec-WebSocket-Protocol")) {
		return "subprotocol_token_rejected"
	}
	if _, err := r.Cookie(restmw.PlayerSessionCookieName); err == nil {
		return "invalid_cookie"
	}
	return "missing_session"
}

func hasLegacyBearerSubprotocol(values []string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(part)), "tpm.bearer.") {
				return true
			}
		}
	}
	return false
}
