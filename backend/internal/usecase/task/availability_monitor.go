package task

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	defaultPollInterval   = time.Second
	defaultMinimumBackoff = 100 * time.Millisecond
	defaultMaximumBackoff = 10 * time.Second
	defaultStaleAfter     = 30 * time.Second
)

type AvailabilityMonitorConfig struct {
	PollInterval   time.Duration
	MinimumBackoff time.Duration
	MaximumBackoff time.Duration
	ScanTimeout    time.Duration
	StaleAfter     time.Duration
	Now            func() time.Time
}

// AvailabilityMonitor verifies that WaveStart's atomic receipt commits remain
// visible as a complete private-task availability graph. It never transports
// task content and must not be used as an on-demand delivery path.
type AvailabilityMonitor struct {
	source BacklogSource
	config AvailabilityMonitorConfig

	healthMu sync.RWMutex
	health   HealthSnapshot
}

func NewAvailabilityMonitor(
	source BacklogSource,
	config AvailabilityMonitorConfig,
) (*AvailabilityMonitor, error) {
	config = availabilityMonitorDefaults(config)
	if source == nil || !validAvailabilityMonitorConfig(config) {
		return nil, ErrInvalidConfig
	}
	return &AvailabilityMonitor{source: source, config: config}, nil
}

// Process performs one bounded durable receipt-availability scan. It never handles private
// task content: the source returns only sanitized backlog cardinality and age.
func (monitor *AvailabilityMonitor) Process(ctx context.Context) (snapshot BacklogSnapshot, resultErr error) {
	if ctx == nil || monitor == nil || monitor.source == nil || !validAvailabilityMonitorConfig(monitor.config) {
		return BacklogSnapshot{}, ErrInvalidConfig
	}
	startedAt, ok := monitor.now()
	if !ok {
		return BacklogSnapshot{}, ErrInvalidConfig
	}
	defer func() { monitor.recordAttempt(startedAt, resultErr) }()
	if err := ctx.Err(); err != nil {
		return BacklogSnapshot{}, err
	}
	scanCtx, cancel := context.WithTimeout(ctx, monitor.config.ScanTimeout)
	defer cancel()
	snapshot, resultErr = monitor.source.TaskDeliveryBacklog(scanCtx)
	if resultErr != nil {
		return BacklogSnapshot{}, errors.Join(ErrBacklogUnavailable, resultErr)
	}
	if err := snapshot.Validate(); err != nil {
		return BacklogSnapshot{}, err
	}
	return snapshot, nil
}

func (monitor *AvailabilityMonitor) Run(ctx context.Context) error {
	if ctx == nil || monitor == nil || monitor.source == nil || !validAvailabilityMonitorConfig(monitor.config) {
		return ErrInvalidConfig
	}
	startedAt, ok := monitor.now()
	if !ok || !monitor.markStarted(startedAt) {
		return ErrMonitorRunning
	}
	defer monitor.markStopped()

	backoff := monitor.config.MinimumBackoff
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if _, err := monitor.Process(ctx); err != nil {
			if !wait(ctx, backoff) {
				return nil
			}
			backoff = nextBackoff(backoff, monitor.config.MaximumBackoff)
			continue
		}
		backoff = monitor.config.MinimumBackoff
		if !wait(ctx, monitor.config.PollInterval) {
			return nil
		}
	}
}

func (monitor *AvailabilityMonitor) Ready() bool {
	if monitor == nil {
		return false
	}
	now, ok := monitor.now()
	if !ok {
		return false
	}
	health := monitor.Health(now)
	return health.Started && health.Running && health.LastSuccessAt != nil &&
		health.ConsecutiveFailures == 0 && !health.Stale
}

func (monitor *AvailabilityMonitor) Health(now time.Time) HealthSnapshot {
	if monitor == nil || now.IsZero() || now.Location() != time.UTC {
		return HealthSnapshot{Stale: true}
	}
	monitor.healthMu.RLock()
	snapshot := cloneHealth(monitor.health)
	monitor.healthMu.RUnlock()
	if snapshot.Running {
		reference := snapshot.StartedAt
		if snapshot.LastSuccessAt != nil {
			reference = snapshot.LastSuccessAt
		}
		snapshot.Stale = reference == nil || now.Before(*reference) || now.Sub(*reference) > monitor.config.StaleAfter
	}
	return snapshot
}

func (monitor *AvailabilityMonitor) markStarted(startedAt time.Time) bool {
	monitor.healthMu.Lock()
	defer monitor.healthMu.Unlock()
	if monitor.health.Running {
		return false
	}
	monitor.health = HealthSnapshot{Started: true, Running: true, StartedAt: timePointer(startedAt)}
	return true
}

func (monitor *AvailabilityMonitor) markStopped() {
	monitor.healthMu.Lock()
	defer monitor.healthMu.Unlock()
	monitor.health.Running = false
}

func (monitor *AvailabilityMonitor) recordAttempt(attemptedAt time.Time, resultErr error) {
	monitor.healthMu.Lock()
	defer monitor.healthMu.Unlock()
	monitor.health.LastAttemptAt = timePointer(attemptedAt)
	if resultErr == nil {
		monitor.health.LastSuccessAt = timePointer(attemptedAt)
		monitor.health.ConsecutiveFailures = 0
		return
	}
	monitor.health.LastFailureAt = timePointer(attemptedAt)
	monitor.health.ConsecutiveFailures++
}

func availabilityMonitorDefaults(config AvailabilityMonitorConfig) AvailabilityMonitorConfig {
	if config.PollInterval == 0 {
		config.PollInterval = defaultPollInterval
	}
	if config.MinimumBackoff == 0 {
		config.MinimumBackoff = defaultMinimumBackoff
	}
	if config.MaximumBackoff == 0 {
		config.MaximumBackoff = defaultMaximumBackoff
	}
	if config.ScanTimeout == 0 {
		config.ScanTimeout = defaultPollInterval
	}
	if config.StaleAfter == 0 {
		config.StaleAfter = defaultStaleAfter
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return config
}

func validAvailabilityMonitorConfig(config AvailabilityMonitorConfig) bool {
	return config.PollInterval > 0 && config.MinimumBackoff > 0 &&
		config.MaximumBackoff >= config.MinimumBackoff && config.ScanTimeout > 0 &&
		config.StaleAfter > 0 && config.Now != nil
}

func (monitor *AvailabilityMonitor) now() (now time.Time, ok bool) {
	defer func() {
		if recover() != nil {
			now = time.Time{}
			ok = false
		}
	}()
	now = monitor.config.Now().Round(0).UTC()
	return now, !now.IsZero()
}

func cloneHealth(snapshot HealthSnapshot) HealthSnapshot {
	cloned := snapshot
	cloned.StartedAt = timePointerValue(snapshot.StartedAt)
	cloned.LastAttemptAt = timePointerValue(snapshot.LastAttemptAt)
	cloned.LastSuccessAt = timePointerValue(snapshot.LastSuccessAt)
	cloned.LastFailureAt = timePointerValue(snapshot.LastFailureAt)
	return cloned
}

func timePointer(value time.Time) *time.Time {
	value = value.Round(0).UTC()
	return &value
}

func timePointerValue(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	return timePointer(*value)
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextBackoff(current time.Duration, maximum time.Duration) time.Duration {
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
}
