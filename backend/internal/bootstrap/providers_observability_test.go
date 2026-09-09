package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
	eventdeliverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery/mocks"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	recoverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery/mocks"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func TestEventTelemetryProviders(t *testing.T) {
	t.Parallel()

	t.Run("queues metrics behind the observer worker", func(t *testing.T) {
		t.Parallel()

		telemetry := requireEventTelemetry(t)
		require.NotNil(t, telemetry.metrics)
		require.NotNil(t, telemetry.observer)

		const tournamentID = "72000000-0000-0000-0000-000000000001"
		require.NoError(t, observability.EmitTournamentEvent(t.Context(), telemetry.observer, observability.TournamentEventInput{
			Event:         "tournament.lifecycle.transition",
			Outcome:       observability.TournamentOutcomeSuccess,
			CorrelationID: tournamentID,
			TournamentID:  tournamentID,
			EntityKind:    "tournament",
			EntityID:      tournamentID,
			Stage:         "tournament_lifecycle",
			Transition:    "draft_to_registration",
			ReasonCode:    "transitioned",
			Revision:      1,
		}))

		families, err := telemetry.metrics.Gatherer().Gather()
		require.NoError(t, err)
		require.Zero(t, tournamentOperationCount(families, "lifecycle", "success"))

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- telemetry.dispatcher.Run(ctx) }()
		require.Eventually(t, telemetry.dispatcher.Ready, time.Second, time.Millisecond)
		require.Eventually(t, func() bool {
			families, gatherErr := telemetry.metrics.Gatherer().Gather()
			return gatherErr == nil && tournamentOperationCount(families, "lifecycle", "success") == 1
		}, time.Second, time.Millisecond)
		cancel()
		require.NoError(t, <-done)
	})

	t.Run("health source fails closed and follows runtime shutdown", func(t *testing.T) {
		t.Parallel()

		runtime, cancel := context.WithCancel(t.Context())
		telemetry := requireEventTelemetry(t)
		realtime := NewMockRealtimeHealthSource(t)
		realtime.EXPECT().Health(mock.Anything).Return(healthyWorkerHealth(provideClock().Now())).Maybe()
		heartbeats := runtimeWorkerHeartbeatReaderFunc(func(
			context.Context,
			string,
			time.Time,
		) (observability.RuntimeWorkerHeartbeatStatus, error) {
			return observability.RuntimeWorkerHeartbeatStatus{
				Worker:         "realtime-session-delivery",
				FreshInstances: 1,
			}, nil
		})
		probe := provideHealthProbe(
			runtime, realtime, nil, provideClock(), telemetry, nil, nil, nil, nil, nil, nil, heartbeats, nil,
		)

		active := probe.TournamentHealth(t.Context())
		require.False(t, active.Healthy())
		require.Equal(t, observability.TournamentHealthStateFailed, active.Authority.Health)
		require.Equal(t, observability.TournamentHealthStateHealthy, active.Realtime.Health)

		cancel()
		stopped := probe.TournamentHealth(t.Context())
		require.Equal(t, observability.TournamentHealthStateFailed, stopped.Realtime.Health)
		require.Equal(t, observability.TournamentReadinessStateNotReady, stopped.Realtime.Readiness)
	})

	t.Run("health source fails closed when realtime is not ready", func(t *testing.T) {
		t.Parallel()

		realtime := NewMockRealtimeHealthSource(t)
		realtime.EXPECT().Health(mock.Anything).Return(eventdelivery.HealthSnapshot{}).Once()
		probe := provideHealthProbe(t.Context(), realtime, nil, provideClock(), requireEventTelemetry(t), nil, nil, nil, nil, nil, nil, nil, nil)

		snapshot := probe.TournamentHealth(t.Context())
		require.Equal(t, observability.TournamentHealthStateFailed, snapshot.Realtime.Health)
		require.Equal(t, observability.TournamentReadinessStateNotReady, snapshot.Realtime.Readiness)
	})

	t.Run("duplicate application graphs own isolated registries", func(t *testing.T) {
		t.Parallel()

		first := requireEventTelemetry(t)
		second := requireEventTelemetry(t)

		require.NotSame(t, first.metrics, second.metrics)
		require.NotSame(t, first.metrics.Gatherer(), second.metrics.Gatherer())
	})

	t.Run("private handler exposes only the application registry", func(t *testing.T) {
		t.Parallel()

		telemetry := requireEventTelemetry(t)
		const tournamentID = "72000000-0000-0000-0000-000000000002"
		require.NoError(t, observability.EmitTournamentEvent(t.Context(), telemetry.metrics, observability.TournamentEventInput{
			Event:         "tournament.recovery",
			Outcome:       observability.TournamentOutcomeSuccess,
			CorrelationID: tournamentID,
			TournamentID:  tournamentID,
			EntityKind:    "tournament",
			EntityID:      tournamentID,
			Stage:         "startup_recovery",
			Transition:    "pending_to_rearmed",
			ReasonCode:    "recovered",
			Revision:      1,
		}))

		handler := providePrivateMetricsHandler(telemetry)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/internal/metrics", nil))

		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Header().Get("Content-Type"), "text/plain")
		require.Contains(t, response.Body.String(), "tpm_tournament_operations_total")
	})

	t.Run("private handler fails closed without a registry", func(t *testing.T) {
		t.Parallel()

		handler := providePrivateMetricsHandler(eventTelemetry{})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/internal/metrics", nil))

		require.Equal(t, http.StatusServiceUnavailable, response.Code)
	})
}

func TestHealthProbeClockDriftUsesMidpointForDelayedDatabaseProbe(t *testing.T) {
	t.Parallel()

	before := time.Date(2026, time.September, 7, 15, 0, 0, 0, time.UTC)
	localSamples := []time.Time{before, before.Add(10 * time.Second)}
	nextSample := 0
	queryCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	probe := &healthProbe{
		clock: clockFunc(func() time.Time {
			value := localSamples[nextSample]
			nextSample++
			return value
		}),
		databaseClock: healthDatabaseClockFunc(func(ctx context.Context) (time.Time, error) {
			require.Same(t, queryCtx, ctx)
			_, bounded := ctx.Deadline()
			require.True(t, bounded)
			// The query consumed ten seconds of local time, but its database
			// timestamp is centered in that interval.
			return before.Add(5 * time.Second), nil
		}),
	}
	snapshot := observability.HealthyTournamentHealthSnapshot()

	probe.measureClockDrift(queryCtx, &snapshot)

	require.Equal(t, observability.TournamentHealthStateHealthy, snapshot.Clock.Health)
	require.Equal(t, observability.TournamentReadinessStateReady, snapshot.Clock.Readiness)
}

func TestPreflightRuntimeHealthSourceSamplesBoundedProductionSignals(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 14, 0, 0, 0, time.UTC)
	realtime := NewMockRealtimeHealthSource(t)
	realtime.EXPECT().Health(now).Return(healthyWorkerHealth(now)).Once()

	source := preflightRuntimeHealthSource{
		clock:            clockFunc(func() time.Time { return now }),
		taskAvailability: healthyPrivateTaskAvailabilityHealth(now),
		receiptBacklog:   healthyPrivateTaskReceiptBacklog(),
		realtime:         realtime,
		heartbeats:       healthyRuntimeWorkerHeartbeats(),
		redis: preflightDependencyProbeFunc(func(ctx context.Context) error {
			_, bounded := ctx.Deadline()
			require.True(t, bounded)
			return nil
		}),
		objectStorage: preflightDependencyProbeFunc(func(ctx context.Context) error {
			_, bounded := ctx.Deadline()
			require.True(t, bounded)
			return nil
		}),
		databaseClock: preflightClockProbeFunc(func(ctx context.Context) (time.Time, error) {
			_, bounded := ctx.Deadline()
			require.True(t, bounded)
			return now, nil
		}),
	}
	health := source.RuntimeHealth(t.Context())

	require.Equal(t, tournamentpreflight.ComponentHealth{
		Healthy: true, Revision: "task_delivery:healthy:ready",
	}, health.TaskDelivery)
	require.Equal(t, tournamentpreflight.ComponentHealth{
		Healthy: true, Revision: "realtime:healthy:ready",
	}, health.Realtime)
	require.Equal(t, tournamentpreflight.ClockHealth{
		ObservedAt: now, ReferenceAt: now, MaxSkew: runtimeClockDegradedAfter,
	}, health.Clock)
	require.True(t, health.ClockSampled)
	require.Equal(t, runtimeClockDegradedAfter, health.Clock.MaxSkew)
	require.Equal(t, []tournamentpreflight.DependencyHealth{
		{Name: tournamentpreflight.DependencyRedis, Healthy: true, Revision: "redis:ready"},
		{Name: tournamentpreflight.DependencyObjectStorage, Healthy: true, Revision: "object_storage:ready"},
	}, health.Dependencies)
}

func TestPreflightRuntimeHealthSourceFailsClosedAndRecovers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 14, 1, 0, 0, time.UTC)
	available := false
	realtime := NewMockRealtimeHealthSource(t)
	realtime.EXPECT().Health(now).Return(healthyWorkerHealth(now)).Twice()
	source := preflightRuntimeHealthSource{
		clock:            clockFunc(func() time.Time { return now }),
		taskAvailability: healthyPrivateTaskAvailabilityHealth(now),
		receiptBacklog:   healthyPrivateTaskReceiptBacklog(),
		realtime:         realtime,
		heartbeats:       healthyRuntimeWorkerHeartbeats(),
		redis: preflightDependencyProbeFunc(func(context.Context) error {
			if !available {
				return context.DeadlineExceeded
			}
			return nil
		}),
		objectStorage: preflightDependencyProbeFunc(func(context.Context) error {
			if !available {
				return context.DeadlineExceeded
			}
			return nil
		}),
		databaseClock: preflightClockProbeFunc(func(context.Context) (time.Time, error) {
			if !available {
				return time.Time{}, context.DeadlineExceeded
			}
			return now, nil
		}),
	}

	failed := source.RuntimeHealth(t.Context())
	require.Equal(t, tournamentpreflight.ComponentHealth{Healthy: true, Revision: "task_delivery:healthy:ready"}, failed.TaskDelivery)
	require.Equal(t, tournamentpreflight.ClockHealth{
		ObservedAt: now, MaxSkew: runtimeClockDegradedAfter,
	}, failed.Clock)
	require.Equal(t, []tournamentpreflight.DependencyHealth{
		{Name: tournamentpreflight.DependencyRedis, Healthy: false, Revision: "redis:failed"},
		{Name: tournamentpreflight.DependencyObjectStorage, Healthy: false, Revision: "object_storage:failed"},
	}, failed.Dependencies)

	available = true
	recovered := source.RuntimeHealth(t.Context())
	require.Equal(t, tournamentpreflight.ComponentHealth{Healthy: true, Revision: "task_delivery:healthy:ready"}, recovered.TaskDelivery)
	require.Equal(t, tournamentpreflight.ClockHealth{
		ObservedAt: now, ReferenceAt: now, MaxSkew: runtimeClockDegradedAfter,
	}, recovered.Clock)
	require.Equal(t, []tournamentpreflight.DependencyHealth{
		{Name: tournamentpreflight.DependencyRedis, Healthy: true, Revision: "redis:ready"},
		{Name: tournamentpreflight.DependencyObjectStorage, Healthy: true, Revision: "object_storage:ready"},
	}, recovered.Dependencies)
}

func TestPreflightRuntimeHealthSourceFailsClosedForStaleSharedWorkerHeartbeats(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 14, 1, 30, 0, time.UTC)
	fresh := false
	realtime := NewMockRealtimeHealthSource(t)
	realtime.EXPECT().Health(now).Return(healthyWorkerHealth(now)).Twice()
	source := preflightRuntimeHealthSource{
		clock:            clockFunc(func() time.Time { return now }),
		taskAvailability: healthyPrivateTaskAvailabilityHealth(now),
		receiptBacklog:   healthyPrivateTaskReceiptBacklog(),
		realtime:         realtime,
		heartbeats: runtimeWorkerHeartbeatReaderFunc(func(
			ctx context.Context,
			worker string,
			freshAfter time.Time,
		) (observability.RuntimeWorkerHeartbeatStatus, error) {
			_, bounded := ctx.Deadline()
			require.True(t, bounded)
			switch worker {
			case "private-task-availability":
				require.Equal(t, now.Add(-runtimePrivateTaskAvailabilityLastSuccessStaleAfter), freshAfter)
			case "realtime-session-delivery":
				require.Equal(t, now.Add(-runtimeEventDeliveryLastSuccessStaleAfter), freshAfter)
			default:
				t.Fatalf("unexpected worker %q", worker)
			}
			instances := 0
			if fresh {
				instances = 2
			}
			return observability.RuntimeWorkerHeartbeatStatus{Worker: worker, FreshInstances: instances}, nil
		}),
		redis:         preflightDependencyProbeFunc(func(context.Context) error { return nil }),
		objectStorage: preflightDependencyProbeFunc(func(context.Context) error { return nil }),
		databaseClock: preflightClockProbeFunc(func(context.Context) (time.Time, error) { return now, nil }),
	}

	stale := source.RuntimeHealth(t.Context())
	require.False(t, stale.TaskDelivery.Healthy)
	require.False(t, stale.Realtime.Healthy)
	require.Equal(t, "task_delivery:degraded:stale", stale.TaskDelivery.Revision)
	require.Equal(t, "realtime:degraded:stale", stale.Realtime.Revision)

	fresh = true
	recovered := source.RuntimeHealth(t.Context())
	require.True(t, recovered.TaskDelivery.Healthy)
	require.Equal(t, "task_delivery:healthy:ready", recovered.TaskDelivery.Revision)
	require.True(t, recovered.Realtime.Healthy)
}

func TestHealthProbeFailsClosedWithoutSharedWorkerHeartbeats(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 14, 1, 45, 0, time.UTC)
	probe := &healthProbe{}

	require.Equal(t, failedTournamentDependency(), probe.sharedWorkerHealth(
		t.Context(),
		"event-delivery",
		now,
		runtimeEventDeliveryLastSuccessStaleAfter,
	))
}

func TestHealthProbeProjectionLagFailsClosedAndRecovers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 16, 20, 0, 0, time.UTC)
	available := false
	probe := &healthProbe{
		projection: observability.ProjectionHealthSourceFunc(func(context.Context) (observability.ProjectionHealthSnapshot, error) {
			if !available {
				return observability.ProjectionHealthSnapshot{}, context.DeadlineExceeded
			}
			return observability.ProjectionHealthSnapshot{ObservedAt: now}, nil
		}),
	}

	require.Equal(t, failedTournamentDependency(), probe.projectionHealth(t.Context()))

	available = true
	require.Equal(t, healthyTournamentDependency(), probe.projectionHealth(t.Context()))
}

func TestHealthProbeRecoveryRequiresExecutionRecoveryAcrossReplicas(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 16, 25, 0, 0, time.UTC)
	executionRunnerReady := false
	executionHeartbeatReady := true
	recoverySource := recoverymocks.NewMockWorkerHealthSource(t)
	recoverySource.EXPECT().Health(now).Return(recovery.WorkerHealth{
		State: recovery.WorkerHealthHealthy, Ready: true,
	}).Times(3)
	probe := &healthProbe{
		recovery: recoverySource,
		workers: runtimeWorkerHealthSourceFunc(func(name string) runtimeWorkerHealth {
			if name == "execution-recovery" && !executionRunnerReady {
				return runtimeWorkerHealth{State: runtimeWorkerStateStale}
			}
			return runtimeWorkerHealth{State: runtimeWorkerStateHealthy}
		}),
		heartbeats: runtimeWorkerHeartbeatReaderFunc(func(
			_ context.Context,
			worker string,
			freshAfter time.Time,
		) (observability.RuntimeWorkerHeartbeatStatus, error) {
			require.Contains(t, []string{"deadline-scheduler", "deadline-recovery", "execution-recovery"}, worker)
			require.Equal(t, now.Add(-runtimeRecoveryCompletionStaleAfter), freshAfter)
			if worker == "execution-recovery" && !executionHeartbeatReady {
				return observability.RuntimeWorkerHeartbeatStatus{Worker: worker}, nil
			}
			return observability.RuntimeWorkerHeartbeatStatus{Worker: worker, FreshInstances: 2}, nil
		}),
	}

	stalled := probe.recoveryHealth(t.Context(), now)
	require.Equal(t, observability.TournamentHealthStateDegraded, stalled.Health)
	require.Equal(t, observability.TournamentReadinessStateStale, stalled.Readiness)

	executionRunnerReady = true
	executionHeartbeatReady = false
	missingReplicaHeartbeat := probe.recoveryHealth(t.Context(), now)
	require.Equal(t, observability.TournamentHealthStateDegraded, missingReplicaHeartbeat.Health)
	require.Equal(t, observability.TournamentReadinessStateStale, missingReplicaHeartbeat.Readiness)

	executionHeartbeatReady = true
	recovered := probe.recoveryHealth(t.Context(), now)
	require.Equal(t, healthyTournamentDependency(), recovered)
}

func TestPreflightRuntimeHealthSourceUsesClockQueryMidpoint(t *testing.T) {
	t.Parallel()

	before := time.Date(2026, time.September, 7, 14, 2, 0, 0, time.UTC)
	clockValues := []time.Time{before, before, before.Add(10 * time.Second)}
	clockIndex := 0
	clock := clockFunc(func() time.Time {
		value := clockValues[clockIndex]
		clockIndex++
		return value
	})
	realtime := NewMockRealtimeHealthSource(t)
	realtime.EXPECT().Health(before).Return(healthyWorkerHealth(before)).Once()
	source := preflightRuntimeHealthSource{
		clock:            clock,
		taskAvailability: healthyPrivateTaskAvailabilityHealth(before),
		receiptBacklog:   healthyPrivateTaskReceiptBacklog(),
		realtime:         realtime,
		heartbeats:       healthyRuntimeWorkerHeartbeats(),
		redis: preflightDependencyProbeFunc(func(context.Context) error {
			return nil
		}),
		objectStorage: preflightDependencyProbeFunc(func(context.Context) error {
			return nil
		}),
		databaseClock: preflightClockProbeFunc(func(context.Context) (time.Time, error) {
			return before.Add(5 * time.Second), nil
		}),
	}

	health := source.RuntimeHealth(t.Context())
	require.Equal(t, tournamentpreflight.ClockHealth{
		ObservedAt: before.Add(5 * time.Second), ReferenceAt: before.Add(5 * time.Second),
		MaxSkew: runtimeClockDegradedAfter,
	}, health.Clock)
}

func healthyRuntimeWorkerHeartbeats() runtimeWorkerHeartbeatReaderFunc {
	return func(
		_ context.Context,
		worker string,
		_ time.Time,
	) (observability.RuntimeWorkerHeartbeatStatus, error) {
		return observability.RuntimeWorkerHeartbeatStatus{Worker: worker, FreshInstances: 2}, nil
	}
}

func healthyWorkerHealth(now time.Time) eventdelivery.HealthSnapshot {
	now = now.Round(0).UTC()
	return eventdelivery.HealthSnapshot{
		Started: true, Running: true, StartedAt: &now, LastAttemptAt: &now, LastSuccessAt: &now,
	}
}

func healthyPrivateTaskAvailabilityHealth(now time.Time) taskusecase.HealthSource {
	now = now.Round(0).UTC()
	return taskusecase.HealthSourceFunc(func(time.Time) taskusecase.HealthSnapshot {
		return taskusecase.HealthSnapshot{
			Started: true, Running: true, StartedAt: &now, LastAttemptAt: &now, LastSuccessAt: &now,
		}
	})
}

func healthyPrivateTaskReceiptBacklog() taskusecase.BacklogSource {
	return taskusecase.BacklogSourceFunc(func(context.Context) (taskusecase.BacklogSnapshot, error) {
		return taskusecase.BacklogSnapshot{}, nil
	})
}

func requireEventTelemetry(t *testing.T) eventTelemetry {
	t.Helper()
	telemetry, err := provideEventTelemetry(logkit.Noop())
	require.NoError(t, err)
	return telemetry
}

func tournamentOperationCount(families []*dto.MetricFamily, operation string, outcome string) uint64 {
	for _, family := range families {
		if family.GetName() != "tpm_tournament_operations_total" {
			continue
		}
		for _, metric := range family.Metric {
			labels := make(map[string]string, len(metric.Label))
			for _, pair := range metric.Label {
				labels[pair.GetName()] = pair.GetValue()
			}
			if labels["operation"] == operation && labels["outcome"] == outcome && metric.Counter != nil {
				return uint64(metric.Counter.GetValue())
			}
		}
	}
	return 0
}

func TestOutboxBacklogHealthThresholds(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	healthy := healthyTournamentDependency()
	tests := []struct {
		name     string
		backlog  eventdelivery.BacklogSnapshot
		delivery observability.TournamentDependencyStatus
		want     observability.TournamentDependencyStatus
	}{
		{name: "empty", delivery: healthy, want: healthy},
		{name: "delivery degraded", delivery: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateNotReady,
		}, want: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateNotReady,
		}},
		{name: "backlog degraded", backlog: eventdelivery.BacklogSnapshot{
			PendingCount: runtimeOutboxBacklogDegradedCount, OldestPendingAt: backlogTime(now),
		}, delivery: healthy, want: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
		}},
		{name: "backlog failed", backlog: eventdelivery.BacklogSnapshot{
			PendingCount: runtimeOutboxBacklogFailedCount, OldestPendingAt: backlogTime(now),
		}, delivery: healthy, want: failedTournamentDependency()},
		{name: "future timestamp", backlog: eventdelivery.BacklogSnapshot{
			PendingCount: 1, OldestPendingAt: backlogTime(now.Add(time.Second)),
		}, delivery: healthy, want: failedTournamentDependency()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.want, outboxBacklogHealthStatus(test.backlog, now, test.delivery))
		})
	}
}

func backlogTime(value time.Time) *time.Time {
	value = value.Round(0).UTC()
	return &value
}

func TestEventDeliveryHealthStatusUsesWorkerState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 13, 0, 0, 0, time.UTC)
	started := now.Add(-time.Minute)
	lastAttempt := now.Add(-time.Second)
	tests := []struct {
		name   string
		health eventdelivery.HealthSnapshot
		want   observability.TournamentDependencyStatus
	}{
		{name: "not started", want: failedTournamentDependency()},
		{name: "starting", health: eventdelivery.HealthSnapshot{
			Started: true, Running: true, StartedAt: backlogTime(started),
		}, want: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateNotReady,
		}},
		{name: "healthy", health: eventdelivery.HealthSnapshot{
			Started: true, Running: true, StartedAt: backlogTime(started),
			LastAttemptAt: backlogTime(lastAttempt), LastSuccessAt: backlogTime(lastAttempt),
		}, want: healthyTournamentDependency()},
		{name: "stalled", health: eventdelivery.HealthSnapshot{
			Started: true, Running: true, Stale: true, StartedAt: backlogTime(started),
			LastAttemptAt: backlogTime(lastAttempt), LastSuccessAt: backlogTime(lastAttempt),
		}, want: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
		}},
		{name: "last success exceeds reviewed threshold", health: eventdelivery.HealthSnapshot{
			Started: true, Running: true, StartedAt: backlogTime(started),
			LastAttemptAt: backlogTime(lastAttempt),
			LastSuccessAt: backlogTime(now.Add(-runtimeEventDeliveryLastSuccessStaleAfter - time.Nanosecond)),
		}, want: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
		}},
		{name: "dependency failed", health: eventdelivery.HealthSnapshot{
			Started: true, Running: true, StartedAt: backlogTime(started),
			LastAttemptAt: backlogTime(lastAttempt), LastFailureAt: backlogTime(lastAttempt), ConsecutiveFailures: 1,
		}, want: failedTournamentDependency()},
		{name: "future heartbeat", health: eventdelivery.HealthSnapshot{
			Started: true, Running: true, StartedAt: backlogTime(started),
			LastAttemptAt: backlogTime(now.Add(time.Second)), LastSuccessAt: backlogTime(now.Add(time.Second)),
		}, want: failedTournamentDependency()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := eventdeliverymocks.NewMockHealthSource(t)
			source.EXPECT().Health(now).Return(test.health).Once()
			require.Equal(t, test.want, eventDeliveryHealthStatus(source, now))
		})
	}
}

func TestPrivateTaskAvailabilityHealthStatusUsesMonitorState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 13, 30, 0, 0, time.UTC)
	started := now.Add(-time.Minute)
	lastAttempt := now.Add(-time.Second)
	tests := []struct {
		name   string
		health taskusecase.HealthSnapshot
		want   observability.TournamentDependencyStatus
	}{
		{name: "not started", want: failedTournamentDependency()},
		{name: "starting", health: taskusecase.HealthSnapshot{
			Started: true, Running: true, StartedAt: backlogTime(started),
		}, want: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateNotReady,
		}},
		{name: "healthy", health: taskusecase.HealthSnapshot{
			Started: true, Running: true, StartedAt: backlogTime(started),
			LastAttemptAt: backlogTime(lastAttempt), LastSuccessAt: backlogTime(lastAttempt),
		}, want: healthyTournamentDependency()},
		{name: "stalled", health: taskusecase.HealthSnapshot{
			Started: true, Running: true, Stale: true, StartedAt: backlogTime(started),
			LastAttemptAt: backlogTime(lastAttempt), LastSuccessAt: backlogTime(lastAttempt),
		}, want: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
		}},
		{name: "last success exceeds reviewed threshold", health: taskusecase.HealthSnapshot{
			Started: true, Running: true, StartedAt: backlogTime(started),
			LastAttemptAt: backlogTime(lastAttempt),
			LastSuccessAt: backlogTime(now.Add(-runtimePrivateTaskAvailabilityLastSuccessStaleAfter - time.Nanosecond)),
		}, want: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
		}},
		{name: "dependency failed", health: taskusecase.HealthSnapshot{
			Started: true, Running: true, StartedAt: backlogTime(started),
			LastAttemptAt: backlogTime(lastAttempt), LastFailureAt: backlogTime(lastAttempt), ConsecutiveFailures: 1,
		}, want: failedTournamentDependency()},
		{name: "future heartbeat", health: taskusecase.HealthSnapshot{
			Started: true, Running: true, StartedAt: backlogTime(started),
			LastAttemptAt: backlogTime(now.Add(time.Second)), LastSuccessAt: backlogTime(now.Add(time.Second)),
		}, want: failedTournamentDependency()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := taskusecase.HealthSourceFunc(func(time.Time) taskusecase.HealthSnapshot { return test.health })
			require.Equal(t, test.want, privateTaskAvailabilityHealthStatus(source, now))
		})
	}
}

func TestPrivateTaskAvailabilityBacklogHealthThresholds(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 13, 45, 0, 0, time.UTC)
	tests := []struct {
		name    string
		backlog taskusecase.BacklogSnapshot
		want    observability.TournamentDependencyStatus
	}{
		{name: "empty", want: healthyTournamentDependency()},
		{name: "pending receipt is stale", backlog: taskusecase.BacklogSnapshot{
			PendingCount: runtimePrivateTaskAvailabilityBacklogDegradedCount, OldestPendingAt: backlogTime(now),
		}, want: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
		}},
		{name: "pending receipt count failed", backlog: taskusecase.BacklogSnapshot{
			PendingCount: runtimePrivateTaskAvailabilityBacklogFailedCount, OldestPendingAt: backlogTime(now),
		}, want: failedTournamentDependency()},
		{name: "pending receipt age failed", backlog: taskusecase.BacklogSnapshot{
			PendingCount: 1, OldestPendingAt: backlogTime(now.Add(-runtimePrivateTaskAvailabilityOldestFailedAge)),
		}, want: failedTournamentDependency()},
		{name: "future oldest receipt", backlog: taskusecase.BacklogSnapshot{
			PendingCount: 1, OldestPendingAt: backlogTime(now.Add(time.Second)),
		}, want: failedTournamentDependency()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.want, privateTaskAvailabilityBacklogHealthStatus(test.backlog, now))
		})
	}
}

func TestRecoveryHealthStatusUsesCompletionAndFailureState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 7, 13, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		health recovery.WorkerHealth
		want   observability.TournamentDependencyStatus
	}{
		{name: "completed", health: recovery.WorkerHealth{
			State: recovery.WorkerHealthHealthy, Ready: true,
		}, want: healthyTournamentDependency()},
		{name: "initial sweep pending", health: recovery.WorkerHealth{
			State: recovery.WorkerHealthStarting,
		}, want: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateNotReady,
		}},
		{name: "stale", health: recovery.WorkerHealth{
			State: recovery.WorkerHealthStale,
		}, want: observability.TournamentDependencyStatus{
			Health: observability.TournamentHealthStateDegraded, Readiness: observability.TournamentReadinessStateStale,
		}},
		{name: "failed", health: recovery.WorkerHealth{
			State: recovery.WorkerHealthFailed,
		}, want: failedTournamentDependency()},
		{name: "healthy but not ready", health: recovery.WorkerHealth{
			State: recovery.WorkerHealthHealthy,
		}, want: failedTournamentDependency()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := recoverymocks.NewMockWorkerHealthSource(t)
			source.EXPECT().Health(now).Return(test.health).Once()
			require.Equal(t, test.want, recoveryHealthStatus(source, now))
		})
	}
}
