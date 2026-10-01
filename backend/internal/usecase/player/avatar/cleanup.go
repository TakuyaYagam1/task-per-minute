package avatar

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	defaultCleanupInterval = time.Minute
	defaultCleanupLease    = 2 * time.Minute
	defaultCleanupTimeout  = 15 * time.Second
	defaultCleanupBatch    = 4
	maxCleanupBackoff      = time.Hour
)

var (
	errCleanupContextUnavailable = errors.New("player avatar cleanup: context unavailable")
	errCleanupDependencies       = errors.New("player avatar cleanup: dependencies unavailable")
	errCleanupAlreadyRunning     = errors.New("player avatar cleanup: worker already running")
)

type CleanupConfig struct {
	Interval      time.Duration
	ClaimLease    time.Duration
	DeleteTimeout time.Duration
	BatchSize     int32
}

type CleanupWorker struct {
	repository Repository
	objects    ObjectStorage
	clock      Clock
	cfg        CleanupConfig
	running    atomic.Bool
	ready      atomic.Bool
}

func NewCleanupWorker(
	repository Repository,
	objects ObjectStorage,
	clock Clock,
	cfg CleanupConfig,
) *CleanupWorker {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultCleanupInterval
	}
	if cfg.ClaimLease <= 0 {
		cfg.ClaimLease = defaultCleanupLease
	}
	if cfg.DeleteTimeout <= 0 {
		cfg.DeleteTimeout = defaultCleanupTimeout
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultCleanupBatch
	}
	return &CleanupWorker{repository: repository, objects: objects, clock: clock, cfg: cfg}
}

func (w *CleanupWorker) Ready() bool {
	return w != nil && w.running.Load() && w.ready.Load()
}

func (w *CleanupWorker) Run(ctx context.Context) error {
	if ctx == nil {
		return errCleanupContextUnavailable
	}
	if w == nil || w.repository == nil || w.objects == nil || w.clock == nil {
		return errCleanupDependencies
	}
	if !w.running.CompareAndSwap(false, true) {
		return errCleanupAlreadyRunning
	}
	defer w.running.Store(false)
	defer w.ready.Store(false)

	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	w.ready.Store(true)
	w.cleanup(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			w.cleanup(ctx)
		}
	}
}

func (w *CleanupWorker) cleanup(ctx context.Context) {
	claimToken, err := uuid.NewRandom()
	if err != nil {
		slog.Error("player avatar cleanup could not create claim token", "error", err)
		return
	}
	now := w.clock.Now().UTC()
	items, err := w.repository.ClaimCleanup(ctx, now, now.Add(w.cfg.ClaimLease), claimToken, w.cfg.BatchSize)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("player avatar cleanup claim failed", "error", err)
		}
		return
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		deleteCtx, cancel := context.WithTimeout(ctx, w.cfg.DeleteTimeout)
		err = w.objects.DeleteAvatar(deleteCtx, item.ObjectKey)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			retryAt := w.clock.Now().UTC().Add(cleanupRetryDelay(item.Attempts))
			if retryErr := w.repository.RetryCleanup(ctx, item.ObjectKey, claimToken, retryAt); retryErr != nil {
				slog.Error("player avatar cleanup retry scheduling failed", "error", retryErr)
			}
			slog.Error("player avatar object deletion failed", "error", err)
			continue
		}
		if err := w.repository.CompleteCleanup(ctx, item.ObjectKey, claimToken); err != nil && ctx.Err() == nil {
			slog.Error("player avatar cleanup completion failed", "error", err)
		}
	}
}

func cleanupRetryDelay(attempts int32) time.Duration {
	delay := 30 * time.Second
	for attempt := int32(0); attempt < attempts && delay < maxCleanupBackoff; attempt++ {
		delay *= 2
	}
	if delay > maxCleanupBackoff {
		return maxCleanupBackoff
	}
	return delay
}
