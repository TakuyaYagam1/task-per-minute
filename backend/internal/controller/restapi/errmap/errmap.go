package errmap

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
)

const problemContentType = "application/problem+json"

// HandleError writes an RFC 7807 response for a domain/application error.
func HandleError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}

	status, app := classify(err)
	detail := app.Message
	instance := r.URL.Path
	requestID := middleware.GetRequestIDFromCtx(r.Context())

	problem := openapi.ProblemDetails{
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

func classify(err error) (int, *apperr.Error) {
	switch {
	case isAny(err, apperr.ErrPlayerNotFound, apperr.ErrTaskNotFound, apperr.ErrDuelNotFound):
		return http.StatusNotFound, appError(err, apperr.ErrInternal)
	case isAny(err, apperr.ErrInvalidCredentials, apperr.ErrTokenExpired, apperr.ErrTokenRevoked, apperr.ErrInvalidSession):
		return http.StatusUnauthorized, appError(err, apperr.ErrInternal)
	case errors.Is(err, apperr.ErrNotDuelParticipant):
		return http.StatusForbidden, appError(err, apperr.ErrInternal)
	case isAny(err, apperr.ErrUsernameTaken, apperr.ErrPlayerInDuel, apperr.ErrPlayerQueued, apperr.ErrTaskInUse, apperr.ErrDuelFinished, apperr.ErrConflict):
		return http.StatusConflict, appError(err, apperr.ErrInternal)
	case isAny(err, apperr.ErrFlagIncorrect, apperr.ErrDuelDeadlinePassed):
		return http.StatusUnprocessableEntity, appError(err, apperr.ErrInternal)
	case isAny(err, apperr.ErrValidation, apperr.ErrUsernameInvalid, apperr.ErrTaskValidation):
		return http.StatusBadRequest, appError(err, apperr.ErrInternal)
	case errors.Is(err, apperr.ErrRateLimited):
		return http.StatusTooManyRequests, appError(err, apperr.ErrInternal)
	case errors.Is(err, apperr.ErrInternal):
		return http.StatusInternalServerError, apperr.ErrInternal
	default:
		return http.StatusInternalServerError, apperr.ErrInternal
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

func appError(err error, fallback *apperr.Error) *apperr.Error {
	var app *apperr.Error
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
