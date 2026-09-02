package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
)

func TestArenaHTTPSecurityMiddlewareComposition(t *testing.T) {
	t.Parallel()

	validator, err := middleware.OpenAPIRequestValidator(context.Background(), logkit.Noop())
	require.NoError(t, err)
	limits := middleware.NewArenaRequestLimits(middleware.ArenaLimitConfig{
		OperatorMutation: middleware.ArenaEndpointLimit{Requests: 10, Window: time.Hour},
	})
	handler := NewHandler(New(Dependencies{}), HandlerOptions{
		AdminAuth:        arenaAdminVerifier{},
		ArenaAuthorizer:  middleware.ArenaScopeAuthorizerFunc(func(context.Context, middleware.ArenaAccess) bool { return true }),
		ArenaLimits:      limits,
		RequestValidator: validator,
	})

	commandID := "c4dfc84f-a0fb-4f53-9959-1710653d905c"
	request := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/arena/operator/tournaments", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer access-token")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", commandID)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}

	invalid := request(`{"expected_projection_revision":0,"preset":"arena_v1","roster_size":8,"operator_id":"private"}`)
	require.Equal(t, http.StatusBadRequest, invalid.Code)
	require.NotContains(t, invalid.Body.String(), "private")

	accepted := request(`{"expected_projection_revision":0,"preset":"arena_v1","roster_size":8}`)
	require.Equal(t, http.StatusNotImplemented, accepted.Code)

	replayed := request(`{"expected_projection_revision":0,"preset":"arena_v1","roster_size":8}`)
	require.Equal(t, http.StatusNotImplemented, replayed.Code)
	require.NotContains(t, replayed.Body.String(), commandID)
}
