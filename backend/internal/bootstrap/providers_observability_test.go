package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	rootws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestArenaObservabilityProviders(t *testing.T) {
	t.Parallel()

	t.Run("shares one registry with every observer consumer", func(t *testing.T) {
		t.Parallel()

		telemetry := provideArenaObservability(logkit.Noop())
		require.NotNil(t, telemetry.metrics)
		require.NotNil(t, telemetry.observer)

		require.NoError(t, observability.EmitArenaEvent(
			t.Context(),
			telemetry.observer,
			observability.ArenaEventInput{
				Event:         "arena.lifecycle.transition",
				Outcome:       observability.ArenaOutcomeSuccess,
				CorrelationID: "provider-correlation",
				TournamentID:  "provider-tournament",
				EntityKind:    "tournament",
				EntityID:      "provider-tournament",
				Stage:         "tournament_lifecycle",
				Transition:    "draft_to_registration",
				ReasonCode:    "transitioned",
				Revision:      1,
			},
		))

		server := provideRESTServerWithClock(
			nil, nil, nil, nil, nil, nil, nil, nil,
			restv1.HealthChecks{},
			provideClock(),
			nil,
			adminRefreshRateLimiter{},
			nil,
			leaderboardRateLimiter{},
			logkit.Noop(),
			telemetry,
		)
		response := httptest.NewRecorder()
		handler := provideHTTPHandler(
			&config.Config{},
			server,
			&rootws.Server{},
			nil,
			nil,
			restMiddlewareStack{},
			logkit.Noop(),
		)
		handler.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "/internal/metrics", nil),
		)
		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), `operation="lifecycle",outcome="success"`)
	})

	t.Run("health source fails closed and follows runtime shutdown", func(t *testing.T) {
		t.Parallel()

		runtime, cancel := context.WithCancel(t.Context())
		telemetry := provideArenaObservability(logkit.Noop())
		probe := provideArenaHealthSource(runtime, nil, provideClock(), telemetry)

		active := probe.ArenaHealth(t.Context())
		require.False(t, active.Healthy())
		require.Equal(t, observability.ArenaHealthStateFailed, active.Authority.Health)
		require.Equal(t, observability.ArenaHealthStateHealthy, active.Realtime.Health)

		cancel()
		stopped := probe.ArenaHealth(t.Context())
		require.Equal(t, observability.ArenaHealthStateFailed, stopped.Realtime.Health)
		require.Equal(t, observability.ArenaReadinessStateNotReady, stopped.Realtime.Readiness)
	})

	t.Run("duplicate application graphs own isolated registries", func(t *testing.T) {
		t.Parallel()

		first := provideArenaObservability(logkit.Noop())
		second := provideArenaObservability(logkit.Noop())

		require.NotSame(t, first.metrics, second.metrics)
		require.NotSame(t, first.metrics.Gatherer(), second.metrics.Gatherer())
	})
}
