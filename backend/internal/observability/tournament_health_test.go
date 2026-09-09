package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTournamentHealthReadiness(t *testing.T) {
	t.Parallel()

	t.Run("healthy snapshot is healthy and ready", func(t *testing.T) {
		t.Parallel()

		snapshot := HealthyTournamentHealthSnapshot()

		require.NoError(t, snapshot.Validate())
		require.True(t, snapshot.Healthy())
		require.True(t, snapshot.Ready())
	})

	t.Run("degraded dependency remains available but is not healthy", func(t *testing.T) {
		t.Parallel()

		snapshot := HealthyTournamentHealthSnapshot()
		snapshot.Realtime.Health = TournamentHealthStateDegraded

		require.NoError(t, snapshot.Validate())
		require.False(t, snapshot.Healthy())
		require.True(t, snapshot.Ready())
	})

	t.Run("failed authority is neither healthy nor ready", func(t *testing.T) {
		t.Parallel()

		snapshot := HealthyTournamentHealthSnapshot()
		snapshot.Authority = TournamentDependencyStatus{
			Health:    TournamentHealthStateFailed,
			Readiness: TournamentReadinessStateNotReady,
		}

		require.NoError(t, snapshot.Validate())
		require.False(t, snapshot.Healthy())
		require.False(t, snapshot.Ready())
	})

	t.Run("stale authoritative view blocks readiness", func(t *testing.T) {
		t.Parallel()

		snapshot := HealthyTournamentHealthSnapshot()
		snapshot.Authority.Readiness = TournamentReadinessStateStale

		require.NoError(t, snapshot.Validate())
		require.False(t, snapshot.Ready())
	})

	t.Run("clock drift is reported independently", func(t *testing.T) {
		t.Parallel()

		snapshot := HealthyTournamentHealthSnapshot()
		snapshot.Clock = TournamentDependencyStatus{
			Health:    TournamentHealthStateDegraded,
			Readiness: TournamentReadinessStateNotReady,
		}

		require.False(t, snapshot.Healthy())
		require.False(t, snapshot.Ready())
		require.Equal(t, TournamentHealthStateHealthy, snapshot.Authority.Health)
	})

	t.Run("projection freshness blocks readiness independently", func(t *testing.T) {
		t.Parallel()

		snapshot := HealthyTournamentHealthSnapshot()
		snapshot.Projection = TournamentDependencyStatus{
			Health:    TournamentHealthStateDegraded,
			Readiness: TournamentReadinessStateStale,
		}

		require.NoError(t, snapshot.Validate())
		require.False(t, snapshot.Healthy())
		require.False(t, snapshot.Ready())
		require.Equal(t, TournamentHealthStateHealthy, snapshot.Realtime.Health)
	})

	t.Run("recovered source becomes healthy and ready", func(t *testing.T) {
		t.Parallel()

		calls := 0
		source := TournamentHealthSourceFunc(func(context.Context) TournamentHealthSnapshot {
			calls++
			if calls == 1 {
				return FailedTournamentHealthSnapshot()
			}
			return HealthyTournamentHealthSnapshot()
		})

		before := source.TournamentHealth(t.Context())
		after := source.TournamentHealth(t.Context())

		require.False(t, before.Healthy())
		require.False(t, before.Ready())
		require.True(t, after.Healthy())
		require.True(t, after.Ready())
	})

	t.Run("unknown state is rejected", func(t *testing.T) {
		t.Parallel()

		snapshot := HealthyTournamentHealthSnapshot()
		snapshot.Realtime.Health = TournamentHealthState("unknown")

		require.Error(t, snapshot.Validate())
		require.False(t, snapshot.Healthy())
	})
}
