package notification

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"
)

const (
	defaultCleanupInterval = time.Minute
	cleanupAttemptTimeout  = 5 * time.Second
)

var (
	errCleanupContextUnavailable    = errors.New("notification cleanup: context unavailable")
	errCleanupRepositoryUnavailable = errors.New("notification cleanup: repository unavailable")
	errCleanupWorkerRunning         = errors.New("notification cleanup: worker already running")
)

type CleanupWorker struct {
	repository Repository
	interval   time.Duration
	running    atomic.Bool
	ready      atomic.Bool
}

func NewCleanupWorker(repository Repository, interval time.Duration) *CleanupWorker {
	if interval <= 0 {
		interval = defaultCleanupInterval
	}
	return &CleanupWorker{repository: repository, interval: interval}
}

func (w *CleanupWorker) Ready() bool {
	return w != nil && w.running.Load() && w.ready.Load()
}

func (w *CleanupWorker) Run(ctx context.Context) error {
	if ctx == nil {
		return errCleanupContextUnavailable
	}
	if w == nil || w.repository == nil {
		return errCleanupRepositoryUnavailable
	}
	if !w.running.CompareAndSwap(false, true) {
		return errCleanupWorkerRunning
	}
	defer w.running.Store(false)
	defer w.ready.Store(false)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	w.ready.Store(true)
	w.cleanupExpired(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.cleanupExpired(ctx)
		}
	}
}

func (w *CleanupWorker) cleanupExpired(ctx context.Context) {
	cleanupCtx, cancel := context.WithTimeout(ctx, cleanupAttemptTimeout)
	defer cancel()
	if err := w.repository.DeleteExpired(cleanupCtx, expiredCleanupBatch); err != nil && ctx.Err() == nil {
		slog.Error("player notification cleanup batch failed", "error", err)
	}
}
