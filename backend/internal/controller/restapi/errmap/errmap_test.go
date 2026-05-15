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

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
)

func TestHandleError_MapsAllSentinels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		detail string
	}{
		{"player_not_found", apperr.ErrPlayerNotFound, http.StatusNotFound, apperr.ErrPlayerNotFound.Message},
		{"task_not_found", apperr.ErrTaskNotFound, http.StatusNotFound, apperr.ErrTaskNotFound.Message},
		{"duel_not_found", apperr.ErrDuelNotFound, http.StatusNotFound, apperr.ErrDuelNotFound.Message},
		{"invalid_credentials", apperr.ErrInvalidCredentials, http.StatusUnauthorized, apperr.ErrInvalidCredentials.Message},
		{"token_expired", apperr.ErrTokenExpired, http.StatusUnauthorized, apperr.ErrTokenExpired.Message},
		{"token_revoked", apperr.ErrTokenRevoked, http.StatusUnauthorized, apperr.ErrTokenRevoked.Message},
		{"invalid_session", apperr.ErrInvalidSession, http.StatusUnauthorized, apperr.ErrInvalidSession.Message},
		{"not_duel_participant", apperr.ErrNotDuelParticipant, http.StatusForbidden, apperr.ErrNotDuelParticipant.Message},
		{"username_taken", apperr.ErrUsernameTaken, http.StatusConflict, apperr.ErrUsernameTaken.Message},
		{"player_in_duel", apperr.ErrPlayerInDuel, http.StatusConflict, apperr.ErrPlayerInDuel.Message},
		{"player_queued", apperr.ErrPlayerQueued, http.StatusConflict, apperr.ErrPlayerQueued.Message},
		{"task_in_use", apperr.ErrTaskInUse, http.StatusConflict, apperr.ErrTaskInUse.Message},
		{"duel_finished", apperr.ErrDuelFinished, http.StatusConflict, apperr.ErrDuelFinished.Message},
		{"conflict", apperr.ErrConflict, http.StatusConflict, apperr.ErrConflict.Message},
		{"flag_incorrect", apperr.ErrFlagIncorrect, http.StatusUnprocessableEntity, apperr.ErrFlagIncorrect.Message},
		{"duel_deadline_passed", apperr.ErrDuelDeadlinePassed, http.StatusUnprocessableEntity, apperr.ErrDuelDeadlinePassed.Message},
		{"validation", apperr.ErrValidation, http.StatusBadRequest, apperr.ErrValidation.Message},
		{"username_invalid", apperr.ErrUsernameInvalid, http.StatusBadRequest, apperr.ErrUsernameInvalid.Message},
		{"task_validation", apperr.ErrTaskValidation, http.StatusBadRequest, apperr.ErrTaskValidation.Message},
		{"rate_limited", apperr.ErrRateLimited, http.StatusTooManyRequests, apperr.ErrRateLimited.Message},
		{"internal", apperr.ErrInternal, http.StatusInternalServerError, apperr.ErrInternal.Message},
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
	rr, problem := handle(t, apperr.Wrap(cause, apperr.ErrPlayerNotFound))

	require.Equal(t, http.StatusNotFound, rr.Code)
	require.NotNil(t, problem.Detail)
	require.Equal(t, apperr.ErrPlayerNotFound.Message, *problem.Detail)
	require.NotContains(t, *problem.Detail, "password")
}

func TestHandleError_UnknownErrorMapsToInternal(t *testing.T) {
	t.Parallel()

	rr, problem := handle(t, fmt.Errorf("boom"))

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	require.Equal(t, "Internal Server Error", problem.Title)
	require.NotNil(t, problem.Detail)
	require.Equal(t, apperr.ErrInternal.Message, *problem.Detail)
}

func TestHandleError_NilErrorDoesNotWrite(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)

	errmap.HandleError(rr, req, nil)

	require.Empty(t, rr.Body.String())
	require.Empty(t, rr.Header().Get("Content-Type"))
}

func handle(t *testing.T, err error) (*httptest.ResponseRecorder, openapi.ProblemDetails) {
	t.Helper()

	var problem openapi.ProblemDetails
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
