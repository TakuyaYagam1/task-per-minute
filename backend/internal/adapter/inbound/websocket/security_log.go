package websocket

import (
	"net/http"
	"strings"

	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/requestmeta"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	wsSecurityOutcomeSuccess     = "success"
	wsSecurityOutcomeFailure     = "failure"
	wsSecurityOutcomeRateLimited = "rate_limited"
)

func (server *Server) logRequestSecurityEvent(r *http.Request, event, outcome string, fields logkit.Fields) {
	if server == nil || server.log == nil {
		return
	}

	merged := logkit.Fields{
		"event":   event,
		"outcome": outcome,
	}
	if r != nil {
		if requestID := requestmeta.RequestIDFromContext(r.Context()); requestID != "" {
			merged["request_id"] = requestID
		}
		if clientIP := server.resolveClientIP(r); clientIP != "" {
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
		server.log.Info("security event", merged)
	default:
		server.log.Warn("security event", merged)
	}
}

func wsAuthFailureFields(r *http.Request) logkit.Fields {
	return logkit.Fields{
		"error_code": string(domain.ErrorCodeInvalidSession),
		"reason":     wsAuthFailureReason(r),
	}
}

func wsAuthFailureReason(r *http.Request) string {
	if r == nil {
		return "missing_session"
	}
	if queryHasToken(r) {
		return "query_token_rejected"
	}
	if strings.TrimSpace(r.Header.Get("X-Session-Token")) != "" {
		return "header_token_rejected"
	}
	if hasBearerCredentialSubprotocol(r.Header.Values("Sec-WebSocket-Protocol")) {
		return "subprotocol_token_rejected"
	}
	if _, err := r.Cookie(requestmeta.PlayerSessionCookieName); err == nil {
		return "invalid_cookie"
	}
	return "missing_session"
}

func hasUnsafeSessionTokenTransport(r *http.Request) bool {
	if r == nil {
		return false
	}
	return queryHasToken(r) ||
		strings.TrimSpace(r.Header.Get("X-Session-Token")) != "" ||
		hasBearerCredentialSubprotocol(r.Header.Values("Sec-WebSocket-Protocol"))
}

func queryHasToken(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	_, ok := r.URL.Query()["token"]
	return ok
}

func hasBearerCredentialSubprotocol(values []string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(part)), "tpm.bearer.") {
				return true
			}
		}
	}
	return false
}
