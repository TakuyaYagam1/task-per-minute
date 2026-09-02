package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArenaHealthReadiness(t *testing.T) {
	t.Parallel()

	t.Run("healthy snapshot is healthy and ready", func(t *testing.T) {
		t.Parallel()

		snapshot := HealthyArenaHealthSnapshot()

		require.NoError(t, snapshot.Validate())
		require.True(t, snapshot.Healthy())
		require.True(t, snapshot.Ready())
	})

	t.Run("degraded dependency remains available but is not healthy", func(t *testing.T) {
		t.Parallel()

		snapshot := HealthyArenaHealthSnapshot()
		snapshot.Submission.Health = ArenaHealthStateDegraded

		require.NoError(t, snapshot.Validate())
		require.False(t, snapshot.Healthy())
		require.True(t, snapshot.Ready())
	})

	t.Run("failed authority is neither healthy nor ready", func(t *testing.T) {
		t.Parallel()

		snapshot := HealthyArenaHealthSnapshot()
		snapshot.Authority = ArenaDependencyStatus{
			Health:    ArenaHealthStateFailed,
			Readiness: ArenaReadinessStateNotReady,
		}

		require.NoError(t, snapshot.Validate())
		require.False(t, snapshot.Healthy())
		require.False(t, snapshot.Ready())
	})

	t.Run("stale authoritative view blocks readiness", func(t *testing.T) {
		t.Parallel()

		snapshot := HealthyArenaHealthSnapshot()
		snapshot.Authority.Readiness = ArenaReadinessStateStale

		require.NoError(t, snapshot.Validate())
		require.False(t, snapshot.Ready())
	})

	t.Run("clock drift is reported independently", func(t *testing.T) {
		t.Parallel()

		snapshot := HealthyArenaHealthSnapshot()
		snapshot.Clock = ArenaDependencyStatus{
			Health:    ArenaHealthStateDegraded,
			Readiness: ArenaReadinessStateNotReady,
		}

		require.False(t, snapshot.Healthy())
		require.False(t, snapshot.Ready())
		require.Equal(t, ArenaHealthStateHealthy, snapshot.Authority.Health)
	})

	t.Run("recovered source becomes healthy and ready", func(t *testing.T) {
		t.Parallel()

		calls := 0
		source := ArenaHealthSourceFunc(func(context.Context) ArenaHealthSnapshot {
			calls++
			if calls == 1 {
				return FailedArenaHealthSnapshot()
			}
			return HealthyArenaHealthSnapshot()
		})

		before := source.ArenaHealth(t.Context())
		after := source.ArenaHealth(t.Context())

		require.False(t, before.Healthy())
		require.False(t, before.Ready())
		require.True(t, after.Healthy())
		require.True(t, after.Ready())
	})

	t.Run("unknown state is rejected", func(t *testing.T) {
		t.Parallel()

		snapshot := HealthyArenaHealthSnapshot()
		snapshot.Outbox.Health = ArenaHealthState("unknown")

		require.Error(t, snapshot.Validate())
		require.False(t, snapshot.Healthy())
	})
}
