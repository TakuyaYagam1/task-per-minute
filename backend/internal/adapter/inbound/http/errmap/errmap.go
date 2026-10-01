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
	code := string(app.Code)
	instance := r.URL.Path
	requestID := middleware.GetRequestIDFromCtx(r.Context())

	problem := api.ProblemDetails{
		Code:      &code,
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

type errorMapping struct {
	status   int
	targets  []error
	fallback *domain.Error
	problem  *domain.Error
}

var errorMappings = []errorMapping{
	{
		status: http.StatusNotFound,
		targets: []error{
			domain.ErrPlayerNotFound,
			domain.ErrTaskNotFound,
			domain.ErrTournamentNotFound,
			domain.ErrTournamentProjectionNotFound,
			domain.ErrAvatarNotFound,
		},
		fallback: domain.ErrInternal,
	},
	{
		status: http.StatusUnauthorized,
		targets: []error{
			domain.ErrInvalidCredentials,
			domain.ErrTokenExpired,
			domain.ErrTokenRevoked,
			domain.ErrInvalidSession,
			domain.ErrEmailUnverified,
			domain.ErrAccountDeleted,
		},
		fallback: domain.ErrInternal,
	},
	{status: http.StatusGone, targets: []error{domain.ErrPlayerJoinRetired}, fallback: domain.ErrInternal},
	{status: http.StatusForbidden, targets: []error{domain.ErrForbidden, domain.ErrAssignmentParticipant}, fallback: domain.ErrForbidden},
	{
		status: http.StatusConflict,
		targets: []error{
			domain.ErrUsernameTaken,
			domain.ErrTaskInUse,
			domain.ErrConflict,
			domain.ErrEmailAlreadyVerified,
			domain.ErrEmailTaken,
		},
		fallback: domain.ErrInternal,
	},
	{status: http.StatusRequestEntityTooLarge, targets: []error{domain.ErrAvatarTooLarge}, fallback: domain.ErrInternal},
	{status: http.StatusUnsupportedMediaType, targets: []error{domain.ErrAvatarUnsupportedMediaType}, fallback: domain.ErrInternal},
	{
		status:   http.StatusUnprocessableEntity,
		targets:  []error{domain.ErrCurrentPasswordInvalid, domain.ErrEmailChangeCodeInvalid},
		fallback: domain.ErrInternal,
	},
	{status: http.StatusUnprocessableEntity, targets: []error{domain.ErrInvalidContentConfiguration}, problem: errInvalidContentConfigurationProblem},
	{
		status:   http.StatusBadRequest,
		targets:  []error{domain.ErrValidation, domain.ErrUsernameInvalid, domain.ErrTaskValidation, domain.ErrAvatarInvalid},
		fallback: domain.ErrInternal,
	},
	{status: http.StatusBadRequest, targets: []error{domain.ErrVerificationTokenInvalid}, fallback: domain.ErrInternal},
	{status: http.StatusServiceUnavailable, targets: []error{domain.ErrVerificationUnavailable}, fallback: domain.ErrInternal},
	{
		status: http.StatusTooManyRequests,
		targets: []error{
			domain.ErrRateLimited,
			domain.ErrEmailChangeAttemptsExceeded,
			domain.ErrEmailChangeRateLimited,
			domain.ErrAvatarBusy,
		},
		fallback: domain.ErrInternal,
	},
	{
		status:   http.StatusServiceUnavailable,
		targets:  []error{domain.ErrEmailChangeUnavailable, domain.ErrAvatarVideoUnavailable},
		fallback: domain.ErrInternal,
	},
	{status: http.StatusInternalServerError, targets: []error{domain.ErrInternal}, fallback: domain.ErrInternal},
}

func classify(err error) (int, *domain.Error) {
	for _, mapping := range errorMappings {
		if !isAny(err, mapping.targets...) {
			continue
		}
		if mapping.problem != nil {
			return mapping.status, mapping.problem
		}
		return mapping.status, appError(err, mapping.fallback)
	}
	return http.StatusInternalServerError, domain.ErrInternal
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
