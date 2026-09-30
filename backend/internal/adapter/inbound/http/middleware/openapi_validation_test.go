package middleware_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
)

func TestOpenAPIRequestValidator_RejectsInvalidBodyWithoutLeakingIt(t *testing.T) {
	t.Parallel()

	var logs lockedBuffer
	validator, err := middleware.OpenAPIRequestValidator(context.Background(), newTestLogger(t, &logs))
	require.NoError(t, err)

	called := false
	handler := validator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	secret := "sensitive-value-" + strings.Repeat("x", 300)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"`+secret+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.False(t, called)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Equal(t, "application/problem+json", rr.Header().Get("Content-Type"))
	require.JSONEq(t, `{
		"type":"about:blank",
		"title":"Bad Request",
		"status":400,
		"detail":"invalid request",
		"instance":"/api/v1/admin/login"
	}`, rr.Body.String())
	require.NotContains(t, rr.Body.String(), secret)
	require.NotContains(t, logs.String(), secret)
}

func TestOpenAPIRequestValidator_RejectsOversizedBody(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	handler := validator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	payload := `{"password":"` + strings.Repeat("x", 1<<20) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
	require.JSONEq(t, `{
		"type":"about:blank",
		"title":"Request Entity Too Large",
		"status":413,
		"detail":"invalid request",
		"instance":"/api/v1/admin/login"
	}`, rr.Body.String())
}

func TestOpenAPIRequestValidator_RejectsUnsupportedMediaType(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	handler := validator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"valid-password"}`))
	req.Header.Set("Content-Type", "text/plain")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
	require.JSONEq(t, `{
		"type":"about:blank",
		"title":"Unsupported Media Type",
		"status":415,
		"detail":"invalid request",
		"instance":"/api/v1/admin/login"
	}`, rr.Body.String())
}

func TestOpenAPIRequestValidator_RejectsMissingRequiredBodyAsBadRequest(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	handler := validator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/players/register", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestOpenAPIRequestValidator_RejectsStructuredJSONMediaTypeNotDeclaredByContract(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	handler := validator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":"valid-password"}`))
	req.Header.Set("Content-Type", "application/merge-patch+json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
}

func TestOpenAPIRequestValidator_PreservesValidBodyForHandler(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	const payload = `{"password":"valid-password"}`
	handler := validator(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		require.NoError(t, readErr)
		require.JSONEq(t, payload, string(body))
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestOpenAPIRequestValidator_AcceptsCaseInsensitiveJSONMediaType(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	const payload = `{"password":"valid-password"}`
	handler := validator(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/json; charset=UTF-8", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(payload))
	req.Header.Set("Content-Type", "Application/JSON; Charset=UTF-8")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestOpenAPIRequestValidator_DoesNotReplaceApplicationAuth(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	called := false
	handler := validator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/players", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.True(t, called)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestOpenAPIRequestValidator_EnforcesQueryConstraints(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	called := false
	handler := validator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/players/2c754c2e-8458-4417-b049-44c5f92840c7/audit?limit=201",
		nil,
	)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.False(t, called)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestOpenAPIRequestValidator_DoesNotBufferTaskSourceUpload(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	body := &countingErrorReader{}
	handler := validator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/tasks/2c754c2e-8458-4417-b049-44c5f92840c7/source",
		body,
	)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test-boundary")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	require.Zero(t, body.reads, "OpenAPI validation must not buffer the streaming upload")
}

func TestOpenAPIRequestValidator_DoesNotBypassNearMatchTaskSourcePaths(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	tests := []struct {
		name string
		path string
		want int
	}{
		{
			name: "invalid task id",
			path: "/api/v1/admin/tasks/not-a-uuid/source",
			want: http.StatusBadRequest,
		},
		{
			name: "extra path segment",
			path: "/api/v1/admin/tasks/2c754c2e-8458-4417-b049-44c5f92840c7/source/extra",
			want: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			handler := validator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader("multipart body"))
			req.Header.Set("Content-Type", "multipart/form-data; boundary=test-boundary")
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			require.False(t, called)
			require.Equal(t, tt.want, rr.Code)
		})
	}
}

func TestOpenAPIRequestValidator_BypassesOnlyTournamentRealtimePaths(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)

	const tournamentID = "2c754c2e-8458-4417-b049-44c5f92840c7"
	tests := []struct {
		name       string
		path       string
		wantCalled bool
	}{
		{name: "public", path: "/api/v1/tournaments/" + tournamentID + "/realtime", wantCalled: true},
		{name: "participant", path: "/api/v1/tournaments/" + tournamentID + "/participant/realtime", wantCalled: true},
		{name: "operator", path: "/api/v1/admin/tournaments/" + tournamentID + "/realtime", wantCalled: true},
		{name: "legacy root", path: "/ws"},
		{name: "invalid id", path: "/api/v1/tournaments/not-a-uuid/realtime"},
		{name: "extra segment", path: "/api/v1/tournaments/" + tournamentID + "/realtime/extra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			called := false
			handler := validator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tt.path, nil))

			require.Equal(t, tt.wantCalled, called)
			if tt.wantCalled {
				require.Equal(t, http.StatusNoContent, rr.Code)
			} else {
				require.Equal(t, http.StatusNotFound, rr.Code)
			}
		})
	}
}

type countingErrorReader struct {
	reads int
}

func (r *countingErrorReader) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("body must not be read by OpenAPI validation")
}
