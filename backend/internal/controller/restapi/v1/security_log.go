package v1

import (
	"errors"
	"fmt"
	"net/http"

	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

const (
	securityOutcomeSuccess     = "success"
	securityOutcomeFailure     = "failure"
	securityOutcomeRateLimited = "rate_limited"
)

func (s *Server) logSecurityEvent(r *http.Request, event, outcome string, fields logkit.Fields) {
	if s == nil || s.log == nil {
		return
	}

	merged := logkit.Fields{
		"event":   event,
		"outcome": outcome,
	}
	if requestID := middleware.GetRequestIDFromCtx(r.Context()); requestID != "" {
		merged["request_id"] = requestID
	}
	if clientIP := middleware.ClientIPFromRequest(r); clientIP != "" {
		merged["client_ip"] = clientIP
	}
	for key, value := range fields {
		if value != nil {
			merged[key] = value
		}
	}

	switch outcome {
	case securityOutcomeSuccess:
		s.log.Info("security event", merged)
	default:
		s.log.Warn("security event", merged)
	}
}

func securityErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var app *apperr.Error
	if errors.As(err, &app) && app != nil {
		return string(app.Code)
	}
	return string(apperr.CodeInternal)
}

func logkitFields(key string, value any) logkit.Fields {
	return logkit.Fields{key: value}
}

func adminSecurityFields(actor usecase.AdminActor, errorCode any) logkit.Fields {
	fields := logkit.Fields{}
	if actor.Subject != "" {
		fields["admin_subject"] = actor.Subject
	}
	if actor.JTI != "" {
		fields["admin_access_jti"] = actor.JTI
	}
	if errorCode != nil {
		code := fmt.Sprint(errorCode)
		if code != "" {
			fields["error_code"] = code
		}
	}
	return fields
}
