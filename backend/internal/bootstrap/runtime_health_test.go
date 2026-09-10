package bootstrap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func TestFailedRuntimeDependencyFailsClosed(t *testing.T) {
	status := failedRuntimeDependency()

	require.Equal(t, observability.TournamentHealthStateFailed, status.Health)
	require.Equal(t, observability.TournamentReadinessStateNotReady, status.Readiness)
}

func TestProjectionLagHealthStatusUsesDatabaseObservation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 16, 10, 0, 0, time.UTC)
	tests := []struct {
		name     string
		snapshot observability.ProjectionHealthSnapshot
		want     observability.TournamentDependencyStatus
	}{
		{
			name:     "no pending revision",
			snapshot: observability.ProjectionHealthSnapshot{ObservedAt: now},
			want:     healthyTournamentDependency(),
		},
		{
			name: "lagged revision is stale",
			snapshot: observability.ProjectionHealthSnapshot{
				PendingCount: 1, OldestPendingAt: backlogTime(now.Add(-runtimeProjectionLagDegradedAge)), ObservedAt: now,
			},
			want: observability.TournamentDependencyStatus{
				Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
			},
		},
		{
			name: "lagged revision fails after reviewed threshold",
			snapshot: observability.ProjectionHealthSnapshot{
				PendingCount: 1, OldestPendingAt: backlogTime(now.Add(-runtimeProjectionLagFailedAge)), ObservedAt: now,
			},
			want: failedTournamentDependency(),
		},
		{
			name: "future database row fails closed",
			snapshot: observability.ProjectionHealthSnapshot{
				PendingCount: 1, OldestPendingAt: backlogTime(now.Add(time.Second)), ObservedAt: now,
			},
			want: failedTournamentDependency(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.want, projectionLagHealthStatus(test.snapshot))
		})
	}
}

func TestExecutionRecoveryHealthStatusRequiresFreshCompletion(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 16, 45, 0, 0, time.UTC)
	tests := []struct {
		name   string
		health gameusecase.RecoveryRunnerHealth
		want   observability.TournamentDependencyStatus
	}{
		{name: "not running", want: failedTournamentDependency()},
		{name: "initial scan pending", health: gameusecase.RecoveryRunnerHealth{
			Running: true,
		}, want: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateNotReady,
		}},
		{name: "failed scan", health: gameusecase.RecoveryRunnerHealth{
			Running: true, InitialScanComplete: true, LastSuccessAt: backlogTime(now), LastFailureAt: backlogTime(now),
		}, want: failedTournamentDependency()},
		{name: "stale completion", health: gameusecase.RecoveryRunnerHealth{
			Running: true, InitialScanComplete: true,
			LastAttemptAt: backlogTime(now.Add(-runtimeRecoveryCompletionStaleAfter - time.Nanosecond)),
			LastSuccessAt: backlogTime(now.Add(-runtimeRecoveryCompletionStaleAfter - time.Nanosecond)),
		}, want: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
		}},
		{name: "future completion", health: gameusecase.RecoveryRunnerHealth{
			Running: true, InitialScanComplete: true, LastSuccessAt: backlogTime(now.Add(time.Second)),
		}, want: failedTournamentDependency()},
		{name: "completed", health: gameusecase.RecoveryRunnerHealth{
			Running: true, InitialScanComplete: true, LastAttemptAt: backlogTime(now), LastSuccessAt: backlogTime(now),
		}, want: healthyTournamentDependency()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.want, executionRecoveryHealthStatus(test.health, now))
		})
	}
}

func TestRuntimeWorkerHealthStatusMapsLifecycleStates(t *testing.T) {
	tests := []struct {
		name   string
		health runtimeWorkerHealth
		want   observability.TournamentDependencyStatus
	}{
		{
			name:   "created fails closed",
			health: runtimeWorkerHealth{State: runtimeWorkerStateCreated},
			want: observability.TournamentDependencyStatus{
				Health: observability.TournamentHealthStateFailed, Readiness: observability.TournamentReadinessStateNotReady,
			},
		},
		{
			name:   "starting is not ready",
			health: runtimeWorkerHealth{State: runtimeWorkerStateStarting},
			want: observability.TournamentDependencyStatus{
				Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateNotReady,
			},
		},
		{
			name:   "healthy is ready",
			health: runtimeWorkerHealth{State: runtimeWorkerStateHealthy},
			want: observability.TournamentDependencyStatus{
				Health: observability.TournamentHealthStateHealthy, Readiness: observability.TournamentReadinessStateReady,
			},
		},
		{
			name:   "stale is degraded",
			health: runtimeWorkerHealth{State: runtimeWorkerStateStale},
			want: observability.TournamentDependencyStatus{
				Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
			},
		},
		{
			name:   "stopped fails closed",
			health: runtimeWorkerHealth{State: runtimeWorkerStateStopped},
			want: observability.TournamentDependencyStatus{
				Health: observability.TournamentHealthStateFailed, Readiness: observability.TournamentReadinessStateNotReady,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := runtimeWorkerHealthSourceFunc(func(string) runtimeWorkerHealth {
				return test.health
			})

			require.Equal(t, test.want, runtimeWorkerHealthStatus(source, "worker"))
		})
	}
}

func TestCombinedDependencyHealthKeepsTheLeastReadyState(t *testing.T) {
	healthy := observability.TournamentDependencyStatus{
		Health: observability.TournamentHealthStateHealthy, Readiness: observability.TournamentReadinessStateReady,
	}
	starting := observability.TournamentDependencyStatus{
		Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateNotReady,
	}
	stale := observability.TournamentDependencyStatus{
		Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
	}
	failed := observability.TournamentDependencyStatus{
		Health: observability.TournamentHealthStateFailed, Readiness: observability.TournamentReadinessStateNotReady,
	}

	require.Equal(t, healthy, combinedDependencyHealth(healthy, healthy))
	require.Equal(t, starting, combinedDependencyHealth(healthy, starting))
	require.Equal(t, stale, combinedDependencyHealth(healthy, stale))
	require.Equal(t, failed, combinedDependencyHealth(healthy, failed))
}

func TestWorkerBoundDependencyHealthRequiresRuntimeState(t *testing.T) {
	healthy := observability.TournamentDependencyStatus{
		Health: observability.TournamentHealthStateHealthy, Readiness: observability.TournamentReadinessStateReady,
	}
	failed := observability.TournamentDependencyStatus{
		Health: observability.TournamentHealthStateFailed, Readiness: observability.TournamentReadinessStateNotReady,
	}
	failedWorker := runtimeWorkerHealthSourceFunc(func(string) runtimeWorkerHealth {
		return runtimeWorkerHealth{State: runtimeWorkerStateFailed}
	})

	require.Equal(t, failed, workerBoundDependencyHealth(healthy, nil, "worker"))
	require.Equal(t, failed, workerBoundDependencyHealth(healthy, failedWorker, "worker"))
}
