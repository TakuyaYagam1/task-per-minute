package recovery

import "time"

type WorkerHealthState string

const (
	WorkerHealthStarting WorkerHealthState = "starting"
	WorkerHealthHealthy  WorkerHealthState = "healthy"
	WorkerHealthFailed   WorkerHealthState = "failed"
	WorkerHealthStale    WorkerHealthState = "stale"
	WorkerHealthStopped  WorkerHealthState = "stopped"
)

// WorkerHealth is safe to expose through readiness endpoints. It contains no
// repository or handler error text.
type WorkerHealth struct {
	State                WorkerHealthState
	Ready                bool
	Started              bool
	Running              bool
	InitialSweepComplete bool
	StartedAt            *time.Time
	LastAttemptAt        *time.Time
	LastSuccessAt        *time.Time
	LastFailureAt        *time.Time
	ConsecutiveFailures  int64
}

type WorkerHealthSource interface {
	Health(now time.Time) WorkerHealth
}

var _ WorkerHealthSource = (*Worker)(nil)

func (worker *Worker) Health(now time.Time) WorkerHealth {
	now = now.Round(0).UTC()
	if worker == nil || !validRecoveryTime(now) {
		return WorkerHealth{State: WorkerHealthFailed}
	}

	worker.stateMu.RLock()
	health := cloneWorkerHealth(worker.health)
	worker.stateMu.RUnlock()
	health = worker.mergeExecutionHealth(health)
	health.Ready = false
	switch {
	case health.Started && !health.Running:
		health.State = WorkerHealthStopped
	case health.ConsecutiveFailures > 0:
		health.State = WorkerHealthFailed
	case !health.Running || !health.InitialSweepComplete || health.LastSuccessAt == nil:
		health.State = WorkerHealthStarting
	case now.Before(*health.LastSuccessAt) || now.Sub(*health.LastSuccessAt) > worker.config.StaleAfter:
		health.State = WorkerHealthStale
	default:
		health.State = WorkerHealthHealthy
		health.Ready = true
	}
	return health
}

func (worker *Worker) mergeExecutionHealth(health WorkerHealth) WorkerHealth {
	if worker.execution == nil || !health.Running {
		return health
	}
	execution := worker.execution.ExecutionHealth()
	health.LastAttemptAt = latestRecoveryTime(health.LastAttemptAt, execution.LastAttemptAt)
	health.LastFailureAt = latestRecoveryTime(health.LastFailureAt, execution.LastFailureAt)
	if execution.ConsecutiveFailures > health.ConsecutiveFailures {
		health.ConsecutiveFailures = execution.ConsecutiveFailures
	}
	if !execution.Running && health.ConsecutiveFailures == 0 {
		health.ConsecutiveFailures = 1
	}
	return health
}

func latestRecoveryTime(first, second *time.Time) *time.Time {
	if first == nil || (second != nil && second.After(*first)) {
		return cloneRecoveryTime(second)
	}
	return cloneRecoveryTime(first)
}

func cloneWorkerHealth(health WorkerHealth) WorkerHealth {
	health.StartedAt = cloneRecoveryTime(health.StartedAt)
	health.LastAttemptAt = cloneRecoveryTime(health.LastAttemptAt)
	health.LastSuccessAt = cloneRecoveryTime(health.LastSuccessAt)
	health.LastFailureAt = cloneRecoveryTime(health.LastFailureAt)
	return health
}

func cloneRecoveryTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.Round(0).UTC()
	return &cloned
}
