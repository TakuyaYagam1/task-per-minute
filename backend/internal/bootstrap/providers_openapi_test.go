package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
)

func TestProvideRESTMiddlewares_IncludesOpenAPIValidation(t *testing.T) {
	t.Parallel()

	middlewares, err := provideRESTMiddlewares(
		context.Background(),
		logkit.Noop(),
		&config.Config{},
		requireEventTelemetry(t),
	)
	require.NoError(t, err)
	require.NotNil(t, middlewares.RequestValidator)
	require.Len(t, middlewares.Outer, 1)

	called := false
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	handler = middlewares.RequestValidator(handler)
	// Outer middleware is applied last, matching the generated server order.
	for _, wrap := range middlewares.Outer {
		handler = wrap(handler)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", strings.NewReader(`{"password":""}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	require.False(t, called)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.NotEmpty(t, rr.Header().Get("X-Request-ID"))
	require.Contains(t, rr.Body.String(), `"request_id"`)
}
