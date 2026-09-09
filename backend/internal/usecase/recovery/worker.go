package recovery

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	DefaultSweepInterval   = 5 * time.Second
	DefaultSweepTimeout    = 3 * time.Second
	DefaultSweepStaleAfter = 30 * time.Second
)

var (
	ErrInvalidWorkerConfig = errors.New("invalid recovery worker config")
	ErrWorkerRunning       = errors.New("recovery worker is already running")
)

type WorkerConfig struct {
	BatchSize     int32
	SweepInterval time.Duration
	SweepTimeout  time.Duration
	StaleAfter    time.Duration
}

type Worker struct {
	sweep     *DeadlineSweep
	clock     Clock
	execution DeadlineExecutionHealthSource
	config    WorkerConfig

	running atomic.Bool

	stateMu sync.RWMutex
	cursor  DeadlineCursor
	health  WorkerHealth
}

func NewWorker(
	sweep *DeadlineSweep,
	clock Clock,
	config WorkerConfig,
	execution ...DeadlineExecutionHealthSource,
) (*Worker, error) {
	config = workerConfigWithDefaults(config)
	if !availableDeadlineSweep(sweep) || clock == nil || !validWorkerConfig(config) ||
		len(execution) > 1 || (len(execution) == 1 && execution[0] == nil) {
		return nil, ErrInvalidWorkerConfig
	}
	worker := &Worker{sweep: sweep, clock: clock, config: config}
	if len(execution) == 1 {
		worker.execution = execution[0]
	}
	return worker, nil
}

// Run performs one startup sweep immediately and then one bounded sweep per
// interval. Sweep failures are retried on the next interval and reflected in
// Health without leaking their text.
func (worker *Worker) Run(ctx context.Context) error {
	if ctx == nil || worker == nil || !availableDeadlineSweep(worker.sweep) || worker.clock == nil ||
		!validWorkerConfig(worker.config) {
		return ErrInvalidWorkerConfig
	}
	startedAt := worker.now()
	if !validRecoveryTime(startedAt) {
		return domain.ErrValidation
	}
	if !worker.running.CompareAndSwap(false, true) {
		return ErrWorkerRunning
	}
	worker.markStarted(startedAt)
	defer func() {
		worker.running.Store(false)
		worker.markStopped()
	}()

	for {
		if ctx.Err() != nil {
			return nil
		}
		worker.runSweep(ctx)
		if !waitForSweep(ctx, worker.config.SweepInterval) {
			return nil
		}
	}
}

func availableDeadlineSweep(sweep *DeadlineSweep) bool {
	return sweep != nil && sweep.source != nil && sweep.rearmer != nil
}

func (worker *Worker) runSweep(ctx context.Context) {
	worker.stateMu.RLock()
	after := worker.cursor
	worker.stateMu.RUnlock()

	sweepCtx, cancel := context.WithTimeout(ctx, worker.config.SweepTimeout)
	result, err := worker.sweep.Sweep(sweepCtx, after, worker.config.BatchSize)
	cancel()
	if ctx.Err() != nil {
		return
	}
	attemptedAt := worker.now()
	if !validRecoveryTime(attemptedAt) {
		worker.recordClockFailure()
		return
	}
	worker.recordSweep(attemptedAt, result, err)
}

func (worker *Worker) recordClockFailure() {
	worker.stateMu.Lock()
	if worker.health.ConsecutiveFailures < math.MaxInt64 {
		worker.health.ConsecutiveFailures++
	}
	worker.stateMu.Unlock()
}

func (worker *Worker) recordSweep(at time.Time, result SweepResult, err error) {
	worker.stateMu.Lock()
	defer worker.stateMu.Unlock()
	at = at.Round(0).UTC()
	worker.health.LastAttemptAt = cloneRecoveryTime(&at)
	if err != nil {
		worker.health.LastFailureAt = cloneRecoveryTime(&at)
		if worker.health.ConsecutiveFailures < math.MaxInt64 {
			worker.health.ConsecutiveFailures++
		}
		return
	}
	worker.health.LastSuccessAt = cloneRecoveryTime(&at)
	worker.health.ConsecutiveFailures = 0
	if result.Complete {
		worker.cursor = DeadlineCursor{}
		worker.health.InitialSweepComplete = true
	} else {
		worker.cursor = result.NextCursor
	}
}

func (worker *Worker) now() time.Time {
	return worker.clock.Now().Round(0).UTC()
}

func (worker *Worker) markStarted(startedAt time.Time) {
	worker.stateMu.Lock()
	worker.cursor = DeadlineCursor{}
	worker.health = WorkerHealth{
		Started:   true,
		Running:   true,
		StartedAt: cloneRecoveryTime(&startedAt),
	}
	worker.stateMu.Unlock()
}

func (worker *Worker) markStopped() {
	worker.stateMu.Lock()
	worker.health.Running = false
	worker.stateMu.Unlock()
}

func workerConfigWithDefaults(config WorkerConfig) WorkerConfig {
	if config.BatchSize == 0 {
		config.BatchSize = DefaultSweepBatchSize
	}
	if config.SweepInterval == 0 {
		config.SweepInterval = DefaultSweepInterval
	}
	if config.SweepTimeout == 0 {
		config.SweepTimeout = DefaultSweepTimeout
	}
	if config.StaleAfter == 0 {
		config.StaleAfter = DefaultSweepStaleAfter
	}
	return config
}

func validWorkerConfig(config WorkerConfig) bool {
	return config.BatchSize > 0 && config.BatchSize <= MaximumSweepBatchSize &&
		config.SweepInterval > 0 && config.SweepTimeout > 0 && config.StaleAfter > 0
}

func waitForSweep(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func validRecoveryTime(value time.Time) bool {
	return domain.IsValidServerTime(value.Round(0).UTC())
}
