package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestHealthReadiness(t *testing.T) {
	t.Parallel()

	checks := func(snapshot observability.TournamentHealthSnapshot) HealthChecks {
		return HealthChecks{
			DB: HealthCheckerFunc(func(context.Context) error {
				return nil
			}),
			Redis: HealthCheckerFunc(func(context.Context) error {
				return nil
			}),
			SeaweedFS: HealthCheckerFunc(func(context.Context) error {
				return nil
			}),
			SchemaVersion: SchemaVersionReaderFunc(func(context.Context) (int64, error) {
				return 1, nil
			}),
			Tournament: observability.TournamentHealthSourceFunc(
				func(context.Context) observability.TournamentHealthSnapshot {
					return snapshot
				},
			),
		}
	}

	t.Run("healthy dependencies are ready", func(t *testing.T) {
		t.Parallel()

		response := tournamentHealthResponse(t, checks(observability.HealthyTournamentHealthSnapshot()))

		require.Equal(t, http.StatusOK, response.Code)
		body := decodeHealthResponse(t, response)
		require.Equal(t, api.HealthResponseStatusOk, body.Status)
		require.Equal(t, api.DependencyStatusHealthHealthy, body.TournamentAuthority.Health)
		require.Equal(t, api.DependencyStatusReadinessReady, body.TournamentAuthority.Readiness)
		require.Equal(t, api.DependencyStatusHealthHealthy, body.TournamentOutbox.Health)
		require.Equal(t, api.DependencyStatusHealthHealthy, body.TournamentProjection.Health)
		require.Equal(t, api.DependencyStatusReadinessReady, body.TournamentRecovery.Readiness)
		require.NotContains(t, response.Body.String(), "pending_count")
		require.NotContains(t, response.Body.String(), "oldest_pending_at")
		require.NotContains(t, response.Body.String(), "observed_at")
	})

	t.Run("stale projection blocks readiness independently", func(t *testing.T) {
		t.Parallel()

		snapshot := observability.HealthyTournamentHealthSnapshot()
		snapshot.Projection = observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateDegraded,
			Readiness: observability.TournamentReadinessStateStale,
		}
		response := tournamentHealthResponse(t, checks(snapshot))

		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		body := decodeHealthResponse(t, response)
		require.Equal(t, api.DependencyStatusHealthDegraded, body.TournamentProjection.Health)
		require.Equal(t, api.DependencyStatusReadinessStale, body.TournamentProjection.Readiness)
	})

	t.Run("degraded component changes aggregate health", func(t *testing.T) {
		t.Parallel()

		snapshot := observability.HealthyTournamentHealthSnapshot()
		snapshot.Outbox.Health = observability.TournamentHealthStateDegraded
		response := tournamentHealthResponse(t, checks(snapshot))

		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		body := decodeHealthResponse(t, response)
		require.Equal(t, api.DependencyStatusHealthDegraded, body.TournamentOutbox.Health)
		require.Equal(t, api.DependencyStatusReadinessReady, body.TournamentOutbox.Readiness)
	})

	t.Run("stale authority blocks readiness independently", func(t *testing.T) {
		t.Parallel()

		snapshot := observability.HealthyTournamentHealthSnapshot()
		snapshot.Authority.Readiness = observability.TournamentReadinessStateStale
		response := tournamentHealthResponse(t, checks(snapshot))

		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		body := decodeHealthResponse(t, response)
		require.Equal(t, api.DependencyStatusHealthHealthy, body.TournamentAuthority.Health)
		require.Equal(t, api.DependencyStatusReadinessStale, body.TournamentAuthority.Readiness)
	})

	t.Run("missing or invalid source fails closed", func(t *testing.T) {
		t.Parallel()

		missing := tournamentHealthResponse(t, HealthChecks{})
		require.Equal(t, http.StatusServiceUnavailable, missing.Code)
		require.Equal(
			t,
			api.DependencyStatusHealthFailed,
			decodeHealthResponse(t, missing).TournamentAuthority.Health,
		)
		require.Equal(
			t,
			api.DependencyStatusHealthFailed,
			decodeHealthResponse(t, missing).TournamentProjection.Health,
		)

		invalid := observability.HealthyTournamentHealthSnapshot()
		invalid.Recovery.Health = observability.TournamentHealthState("unknown")
		response := tournamentHealthResponse(t, checks(invalid))
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		require.Equal(
			t,
			api.DependencyStatusHealthFailed,
			decodeHealthResponse(t, response).TournamentRecovery.Health,
		)

		invalid = observability.HealthyTournamentHealthSnapshot()
		invalid.Projection.Health = observability.TournamentHealthState("unknown")
		response = tournamentHealthResponse(t, checks(invalid))
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		require.Equal(
			t,
			api.DependencyStatusHealthFailed,
			decodeHealthResponse(t, response).TournamentProjection.Health,
		)
	})
}

func tournamentHealthResponse(t *testing.T, checks HealthChecks) *httptest.ResponseRecorder {
	t.Helper()
	server := New(Dependencies{Health: checks})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	server.HealthCheck(response, request)
	return response
}

func decodeHealthResponse(t *testing.T, response *httptest.ResponseRecorder) api.HealthResponse {
	t.Helper()
	var body api.HealthResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	return body
}
