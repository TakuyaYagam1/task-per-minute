package errmap_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	httpkitmw "github.com/wahrwelt-kit/go-httpkit/httputil/middleware"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestHandleError_MapsAllSentinels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		code   domain.ErrorCode
		detail string
	}{
		{"player_not_found", domain.ErrPlayerNotFound, http.StatusNotFound, domain.ErrPlayerNotFound.Code, domain.ErrPlayerNotFound.Message},
		{"task_not_found", domain.ErrTaskNotFound, http.StatusNotFound, domain.ErrTaskNotFound.Code, domain.ErrTaskNotFound.Message},
		{"tournament_not_found", domain.ErrTournamentNotFound, http.StatusNotFound, domain.ErrTournamentNotFound.Code, domain.ErrTournamentNotFound.Message},
		{"tournament_projection_not_found", domain.ErrTournamentProjectionNotFound, http.StatusNotFound, domain.ErrTournamentProjectionNotFound.Code, domain.ErrTournamentProjectionNotFound.Message},
		{"avatar_not_found", domain.ErrAvatarNotFound, http.StatusNotFound, domain.ErrAvatarNotFound.Code, domain.ErrAvatarNotFound.Message},
		{"invalid_credentials", domain.ErrInvalidCredentials, http.StatusUnauthorized, domain.ErrInvalidCredentials.Code, domain.ErrInvalidCredentials.Message},
		{"email_unverified", domain.ErrEmailUnverified, http.StatusUnauthorized, domain.ErrEmailUnverified.Code, domain.ErrEmailUnverified.Message},
		{"email_already_verified", domain.ErrEmailAlreadyVerified, http.StatusConflict, domain.ErrEmailAlreadyVerified.Code, domain.ErrEmailAlreadyVerified.Message},
		{"token_expired", domain.ErrTokenExpired, http.StatusUnauthorized, domain.ErrTokenExpired.Code, domain.ErrTokenExpired.Message},
		{"token_revoked", domain.ErrTokenRevoked, http.StatusUnauthorized, domain.ErrTokenRevoked.Code, domain.ErrTokenRevoked.Message},
		{"invalid_session", domain.ErrInvalidSession, http.StatusUnauthorized, domain.ErrInvalidSession.Code, domain.ErrInvalidSession.Message},
		{"account_deleted", domain.ErrAccountDeleted, http.StatusUnauthorized, domain.ErrAccountDeleted.Code, domain.ErrAccountDeleted.Message},
		{"forbidden", domain.ErrForbidden, http.StatusForbidden, domain.ErrForbidden.Code, domain.ErrForbidden.Message},
		{"foreign_assignment", domain.ErrAssignmentParticipant, http.StatusForbidden, domain.ErrorCodeForbidden, domain.ErrForbidden.Message},
		{"username_taken", domain.ErrUsernameTaken, http.StatusConflict, domain.ErrUsernameTaken.Code, domain.ErrUsernameTaken.Message},
		{"email_taken", domain.ErrEmailTaken, http.StatusConflict, domain.ErrEmailTaken.Code, domain.ErrEmailTaken.Message},
		{"task_in_use", domain.ErrTaskInUse, http.StatusConflict, domain.ErrTaskInUse.Code, domain.ErrTaskInUse.Message},
		{"conflict", domain.ErrConflict, http.StatusConflict, domain.ErrConflict.Code, domain.ErrConflict.Message},
		{"invalid_content_configuration", domain.ErrInvalidContentConfiguration, http.StatusUnprocessableEntity, domain.ErrorCodeValidation, domain.ErrInvalidContentConfiguration.Error()},
		{"current_password_invalid", domain.ErrCurrentPasswordInvalid, http.StatusUnprocessableEntity, domain.ErrCurrentPasswordInvalid.Code, domain.ErrCurrentPasswordInvalid.Message},
		{"email_change_code_invalid", domain.ErrEmailChangeCodeInvalid, http.StatusUnprocessableEntity, domain.ErrEmailChangeCodeInvalid.Code, domain.ErrEmailChangeCodeInvalid.Message},
		{"validation", domain.ErrValidation, http.StatusBadRequest, domain.ErrValidation.Code, domain.ErrValidation.Message},
		{"username_invalid", domain.ErrUsernameInvalid, http.StatusBadRequest, domain.ErrUsernameInvalid.Code, domain.ErrUsernameInvalid.Message},
		{"task_validation", domain.ErrTaskValidation, http.StatusBadRequest, domain.ErrTaskValidation.Code, domain.ErrTaskValidation.Message},
		{"avatar_invalid", domain.ErrAvatarInvalid, http.StatusBadRequest, domain.ErrAvatarInvalid.Code, domain.ErrAvatarInvalid.Message},
		{"avatar_too_large", domain.ErrAvatarTooLarge, http.StatusRequestEntityTooLarge, domain.ErrAvatarTooLarge.Code, domain.ErrAvatarTooLarge.Message},
		{"avatar_unsupported_media_type", domain.ErrAvatarUnsupportedMediaType, http.StatusUnsupportedMediaType, domain.ErrAvatarUnsupportedMediaType.Code, domain.ErrAvatarUnsupportedMediaType.Message},
		{"rate_limited", domain.ErrRateLimited, http.StatusTooManyRequests, domain.ErrRateLimited.Code, domain.ErrRateLimited.Message},
		{"email_change_attempts_exceeded", domain.ErrEmailChangeAttemptsExceeded, http.StatusTooManyRequests, domain.ErrEmailChangeAttemptsExceeded.Code, domain.ErrEmailChangeAttemptsExceeded.Message},
		{"email_change_rate_limited", domain.ErrEmailChangeRateLimited, http.StatusTooManyRequests, domain.ErrEmailChangeRateLimited.Code, domain.ErrEmailChangeRateLimited.Message},
		{"avatar_busy", domain.ErrAvatarBusy, http.StatusTooManyRequests, domain.ErrAvatarBusy.Code, domain.ErrAvatarBusy.Message},
		{"email_change_unavailable", domain.ErrEmailChangeUnavailable, http.StatusServiceUnavailable, domain.ErrEmailChangeUnavailable.Code, domain.ErrEmailChangeUnavailable.Message},
		{"avatar_video_unavailable", domain.ErrAvatarVideoUnavailable, http.StatusServiceUnavailable, domain.ErrAvatarVideoUnavailable.Code, domain.ErrAvatarVideoUnavailable.Message},
		{"internal", domain.ErrInternal, http.StatusInternalServerError, domain.ErrInternal.Code, domain.ErrInternal.Message},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rr, problem := handle(t, tt.err)

			require.Equal(t, tt.status, rr.Code)
			require.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
			require.Equal(t, "about:blank", problem.Type)
			require.Equal(t, http.StatusText(tt.status), problem.Title)
			require.Equal(t, int32(tt.status), problem.Status)
			require.NotNil(t, problem.Code)
			require.Equal(t, string(tt.code), *problem.Code)
			require.NotNil(t, problem.Detail)
			require.Equal(t, tt.detail, *problem.Detail)
			require.NotNil(t, problem.Instance)
			require.Equal(t, "/api/v1/test", *problem.Instance)
			require.NotNil(t, problem.RequestId)
			require.Equal(t, "req-123", *problem.RequestId)
		})
	}
}

func TestHandleError_WrappedAppErrorKeepsSafeDetail(t *testing.T) {
	t.Parallel()

	cause := errors.New("db connection password leaked here")
	rr, problem := handle(t, domain.WrapError(cause, domain.ErrPlayerNotFound))

	require.Equal(t, http.StatusNotFound, rr.Code)
	require.NotNil(t, problem.Detail)
	require.Equal(t, domain.ErrPlayerNotFound.Message, *problem.Detail)
	require.NotContains(t, *problem.Detail, "password")
}

func TestHandleError_UnknownErrorMapsToInternal(t *testing.T) {
	t.Parallel()

	rr, problem := handle(t, fmt.Errorf("boom"))

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	require.Equal(t, "Internal Server Error", problem.Title)
	require.NotNil(t, problem.Detail)
	require.Equal(t, domain.ErrInternal.Message, *problem.Detail)
}

func TestHandleError_NilErrorDoesNotWrite(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)

	errmap.HandleError(rr, req, nil)

	require.Empty(t, rr.Body.String())
	require.Empty(t, rr.Header().Get("Content-Type"))
}

func handle(t *testing.T, err error) (*httptest.ResponseRecorder, api.ProblemDetails) {
	t.Helper()

	var problem api.ProblemDetails
	handler := httpkitmw.RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errmap.HandleError(w, r, err)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.Header.Set("X-Request-ID", "req-123")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &problem))
	return rr, problem
}
