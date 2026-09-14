package connection

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
)

const (
	defaultOrphanRecoveryInterval = 5 * time.Second
	defaultOrphanRecoveryBatch    = 64
)

var (
	ErrInvalidReaperConfiguration = errors.New("invalid participant connection reaper configuration")
	ErrReaperRunning              = errors.New("participant connection reaper is already running")
)

type ReaperConfig struct {
	Interval  time.Duration
	BatchSize int32
}

func defaultReaperConfig(config ReaperConfig) (ReaperConfig, bool) {
	if config.Interval == 0 {
		config.Interval = defaultOrphanRecoveryInterval
	}
	if config.BatchSize == 0 {
		config.BatchSize = defaultOrphanRecoveryBatch
	}
	return config, config.Interval > 0 && config.BatchSize > 0
}

// Reaper is deliberately connection-owned.  It discovers only stamped
// expired leases and delegates each candidate to Coordinator, keeping the
// participant lock order and nested action in one transaction.
type Reaper struct {
	coordinator *Coordinator
	repository  RecoveryRepository
	authority   RecoveryAuthorityProvider
	config      ReaperConfig
	running     atomic.Bool
	ready       atomic.Bool
}

func NewReaper(
	coordinator *Coordinator,
	repository RecoveryRepository,
	authority RecoveryAuthorityProvider,
	configs ...ReaperConfig,
) (*Reaper, error) {
	config := ReaperConfig{}
	if len(configs) > 0 {
		config = configs[0]
	}
	config, valid := defaultReaperConfig(config)
	if coordinator == nil || repository == nil || authority == nil || !valid {
		return nil, ErrInvalidReaperConfiguration
	}
	return &Reaper{coordinator: coordinator, repository: repository, authority: authority, config: config}, nil
}

func (reaper *Reaper) Run(ctx context.Context) error {
	if ctx == nil || reaper == nil || reaper.coordinator == nil || reaper.repository == nil ||
		reaper.authority == nil || reaper.config.Interval <= 0 || reaper.config.BatchSize <= 0 {
		return ErrInvalidReaperConfiguration
	}
	if !reaper.running.CompareAndSwap(false, true) {
		return ErrReaperRunning
	}
	reaper.ready.Store(false)
	defer func() {
		reaper.ready.Store(false)
		reaper.running.Store(false)
	}()

	if err := reaper.runScan(ctx); err != nil {
		return err
	}
	reaper.ready.Store(true)
	ticker := time.NewTicker(reaper.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := reaper.runScan(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				// Keep the worker alive for a transient database or ownership
				// outage.  Runtime readiness goes red until the next successful scan.
				reaper.ready.Store(false)
				continue
			}
			reaper.ready.Store(true)
		}
	}
}

func (reaper *Reaper) Ready() bool {
	return reaper != nil && reaper.running.Load() && reaper.ready.Load()
}

func (reaper *Reaper) runScan(ctx context.Context) error {
	if err := reaper.renewActiveAuthorities(ctx); err != nil {
		return err
	}
	candidates, err := reaper.repository.ListParticipantConnectionRecoveryCandidates(ctx, reaper.config.BatchSize)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err := reaper.coordinator.recoverOrphanedConnection(ctx, candidate); err != nil {
			if errors.Is(err, authorityusecase.ErrNotOwner) {
				// A different live replica owns this tournament.  Leave the
				// candidate as evidence and retry after the next expiry scan.
				continue
			}
			return err
		}
	}
	return nil
}

func (reaper *Reaper) renewActiveAuthorities(ctx context.Context) error {
	tournamentIDs, err := reaper.repository.ListParticipantConnectionLeaseTournaments(ctx)
	if err != nil {
		return fmt.Errorf("list participant connection lease tournaments: %w", err)
	}
	for _, tournamentID := range tournamentIDs {
		if tournamentID == uuid.Nil {
			return domain.ErrInternal
		}
		identity, owned, authorityErr := reaper.authority.RecoveryAuthorityFor(ctx, tournamentID)
		if authorityErr != nil {
			return fmt.Errorf("renew participant connection authority: %w", authorityErr)
		}
		if owned && (identity.Validate() != nil || identity.TournamentID != tournamentID) {
			return domain.ErrInternal
		}
	}
	return nil
}
