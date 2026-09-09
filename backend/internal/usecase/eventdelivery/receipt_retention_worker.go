package eventdelivery

import (
	"context"
	"sync"
	"time"
)

const (
	defaultReceiptRetention        = 30 * 24 * time.Hour
	defaultReceiptCleanupInterval  = 5 * time.Minute
	defaultReceiptCleanupBatchSize = int32(128)
)

// ReceiptRetentionWorker periodically removes only receipt and subscriber
// rows whose retention window has expired. Outbox events and their immutable
// source evidence are never part of this maintenance path.
type ReceiptRetentionWorker struct {
	store  ReceiptRetentionStore
	config ReceiptRetentionWorkerConfig

	stateMu sync.RWMutex
	running bool
	ready   bool
}

type ReceiptRetentionWorkerConfig struct {
	Retention time.Duration
	Interval  time.Duration
	BatchSize int32
	Now       func() time.Time
}

func NewReceiptRetentionWorker(
	store ReceiptRetentionStore,
	config ReceiptRetentionWorkerConfig,
) (*ReceiptRetentionWorker, error) {
	config = receiptRetentionWorkerDefaults(config)
	if store == nil || !validReceiptRetentionWorkerConfig(config) {
		return nil, ErrInvalidConfig
	}
	return &ReceiptRetentionWorker{store: store, config: config}, nil
}

// Process executes one bounded cleanup transaction. A failed cleanup leaves
// the next run to retry from the same durable state.
func (worker *ReceiptRetentionWorker) Process(ctx context.Context) (ReceiptRetentionResult, error) {
	if ctx == nil || worker == nil || worker.store == nil || !validReceiptRetentionWorkerConfig(worker.config) {
		return ReceiptRetentionResult{}, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return ReceiptRetentionResult{}, err
	}
	now := worker.config.Now().UTC()
	request := ReceiptRetentionRequest{
		CutoffAt:  now.Add(-worker.config.Retention),
		BatchSize: worker.config.BatchSize,
	}
	if err := request.Validate(); err != nil {
		return ReceiptRetentionResult{}, err
	}
	result, err := worker.store.PruneClosedSubscribers(ctx, request)
	if err != nil {
		return ReceiptRetentionResult{}, err
	}
	if err := result.Validate(); err != nil {
		return ReceiptRetentionResult{}, err
	}
	return result, nil
}

func (worker *ReceiptRetentionWorker) Run(ctx context.Context) error {
	if ctx == nil || worker == nil || worker.store == nil || !validReceiptRetentionWorkerConfig(worker.config) {
		return ErrInvalidConfig
	}
	if !worker.markRunning() {
		return ErrWorkerRunning
	}
	defer worker.markStopped()

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if _, err := worker.Process(ctx); err == nil {
			worker.markReady()
		} else {
			worker.markNotReady()
		}
		if !waitFor(ctx, worker.config.Interval) {
			return nil
		}
	}
}

func (worker *ReceiptRetentionWorker) Ready() bool {
	if worker == nil {
		return false
	}
	worker.stateMu.RLock()
	defer worker.stateMu.RUnlock()
	return worker.running && worker.ready
}

func (worker *ReceiptRetentionWorker) markRunning() bool {
	worker.stateMu.Lock()
	defer worker.stateMu.Unlock()
	if worker.running {
		return false
	}
	worker.running = true
	worker.ready = false
	return true
}

func (worker *ReceiptRetentionWorker) markReady() {
	worker.stateMu.Lock()
	worker.ready = true
	worker.stateMu.Unlock()
}

func (worker *ReceiptRetentionWorker) markNotReady() {
	worker.stateMu.Lock()
	worker.ready = false
	worker.stateMu.Unlock()
}

func (worker *ReceiptRetentionWorker) markStopped() {
	worker.stateMu.Lock()
	worker.running = false
	worker.stateMu.Unlock()
}

func receiptRetentionWorkerDefaults(config ReceiptRetentionWorkerConfig) ReceiptRetentionWorkerConfig {
	if config.Retention == 0 {
		config.Retention = defaultReceiptRetention
	}
	if config.Interval == 0 {
		config.Interval = defaultReceiptCleanupInterval
	}
	if config.BatchSize == 0 {
		config.BatchSize = defaultReceiptCleanupBatchSize
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return config
}

func validReceiptRetentionWorkerConfig(config ReceiptRetentionWorkerConfig) bool {
	return config.Retention > 0 && config.Interval > 0 &&
		config.BatchSize > 0 && config.BatchSize <= maximumBatchSize && config.Now != nil
}
