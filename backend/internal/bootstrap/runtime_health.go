package bootstrap

import (
	"context"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

const (
	runtimeEventDeliveryLastSuccessStaleAfter           = 30 * time.Second
	runtimeWorkerHeartbeatStaleAfter                    = 30 * time.Second
	runtimeEventDeliveryFailedAfter                     = 1
	runtimePrivateTaskAvailabilityLastSuccessStaleAfter = 30 * time.Second
	runtimePrivateTaskAvailabilityFailedAfter           = 1
	runtimePrivateTaskAvailabilityBacklogDegradedCount  = int64(1)
	runtimePrivateTaskAvailabilityBacklogFailedCount    = int64(2)
	runtimePrivateTaskAvailabilityOldestDegradedAge     = 30 * time.Second
	runtimePrivateTaskAvailabilityOldestFailedAge       = 5 * time.Minute
	runtimeOutboxBacklogDegradedCount                   = int64(128)
	runtimeOutboxBacklogFailedCount                     = int64(2048)
	runtimeOutboxOldestDegradedAge                      = 30 * time.Second
	runtimeOutboxOldestFailedAge                        = 5 * time.Minute
	runtimeProjectionLagDegradedAge                     = 5 * time.Second
	runtimeProjectionLagFailedAge                       = 30 * time.Second
	runtimeRecoveryCompletionStaleAfter                 = 30 * time.Second
	runtimeClockDegradedAfter                           = 2 * time.Second
	runtimeClockFailedAfter                             = 30 * time.Second
)

type runtimeWorkerHealthSource interface {
	WorkerHealth(name string) runtimeWorkerHealth
}

type runtimeWorkerHealthSourceFunc func(name string) runtimeWorkerHealth

func (fn runtimeWorkerHealthSourceFunc) WorkerHealth(name string) runtimeWorkerHealth {
	if fn == nil {
		return runtimeWorkerHealth{State: runtimeWorkerStateFailed}
	}
	return fn(name)
}

type runtimeWorkerHeartbeatReaderFunc func(
	ctx context.Context,
	worker string,
	freshAfter time.Time,
) (observability.RuntimeWorkerHeartbeatStatus, error)

func (fn runtimeWorkerHeartbeatReaderFunc) RuntimeWorkerHeartbeatStatus(
	ctx context.Context,
	worker string,
	freshAfter time.Time,
) (observability.RuntimeWorkerHeartbeatStatus, error) {
	if fn == nil {
		return observability.RuntimeWorkerHeartbeatStatus{}, context.Canceled
	}
	return fn(ctx, worker, freshAfter)
}

type runtimeWorkerHeartbeatReporterFunc func(
	ctx context.Context,
	heartbeat observability.RuntimeWorkerHeartbeat,
) error

func (fn runtimeWorkerHeartbeatReporterFunc) ReportRuntimeWorkerHeartbeat(
	ctx context.Context,
	heartbeat observability.RuntimeWorkerHeartbeat,
) error {
	if fn == nil {
		return context.Canceled
	}
	return fn(ctx, heartbeat)
}

func failedRuntimeDependency() observability.TournamentDependencyStatus {
	return observability.TournamentDependencyStatus{
		Health:    observability.TournamentHealthStateFailed,
		Readiness: observability.TournamentReadinessStateNotReady,
	}
}

func runtimeWorkerHealthStatus(
	source runtimeWorkerHealthSource,
	name string,
) observability.TournamentDependencyStatus {
	if source == nil {
		return failedRuntimeDependency()
	}
	//nolint:exhaustive // This switch intentionally handles only the valid states for this boundary.
	switch source.WorkerHealth(name).State {
	case runtimeWorkerStateHealthy:
		return observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateHealthy,
			Readiness: observability.TournamentReadinessStateReady,
		}
	case runtimeWorkerStateStarting:
		return observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateDegraded,
			Readiness: observability.TournamentReadinessStateNotReady,
		}
	case runtimeWorkerStateStale:
		return observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateDegraded,
			Readiness: observability.TournamentReadinessStateStale,
		}
	default:
		return failedRuntimeDependency()
	}
}

func combinedDependencyHealth(
	statuses ...observability.TournamentDependencyStatus,
) observability.TournamentDependencyStatus {
	result := observability.TournamentDependencyStatus{
		Health:    observability.TournamentHealthStateHealthy,
		Readiness: observability.TournamentReadinessStateReady,
	}
	for _, status := range statuses {
		switch status.Health {
		case observability.TournamentHealthStateFailed:
			return failedRuntimeDependency()
		case observability.TournamentHealthStateDegraded:
			result.Health = observability.TournamentHealthStateDegraded
		case observability.TournamentHealthStateHealthy:
		default:
			return failedRuntimeDependency()
		}
		switch status.Readiness {
		case observability.TournamentReadinessStateNotReady:
			result.Readiness = observability.TournamentReadinessStateNotReady
		case observability.TournamentReadinessStateStale:
			if result.Readiness == observability.TournamentReadinessStateReady {
				result.Readiness = observability.TournamentReadinessStateStale
			}
		case observability.TournamentReadinessStateReady:
		default:
			return failedRuntimeDependency()
		}
	}
	return result
}

func workerBoundDependencyHealth(
	dependency observability.TournamentDependencyStatus,
	workers runtimeWorkerHealthSource,
	workerName string,
) observability.TournamentDependencyStatus {
	if workers == nil {
		return dependency
	}
	return combinedDependencyHealth(dependency, runtimeWorkerHealthStatus(workers, workerName))
}

func projectionLagHealthStatus(
	snapshot observability.ProjectionHealthSnapshot,
) observability.TournamentDependencyStatus {
	if snapshot.Validate() != nil {
		return failedRuntimeDependency()
	}
	if snapshot.PendingCount == 0 {
		return healthyTournamentDependency()
	}
	lag := snapshot.ObservedAt.Sub(*snapshot.OldestPendingAt)
	switch {
	case lag >= runtimeProjectionLagFailedAge:
		return failedRuntimeDependency()
	case lag >= runtimeProjectionLagDegradedAge:
		return observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateDegraded,
			Readiness: observability.TournamentReadinessStateStale,
		}
	default:
		return healthyTournamentDependency()
	}
}

func executionRecoveryHealthStatus(
	health gameusecase.RecoveryRunnerHealth,
	now time.Time,
) observability.TournamentDependencyStatus {
	if !validExecutionRecoveryHealth(health, now) {
		return failedRuntimeDependency()
	}
	switch {
	case !health.Running || health.LastFailureAt != nil:
		return failedRuntimeDependency()
	case !health.InitialScanComplete || health.LastSuccessAt == nil:
		return observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateDegraded,
			Readiness: observability.TournamentReadinessStateNotReady,
		}
	case now.Sub(*health.LastSuccessAt) > runtimeRecoveryCompletionStaleAfter:
		return observability.TournamentDependencyStatus{
			Health:    observability.TournamentHealthStateDegraded,
			Readiness: observability.TournamentReadinessStateStale,
		}
	default:
		return healthyTournamentDependency()
	}
}

func validExecutionRecoveryHealth(health gameusecase.RecoveryRunnerHealth, now time.Time) bool {
	if !domain.IsValidServerTime(now) {
		return false
	}
	for _, value := range []*time.Time{
		health.LastAttemptAt,
		health.LastSuccessAt,
		health.LastFailureAt,
	} {
		if value != nil && (!domain.IsValidServerTime(*value) || value.After(now)) {
			return false
		}
	}
	if health.InitialScanComplete && health.LastSuccessAt == nil {
		return false
	}
	if health.LastSuccessAt != nil &&
		(health.LastAttemptAt == nil || health.LastSuccessAt.After(*health.LastAttemptAt)) {
		return false
	}
	if health.LastFailureAt != nil &&
		(health.LastAttemptAt == nil || health.LastFailureAt.After(*health.LastAttemptAt)) {
		return false
	}
	return true
}
