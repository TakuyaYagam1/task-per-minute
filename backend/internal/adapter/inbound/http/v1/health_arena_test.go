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

func TestArenaHealthReadiness(t *testing.T) {
	t.Parallel()

	checks := func(snapshot observability.ArenaHealthSnapshot) HealthChecks {
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
			Arena: observability.ArenaHealthSourceFunc(
				func(context.Context) observability.ArenaHealthSnapshot {
					return snapshot
				},
			),
		}
	}

	t.Run("healthy dependencies are ready", func(t *testing.T) {
		t.Parallel()

		response := arenaHealthResponse(t, checks(observability.HealthyArenaHealthSnapshot()))

		require.Equal(t, http.StatusOK, response.Code)
		body := arenaDecodeHealthResponse(t, response)
		require.Equal(t, api.HealthResponseStatusOk, body.Status)
		require.Equal(t, api.ArenaDependencyStatusHealthHealthy, body.ArenaAuthority.Health)
		require.Equal(t, api.ArenaDependencyStatusReadinessReady, body.ArenaAuthority.Readiness)
	})

	t.Run("degraded component changes aggregate health", func(t *testing.T) {
		t.Parallel()

		snapshot := observability.HealthyArenaHealthSnapshot()
		snapshot.Submission.Health = observability.ArenaHealthStateDegraded
		response := arenaHealthResponse(t, checks(snapshot))

		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		body := arenaDecodeHealthResponse(t, response)
		require.Equal(t, api.ArenaDependencyStatusHealthDegraded, body.ArenaSubmission.Health)
		require.Equal(t, api.ArenaDependencyStatusReadinessReady, body.ArenaSubmission.Readiness)
	})

	t.Run("stale authority blocks readiness independently", func(t *testing.T) {
		t.Parallel()

		snapshot := observability.HealthyArenaHealthSnapshot()
		snapshot.Authority.Readiness = observability.ArenaReadinessStateStale
		response := arenaHealthResponse(t, checks(snapshot))

		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		body := arenaDecodeHealthResponse(t, response)
		require.Equal(t, api.ArenaDependencyStatusHealthHealthy, body.ArenaAuthority.Health)
		require.Equal(t, api.ArenaDependencyStatusReadinessStale, body.ArenaAuthority.Readiness)
	})

	t.Run("missing or invalid source fails closed", func(t *testing.T) {
		t.Parallel()

		missing := arenaHealthResponse(t, HealthChecks{})
		require.Equal(t, http.StatusServiceUnavailable, missing.Code)
		require.Equal(
			t,
			api.ArenaDependencyStatusHealthFailed,
			arenaDecodeHealthResponse(t, missing).ArenaAuthority.Health,
		)

		invalid := observability.HealthyArenaHealthSnapshot()
		invalid.Recovery.Health = observability.ArenaHealthState("unknown")
		response := arenaHealthResponse(t, checks(invalid))
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		require.Equal(
			t,
			api.ArenaDependencyStatusHealthFailed,
			arenaDecodeHealthResponse(t, response).ArenaRecovery.Health,
		)
	})
}

func arenaHealthResponse(t *testing.T, checks HealthChecks) *httptest.ResponseRecorder {
	t.Helper()
	server := New(Dependencies{Health: checks})
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	server.HealthCheck(response, request)
	return response
}

func arenaDecodeHealthResponse(t *testing.T, response *httptest.ResponseRecorder) api.HealthResponse {
	t.Helper()
	var body api.HealthResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	return body
}
