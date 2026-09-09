package eventdelivery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultBatchSize       = int32(32)
	defaultLeaseDuration   = 15 * time.Second
	defaultPollInterval    = 250 * time.Millisecond
	defaultMinimumBackoff  = 100 * time.Millisecond
	defaultMaximumBackoff  = 10 * time.Second
	defaultShutdownTimeout = 3 * time.Second
	defaultStaleAfter      = 30 * time.Second
	maximumBatchSize       = int32(256)
)

type WorkerConfig struct {
	WorkerID        uuid.UUID
	BatchSize       int32
	LeaseDuration   time.Duration
	PollInterval    time.Duration
	MinimumBackoff  time.Duration
	MaximumBackoff  time.Duration
	ShutdownTimeout time.Duration
	StaleAfter      time.Duration
	Now             func() time.Time
	NewToken        func() uuid.UUID
}

type ProcessResult struct {
	Claimed      int
	Acknowledged int
	Retried      int
}

type Worker struct {
	store    Store
	sink     Sink
	config   WorkerConfig
	observer WorkerObserver

	healthMu sync.RWMutex
	health   HealthSnapshot
}

func NewWorker(
	store Store,
	sink Sink,
	config WorkerConfig,
	observers ...WorkerObserver,
) (*Worker, error) {
	config = withWorkerDefaults(config)
	if store == nil || sink == nil || !validWorkerConfig(config) {
		return nil, ErrInvalidConfig
	}
	return &Worker{store: store, sink: sink, config: config, observer: firstWorkerObserver(observers...)}, nil
}

func (w *Worker) Process(ctx context.Context) (result ProcessResult, resultErr error) {
	if ctx == nil || w == nil || w.store == nil || w.sink == nil || !validWorkerConfig(w.config) {
		return ProcessResult{}, ErrInvalidConfig
	}
	defer func() {
		w.recordProcessResult(w.config.Now().UTC(), resultErr)
	}()
	if err := ctx.Err(); err != nil {
		return ProcessResult{}, err
	}
	now := w.config.Now().UTC()
	token := w.config.NewToken()
	request := ClaimRequest{
		WorkerID:  w.config.WorkerID,
		Token:     token,
		Limit:     w.config.BatchSize,
		ClaimedAt: now,
		LeaseEnds: now.Add(w.config.LeaseDuration),
	}
	if err := request.Validate(); err != nil {
		return ProcessResult{}, err
	}
	events, err := w.store.Claim(ctx, request)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("event delivery claim: %w", err)
	}
	if len(events) > int(w.config.BatchSize) {
		return ProcessResult{}, ErrRepositoryState
	}
	result = ProcessResult{Claimed: len(events)}
	var deliveryErrors []error
	for index := range events {
		event := events[index].Clone()
		startedAt := w.config.Now().UTC()
		if err := event.Validate(); err != nil {
			retryErr := w.retryInvalidEvent(ctx, token, event, err)
			deliveryErrors = append(deliveryErrors, retryErr)
			result.Retried++
			outcome, transition, reason := deliveryOutcome(
				retryErr,
				"retry_scheduled",
				"invalid_event",
				"retry_failed",
			)
			w.observeEvent(ctx, event, startedAt, outcome, transition, reason)
			continue
		}
		if err := w.sink.Deliver(ctx, event.Clone()); err != nil {
			retryErr := w.retryEvent(ctx, token, event, "sink_failed")
			deliveryErrors = append(deliveryErrors, errors.Join(fmt.Errorf("%w: %w", ErrDeliveryFailed, err), retryErr))
			result.Retried++
			outcome, transition, reason := deliveryOutcome(
				retryErr,
				"retry_scheduled",
				"sink_failed",
				"retry_failed",
			)
			w.observeEvent(ctx, event, startedAt, outcome, transition, reason)
			continue
		}
		acknowledged, err := w.store.Acknowledge(ctx, Acknowledgement{
			EventID: event.ID, WorkerID: w.config.WorkerID, ClaimToken: token,
			AcknowledgedAt: w.config.Now().UTC(),
		})
		if err != nil {
			deliveryErrors = append(deliveryErrors, fmt.Errorf("event delivery acknowledge: %w", err))
			w.observeEvent(ctx, event, startedAt, WorkerOutcomeFailure, "acknowledge_failed", "repository_error")
			continue
		}
		if !acknowledged {
			deliveryErrors = append(deliveryErrors, ErrClaimLost)
			w.observeEvent(ctx, event, startedAt, WorkerOutcomeFailure, "acknowledge_failed", "claim_lost")
			continue
		}
		result.Acknowledged++
		w.observeEvent(ctx, event, startedAt, WorkerOutcomeSuccess, "acknowledged", "delivered")
	}
	return result, errors.Join(deliveryErrors...)
}

func deliveryOutcome(
	err error,
	successTransition string,
	successReason string,
	failureReason string,
) (string, string, string) {
	if err == nil {
		return WorkerOutcomeRetry, successTransition, successReason
	}
	return WorkerOutcomeFailure, "retry_failed", failureReason
}

func (w *Worker) observeEvent(
	ctx context.Context,
	event Event,
	startedAt time.Time,
	outcome string,
	transition string,
	reasonCode string,
) {
	if w == nil || w.observer == nil {
		return
	}
	duration := w.config.Now().UTC().Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	observation := WorkerEvent{
		EventID: event.ID, CorrelationID: event.CorrelationID, TournamentID: event.TournamentID,
		ProjectionRevision: event.ProjectionRevision, Sequence: event.Sequence,
		Outcome: outcome, Transition: transition, ReasonCode: reasonCode, Duration: duration,
	}
	defer func() {
		_ = recover()
	}()
	w.observer.ObserveEventDelivery(ctx, observation)
}

func firstWorkerObserver(observers ...WorkerObserver) WorkerObserver {
	for _, observer := range observers {
		if observer != nil {
			return observer
		}
	}
	return nil
}

func (w *Worker) Run(ctx context.Context) error {
	if ctx == nil || w == nil || w.store == nil || w.sink == nil || !validWorkerConfig(w.config) {
		return ErrInvalidConfig
	}
	if !w.markStarted(w.config.Now().UTC()) {
		return ErrWorkerRunning
	}
	defer w.markStopped()
	defer w.releaseClaims(context.WithoutCancel(ctx))
	backoff := w.config.MinimumBackoff
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		result, err := w.Process(ctx)
		if err != nil {
			if !waitFor(ctx, backoff) {
				return nil
			}
			backoff = nextBackoff(backoff, w.config.MaximumBackoff)
			continue
		}
		backoff = w.config.MinimumBackoff
		if result.Claimed > 0 {
			continue
		}
		if !waitFor(ctx, w.config.PollInterval) {
			return nil
		}
	}
}

func (w *Worker) retryInvalidEvent(
	ctx context.Context,
	token uuid.UUID,
	event Event,
	cause error,
) error {
	return errors.Join(cause, w.retryEvent(ctx, token, event, "invalid_event"))
}

func (w *Worker) retryEvent(
	ctx context.Context,
	token uuid.UUID,
	event Event,
	reason string,
) error {
	delay := retryBackoff(event.AttemptCount, w.config.MinimumBackoff, w.config.MaximumBackoff)
	retried, err := w.store.Retry(ctx, Retry{
		EventID: event.ID, WorkerID: w.config.WorkerID, ClaimToken: token,
		AvailableAt: w.config.Now().UTC().Add(delay), Reason: reason,
	})
	if err != nil {
		return fmt.Errorf("event delivery retry: %w", err)
	}
	if !retried {
		return ErrClaimLost
	}
	return nil
}

func (w *Worker) releaseClaims(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, w.config.ShutdownTimeout)
	defer cancel()
	_ = w.store.ReleaseClaims(ctx, w.config.WorkerID)
}

func withWorkerDefaults(config WorkerConfig) WorkerConfig {
	if config.BatchSize == 0 {
		config.BatchSize = defaultBatchSize
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = defaultLeaseDuration
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaultPollInterval
	}
	if config.MinimumBackoff == 0 {
		config.MinimumBackoff = defaultMinimumBackoff
	}
	if config.MaximumBackoff == 0 {
		config.MaximumBackoff = defaultMaximumBackoff
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = defaultShutdownTimeout
	}
	if config.StaleAfter == 0 {
		config.StaleAfter = defaultStaleAfter
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.NewToken == nil {
		config.NewToken = uuid.New
	}
	return config
}

func validWorkerConfig(config WorkerConfig) bool {
	return config.WorkerID != uuid.Nil && config.BatchSize > 0 &&
		config.BatchSize <= maximumBatchSize && config.LeaseDuration > 0 &&
		config.PollInterval > 0 && config.MinimumBackoff > 0 &&
		config.MaximumBackoff >= config.MinimumBackoff && config.ShutdownTimeout > 0 && config.StaleAfter > 0 &&
		config.Now != nil && config.NewToken != nil
}

func (w *Worker) Health(now time.Time) HealthSnapshot {
	if w == nil || !validServerTime(now) {
		return HealthSnapshot{Stale: true}
	}
	w.healthMu.RLock()
	snapshot := cloneHealthSnapshot(w.health)
	w.healthMu.RUnlock()
	if snapshot.Running {
		reference := snapshot.StartedAt
		if snapshot.LastSuccessAt != nil {
			reference = snapshot.LastSuccessAt
		}
		snapshot.Stale = reference == nil || now.Before(*reference) || now.Sub(*reference) > w.config.StaleAfter
	}
	return snapshot
}

func (w *Worker) markStarted(startedAt time.Time) bool {
	w.healthMu.Lock()
	defer w.healthMu.Unlock()
	if w.health.Running {
		return false
	}
	w.health = HealthSnapshot{
		Started:   true,
		Running:   true,
		StartedAt: cloneTimePointer(startedAt),
	}
	return true
}

func (w *Worker) markStopped() {
	w.healthMu.Lock()
	w.health.Running = false
	w.healthMu.Unlock()
}

func (w *Worker) recordProcessResult(attemptedAt time.Time, err error) {
	w.healthMu.Lock()
	w.health.LastAttemptAt = cloneTimePointer(attemptedAt)
	if err == nil {
		w.health.LastSuccessAt = cloneTimePointer(attemptedAt)
		w.health.ConsecutiveFailures = 0
	} else {
		w.health.LastFailureAt = cloneTimePointer(attemptedAt)
		w.health.ConsecutiveFailures++
	}
	w.healthMu.Unlock()
}

func cloneHealthSnapshot(snapshot HealthSnapshot) HealthSnapshot {
	snapshot.StartedAt = cloneOptionalTime(snapshot.StartedAt)
	snapshot.LastAttemptAt = cloneOptionalTime(snapshot.LastAttemptAt)
	snapshot.LastSuccessAt = cloneOptionalTime(snapshot.LastSuccessAt)
	snapshot.LastFailureAt = cloneOptionalTime(snapshot.LastFailureAt)
	return snapshot
}

func cloneOptionalTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	return cloneTimePointer(*value)
}

func cloneTimePointer(value time.Time) *time.Time {
	clone := value
	return &clone
}

func retryBackoff(attempt int32, minimum, maximum time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	backoff := minimum
	for current := int32(1); current < attempt && backoff < maximum; current++ {
		backoff = nextBackoff(backoff, maximum)
	}
	return backoff
}

func nextBackoff(current, maximum time.Duration) time.Duration {
	if current >= maximum || current > maximum/2 {
		return maximum
	}
	return current * 2
}

func waitFor(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

var _ HealthSource = (*Worker)(nil)
