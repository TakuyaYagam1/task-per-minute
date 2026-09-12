package errmap

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const problemContentType = "application/problem+json"

var errInvalidContentConfigurationProblem = &domain.Error{
	Code:    domain.ErrorCodeValidation,
	Message: domain.ErrInvalidContentConfiguration.Error(),
}

// HandleError writes an RFC 7807 response for a domain/application error.
func HandleError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}

	status, app := classify(err)
	detail := app.Message
	instance := r.URL.Path
	requestID := middleware.GetRequestIDFromCtx(r.Context())

	problem := api.ProblemDetails{
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    problemStatus(status),
		Detail:    &detail,
		Instance:  &instance,
		RequestId: &requestID,
	}

	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem)
}

func classify(err error) (int, *domain.Error) {
	switch {
	case isAny(
		err,
		domain.ErrPlayerNotFound,
		domain.ErrTaskNotFound,
		domain.ErrTournamentNotFound,
		domain.ErrTournamentProjectionNotFound,
	):
		return http.StatusNotFound, appError(err, domain.ErrInternal)
	case isAny(err, domain.ErrInvalidCredentials, domain.ErrTokenExpired, domain.ErrTokenRevoked, domain.ErrInvalidSession):
		return http.StatusUnauthorized, appError(err, domain.ErrInternal)
	case isAny(err, domain.ErrForbidden, domain.ErrAssignmentParticipant):
		return http.StatusForbidden, appError(err, domain.ErrForbidden)
	case isAny(err, domain.ErrUsernameTaken, domain.ErrTaskInUse, domain.ErrConflict):
		return http.StatusConflict, appError(err, domain.ErrInternal)
	case errors.Is(err, domain.ErrInvalidContentConfiguration):
		return http.StatusUnprocessableEntity, errInvalidContentConfigurationProblem
	case isAny(err, domain.ErrValidation, domain.ErrUsernameInvalid, domain.ErrTaskValidation):
		return http.StatusBadRequest, appError(err, domain.ErrInternal)
	case errors.Is(err, domain.ErrRateLimited):
		return http.StatusTooManyRequests, appError(err, domain.ErrInternal)
	case errors.Is(err, domain.ErrInternal):
		return http.StatusInternalServerError, domain.ErrInternal
	default:
		return http.StatusInternalServerError, domain.ErrInternal
	}
}

func isAny(err error, targets ...error) bool {
	for _, target := range targets {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

func appError(err error, fallback *domain.Error) *domain.Error {
	var app *domain.Error
	if errors.As(err, &app) {
		return app
	}
	return fallback
}

func problemStatus(status int) int32 {
	if status < http.StatusBadRequest || status > http.StatusNetworkAuthenticationRequired {
		return http.StatusInternalServerError
	}
	return int32(status)
}
