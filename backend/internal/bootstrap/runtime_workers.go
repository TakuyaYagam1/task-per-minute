package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

const (
	defaultRuntimeStartupTimeout   = 15 * time.Second
	runtimeReadinessPollInterval   = 10 * time.Millisecond
	runtimeWorkerHeartbeatInterval = 5 * time.Second
)

type RuntimeWorker interface {
	Run(context.Context) error
}

type namedRuntimeWorker struct {
	name   string
	worker RuntimeWorker
	ready  func() bool
}

type runtimeWorkerState string

const (
	runtimeWorkerStateCreated  runtimeWorkerState = "created"
	runtimeWorkerStateStarting runtimeWorkerState = "starting"
	runtimeWorkerStateHealthy  runtimeWorkerState = "healthy"
	runtimeWorkerStateStale    runtimeWorkerState = "stale"
	runtimeWorkerStateFailed   runtimeWorkerState = "failed"
	runtimeWorkerStateStopped  runtimeWorkerState = "stopped"
)

type runtimeWorkerHealth struct {
	State runtimeWorkerState
}

type runtimeWorkerHeartbeatTickerFactory func(time.Duration) (<-chan time.Time, func())

type runtimeWorkers struct {
	entries []namedRuntimeWorker

	mu      sync.Mutex
	started bool
	done    chan struct{}
	states  map[string]runtimeWorkerState
	wg      sync.WaitGroup

	heartbeatReporter   observability.RuntimeWorkerHeartbeatReporter
	heartbeatInstanceID uuid.UUID
	heartbeatClock      clockFunc
	heartbeatTicker     runtimeWorkerHeartbeatTickerFactory
	heartbeatWg         sync.WaitGroup
}

func (workers *runtimeWorkers) configureRuntimeWorkerHeartbeats(
	reporter observability.RuntimeWorkerHeartbeatReporter,
	instanceID uuid.UUID,
	clock clockFunc,
) {
	if workers == nil {
		return
	}
	workers.mu.Lock()
	defer workers.mu.Unlock()
	workers.heartbeatReporter = reporter
	workers.heartbeatInstanceID = instanceID
	workers.heartbeatClock = clock
}

func (workers *runtimeWorkers) configureRuntimeWorkerHeartbeatTicker(
	ticker runtimeWorkerHeartbeatTickerFactory,
) {
	if workers == nil || ticker == nil {
		return
	}
	workers.mu.Lock()
	defer workers.mu.Unlock()
	workers.heartbeatTicker = ticker
}

func newRuntimeWorkers(entries ...namedRuntimeWorker) (*runtimeWorkers, error) {
	seen := make(map[string]struct{}, len(entries))
	clean := make([]namedRuntimeWorker, 0, len(entries))
	for _, entry := range entries {
		if entry.name == "" || entry.worker == nil {
			return nil, errors.New("runtime workers: invalid entry")
		}
		if _, duplicate := seen[entry.name]; duplicate {
			return nil, errors.New("runtime workers: duplicate name")
		}
		seen[entry.name] = struct{}{}
		clean = append(clean, entry)
	}
	states := make(map[string]runtimeWorkerState, len(clean))
	for _, entry := range clean {
		states[entry.name] = runtimeWorkerStateCreated
	}
	return &runtimeWorkers{
		entries:         clean,
		states:          states,
		heartbeatTicker: newRuntimeWorkerHeartbeatTicker,
	}, nil
}

func (workers *runtimeWorkers) Start(ctx context.Context) (<-chan error, error) {
	if ctx == nil {
		return nil, errors.New("runtime workers: nil context")
	}
	if workers == nil {
		return nil, nil
	}
	workers.mu.Lock()
	if workers.started {
		workers.mu.Unlock()
		return nil, errors.New("runtime workers: already started")
	}
	workers.started = true
	workers.done = make(chan struct{})
	workers.mu.Unlock()

	// Each worker can emit at most one terminal worker error and one terminal
	// heartbeat error, so the bounded buffer cannot delay either signal.
	errorsCh := make(chan error, len(workers.entries)*2)
	if len(workers.entries) == 0 {
		close(workers.done)
		return nil, nil
	}

	for _, entry := range workers.entries {
		workers.setState(entry.name, runtimeWorkerStateStarting)
		workers.startEntry(ctx, errorsCh, entry)
		if err := waitRuntimeWorkerReady(ctx, errorsCh, entry); err != nil {
			workers.closeWhenStopped()
			return nil, err
		}
		if !workers.markHealthy(entry.name) {
			workers.closeWhenStopped()
			select {
			case err := <-errorsCh:
				return nil, err
			default:
				return nil, fmt.Errorf("runtime worker %s startup stopped", entry.name)
			}
		}
		if err := workers.startWorkerHeartbeat(ctx, errorsCh, entry); err != nil {
			workers.setState(entry.name, runtimeWorkerStateFailed)
			workers.closeWhenStopped()
			return nil, fmt.Errorf("runtime worker %s heartbeat startup: %w", entry.name, err)
		}
	}
	workers.closeWhenStopped()
	return errorsCh, nil
}

func (workers *runtimeWorkers) closeWhenStopped() {
	go func() {
		workers.wg.Wait()
		workers.heartbeatWg.Wait()
		close(workers.done)
	}()
}

func (workers *runtimeWorkers) startWorkerHeartbeat(
	ctx context.Context,
	errorsCh chan<- error,
	entry namedRuntimeWorker,
) error {
	if !workers.hasRuntimeWorkerHeartbeat() {
		return nil
	}
	reported, err := workers.reportRuntimeWorkerHeartbeat(ctx, entry)
	if err != nil {
		return err
	}
	if !reported {
		return errors.New("worker is not ready")
	}
	workers.heartbeatWg.Add(1)
	go func() {
		defer workers.heartbeatWg.Done()
		ticks, stop := workers.runtimeWorkerHeartbeatTicker()
		defer stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticks:
				reported, reportErr := workers.reportRuntimeWorkerHeartbeat(ctx, entry)
				if reportErr != nil {
					workers.setState(entry.name, runtimeWorkerStateFailed)
					reportRuntimeWorkerHeartbeatError(
						ctx,
						errorsCh,
						fmt.Errorf("runtime worker %s heartbeat: %w", entry.name, reportErr),
					)
					return
				}
				if !reported {
					continue
				}
			}
		}
	}()
	return nil
}

func newRuntimeWorkerHeartbeatTicker(interval time.Duration) (<-chan time.Time, func()) {
	ticker := time.NewTicker(interval)
	return ticker.C, ticker.Stop
}

func (workers *runtimeWorkers) runtimeWorkerHeartbeatTicker() (<-chan time.Time, func()) {
	workers.mu.Lock()
	ticker := workers.heartbeatTicker
	workers.mu.Unlock()
	if ticker == nil {
		return newRuntimeWorkerHeartbeatTicker(runtimeWorkerHeartbeatInterval)
	}
	return ticker(runtimeWorkerHeartbeatInterval)
}

func reportRuntimeWorkerHeartbeatError(ctx context.Context, errorsCh chan<- error, err error) {
	select {
	case errorsCh <- err:
		return
	default:
	}
	select {
	case errorsCh <- err:
	case <-ctx.Done():
	}
}

func (workers *runtimeWorkers) hasRuntimeWorkerHeartbeat() bool {
	if workers == nil {
		return false
	}
	workers.mu.Lock()
	defer workers.mu.Unlock()
	return workers.heartbeatReporter != nil && workers.heartbeatInstanceID != uuid.Nil && workers.heartbeatClock != nil
}

func (workers *runtimeWorkers) reportRuntimeWorkerHeartbeat(
	ctx context.Context,
	entry namedRuntimeWorker,
) (bool, error) {
	if workers == nil {
		return false, errors.New("runtime worker heartbeat unavailable")
	}
	workers.mu.Lock()
	state := workers.states[entry.name]
	reporter := workers.heartbeatReporter
	instanceID := workers.heartbeatInstanceID
	clock := workers.heartbeatClock
	workers.mu.Unlock()
	if state != runtimeWorkerStateHealthy || !runtimeWorkerReady(entry.ready) {
		return false, nil
	}
	observedAt, ok := runtimeWorkerHeartbeatTime(clock)
	if !ok {
		return false, errors.New("runtime worker heartbeat clock unavailable")
	}
	if err := reporter.ReportRuntimeWorkerHeartbeat(ctx, observability.RuntimeWorkerHeartbeat{
		InstanceID: instanceID,
		Worker:     entry.name,
		ObservedAt: observedAt,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func runtimeWorkerHeartbeatTime(clock clockFunc) (now time.Time, ok bool) {
	if clock == nil {
		return time.Time{}, false
	}
	defer func() {
		if recover() != nil {
			now = time.Time{}
			ok = false
		}
	}()
	now = clock.Now().Round(0).UTC()
	return now, !now.IsZero()
}

func (workers *runtimeWorkers) startEntry(
	ctx context.Context,
	errorsCh chan<- error,
	entry namedRuntimeWorker,
) {
	workers.wg.Add(1)
	go func() {
		defer workers.wg.Done()
		defer func() {
			if ctx.Err() != nil {
				workers.setState(entry.name, runtimeWorkerStateStopped)
			}
		}()
		var err error
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("panic: %v", recovered)
				}
			}()
			err = entry.worker.Run(ctx)
		}()
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			err = errors.New("stopped unexpectedly")
		}
		workers.setState(entry.name, runtimeWorkerStateFailed)
		errorsCh <- fmt.Errorf("runtime worker %s: %w", entry.name, err)
	}()
}

func waitRuntimeWorkerReady(
	ctx context.Context,
	errorsCh <-chan error,
	entry namedRuntimeWorker,
) error {
	if runtimeWorkerReady(entry.ready) {
		return nil
	}
	timeout := time.NewTimer(defaultRuntimeStartupTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(runtimeReadinessPollInterval)
	defer ticker.Stop()
	for {
		select {
		case err := <-errorsCh:
			return err
		case <-ctx.Done():
			return fmt.Errorf("runtime worker %s startup: %w", entry.name, ctx.Err())
		case <-timeout.C:
			return fmt.Errorf("runtime worker %s startup: readiness timeout", entry.name)
		case <-ticker.C:
			if runtimeWorkerReady(entry.ready) {
				return nil
			}
		}
	}
}

func (workers *runtimeWorkers) WorkerHealth(name string) runtimeWorkerHealth {
	if workers == nil {
		return runtimeWorkerHealth{State: runtimeWorkerStateFailed}
	}
	workers.mu.Lock()
	state, ok := workers.states[name]
	var ready func() bool
	for _, entry := range workers.entries {
		if entry.name == name {
			ready = entry.ready
			break
		}
	}
	workers.mu.Unlock()
	if !ok {
		return runtimeWorkerHealth{State: runtimeWorkerStateFailed}
	}
	if state == runtimeWorkerStateHealthy && !runtimeWorkerReady(ready) {
		state = runtimeWorkerStateStale
	}
	return runtimeWorkerHealth{State: state}
}

func (workers *runtimeWorkers) setState(name string, state runtimeWorkerState) {
	if workers == nil {
		return
	}
	workers.mu.Lock()
	defer workers.mu.Unlock()
	if _, ok := workers.states[name]; ok {
		workers.states[name] = state
	}
}

func (workers *runtimeWorkers) markHealthy(name string) bool {
	if workers == nil {
		return false
	}
	workers.mu.Lock()
	defer workers.mu.Unlock()
	if workers.states[name] != runtimeWorkerStateStarting {
		return false
	}
	workers.states[name] = runtimeWorkerStateHealthy
	return true
}

func runtimeWorkerReady(ready func() bool) (isReady bool) {
	if ready == nil {
		return true
	}
	defer func() {
		if recover() != nil {
			isReady = false
		}
	}()
	return ready()
}

func (workers *runtimeWorkers) Wait(ctx context.Context) error {
	if ctx == nil {
		return errors.New("runtime workers: nil context")
	}
	if workers == nil {
		return nil
	}
	workers.mu.Lock()
	done := workers.done
	started := workers.started
	workers.mu.Unlock()
	if !started || done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("runtime workers: wait: %w", ctx.Err())
	}
}
