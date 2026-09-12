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
		detail string
	}{
		{"player_not_found", domain.ErrPlayerNotFound, http.StatusNotFound, domain.ErrPlayerNotFound.Message},
		{"task_not_found", domain.ErrTaskNotFound, http.StatusNotFound, domain.ErrTaskNotFound.Message},
		{"tournament_not_found", domain.ErrTournamentNotFound, http.StatusNotFound, domain.ErrTournamentNotFound.Message},
		{"tournament_projection_not_found", domain.ErrTournamentProjectionNotFound, http.StatusNotFound, domain.ErrTournamentProjectionNotFound.Message},
		{"invalid_credentials", domain.ErrInvalidCredentials, http.StatusUnauthorized, domain.ErrInvalidCredentials.Message},
		{"token_expired", domain.ErrTokenExpired, http.StatusUnauthorized, domain.ErrTokenExpired.Message},
		{"token_revoked", domain.ErrTokenRevoked, http.StatusUnauthorized, domain.ErrTokenRevoked.Message},
		{"invalid_session", domain.ErrInvalidSession, http.StatusUnauthorized, domain.ErrInvalidSession.Message},
		{"forbidden", domain.ErrForbidden, http.StatusForbidden, domain.ErrForbidden.Message},
		{"foreign_assignment", domain.ErrAssignmentParticipant, http.StatusForbidden, domain.ErrForbidden.Message},
		{"username_taken", domain.ErrUsernameTaken, http.StatusConflict, domain.ErrUsernameTaken.Message},
		{"task_in_use", domain.ErrTaskInUse, http.StatusConflict, domain.ErrTaskInUse.Message},
		{"conflict", domain.ErrConflict, http.StatusConflict, domain.ErrConflict.Message},
		{"invalid_content_configuration", domain.ErrInvalidContentConfiguration, http.StatusUnprocessableEntity, domain.ErrInvalidContentConfiguration.Error()},
		{"validation", domain.ErrValidation, http.StatusBadRequest, domain.ErrValidation.Message},
		{"username_invalid", domain.ErrUsernameInvalid, http.StatusBadRequest, domain.ErrUsernameInvalid.Message},
		{"task_validation", domain.ErrTaskValidation, http.StatusBadRequest, domain.ErrTaskValidation.Message},
		{"rate_limited", domain.ErrRateLimited, http.StatusTooManyRequests, domain.ErrRateLimited.Message},
		{"internal", domain.ErrInternal, http.StatusInternalServerError, domain.ErrInternal.Message},
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
