package recovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const defaultRecoveryRunnerInterval = 5 * time.Second

var (
	ErrInvalidRecoveryRunnerConfig = errors.New("invalid execution recovery runner config")
	ErrRecoveryRunnerRunning       = errors.New("execution recovery runner is already running")
)

type RecoveryRunnerConfig struct {
	Interval time.Duration
	Observer RecoveryObserver
	Golden   GoldenRecovery
}

type GoldenRecovery interface {
	Recover(ctx context.Context, tournamentID uuid.UUID) error
}

// RecoveryRunner is lifecycle-owned. Its initial scan finishes before Ready
// becomes true, so HTTP never starts while local authoritative recovery is
// still pending.
type RecoveryRunner struct {
	source    RecoveryTournamentSource
	authority RecoveryAuthorityProvider
	recoverer *Recoverer
	clock     ExecutionClock
	config    RecoveryRunnerConfig
	observer  RecoveryObserver

	running atomic.Bool

	healthMu sync.RWMutex
	health   RecoveryRunnerHealth
}

type RecoveryRunnerHealth struct {
	Running             bool
	InitialScanComplete bool
	LastAttemptAt       *time.Time
	LastSuccessAt       *time.Time
	LastFailureAt       *time.Time
}

func NewRecoveryRunner(
	source RecoveryTournamentSource,
	authority RecoveryAuthorityProvider,
	recoverer *Recoverer,
	clock ExecutionClock,
	configs ...RecoveryRunnerConfig,
) (*RecoveryRunner, error) {
	config, valid := recoveryRunnerConfig(configs)
	if source == nil || authority == nil || recoverer == nil || clock == nil || !valid {
		return nil, ErrInvalidRecoveryRunnerConfig
	}
	return &RecoveryRunner{
		source: source, authority: authority, recoverer: recoverer, clock: clock, config: config,
		observer: firstRecoveryObserver(config.Observer),
	}, nil
}

func (runner *RecoveryRunner) Run(ctx context.Context) error {
	if ctx == nil || runner == nil || runner.source == nil || runner.authority == nil ||
		runner.recoverer == nil || runner.clock == nil || runner.config.Interval <= 0 {
		return ErrInvalidRecoveryRunnerConfig
	}
	if !runner.running.CompareAndSwap(false, true) {
		return ErrRecoveryRunnerRunning
	}
	runner.markRunning()
	defer func() {
		runner.running.Store(false)
		runner.markStopped()
	}()

	if err := runner.runScan(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(runner.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := runner.runScan(ctx); err != nil {
				// Initial recovery is a startup gate. After the complete initial
				// scan has succeeded, a dependency outage must make readiness red
				// but leave the lifecycle-owned worker alive for bounded retries.
				if ctx.Err() != nil {
					return nil
				}
			}
		}
	}
}

func (runner *RecoveryRunner) Ready() bool {
	if runner == nil {
		return false
	}
	runner.healthMu.RLock()
	defer runner.healthMu.RUnlock()
	return runner.health.Running && runner.health.InitialScanComplete && runner.health.LastFailureAt == nil
}

func (runner *RecoveryRunner) Health() RecoveryRunnerHealth {
	if runner == nil {
		return RecoveryRunnerHealth{}
	}
	runner.healthMu.RLock()
	defer runner.healthMu.RUnlock()
	return cloneRecoveryRunnerHealth(runner.health)
}

func (runner *RecoveryRunner) runScan(ctx context.Context) error {
	attemptedAt, err := runner.now()
	if err != nil {
		runner.recordFailure(time.Time{})
		return err
	}
	tournaments, err := runner.source.ListRecoveryTournaments(ctx)
	if err != nil {
		runner.recordFailure(attemptedAt)
		return fmt.Errorf("execution recovery runner - list tournaments: %w", err)
	}
	if err := validateRecoveryTournaments(tournaments); err != nil {
		runner.recordFailure(attemptedAt)
		return err
	}
	for _, tournamentID := range tournaments {
		// Golden runtime recovery is fenced by its own transactional runtime
		// head. Run it before execution authority and Swiss recovery so a Golden
		// ready-window or deadline cannot be suppressed by an unrelated execution
		// lease or recovery failure.
		if runner.config.Golden != nil {
			if goldenErr := runner.config.Golden.Recover(ctx, tournamentID); goldenErr != nil {
				runner.emitRecoveryEvent(ctx, tournamentID, 0, RecoveryOutcomeFailure, "scan_failed", "golden_recovery_failed")
				runner.recordFailure(attemptedAt)
				return fmt.Errorf("execution recovery runner - recover Golden: %w", goldenErr)
			}
		}
		identity, owned, authorityErr := runner.authority.RecoveryAuthorityFor(ctx, tournamentID)
		if authorityErr != nil {
			runner.emitRecoveryEvent(ctx, tournamentID, 0, RecoveryOutcomeFailure, "scan_failed", "authority_failed")
			runner.recordFailure(attemptedAt)
			return fmt.Errorf("execution recovery runner - authority: %w", authorityErr)
		}
		if !owned {
			continue
		}
		if identity.Validate() != nil || identity.TournamentID != tournamentID {
			runner.emitRecoveryEvent(ctx, tournamentID, 0, RecoveryOutcomeFailure, "scan_failed", "invalid_authority")
			runner.recordFailure(attemptedAt)
			return domain.ErrInternal
		}
		report, recoverErr := runner.recoverer.Recover(ctx, RecoveryCommand{
			TournamentID: tournamentID,
			Authority:    identity,
		})
		if recoverErr != nil {
			runner.emitRecoveryEvent(
				ctx,
				tournamentID,
				identity.Epoch,
				RecoveryOutcomeFailure,
				"scan_failed",
				"recovery_failed",
			)
			runner.recordFailure(attemptedAt)
			return fmt.Errorf("execution recovery runner - recover: %w", recoverErr)
		}
		if reason, observed := executionRecoverySuccessReason(report); observed {
			runner.emitRecoveryEvent(
				ctx,
				tournamentID,
				identity.Epoch,
				RecoveryOutcomeSuccess,
				"scan_completed",
				reason,
			)
		}
	}
	runner.recordSuccess(attemptedAt)
	return nil
}

func (runner *RecoveryRunner) emitRecoveryEvent(
	ctx context.Context,
	tournamentID uuid.UUID,
	revision int64,
	outcome string,
	transition string,
	reason string,
) {
	if runner == nil || tournamentID == uuid.Nil || revision < 0 {
		return
	}
	observeExecutionRecoverySafely(ctx, runner.observer, RecoveryEvent{
		TournamentID: tournamentID,
		Outcome:      outcome,
		Transition:   transition,
		ReasonCode:   reason,
		Revision:     revision,
	})
}

func (runner *RecoveryRunner) now() (time.Time, error) {
	now := runner.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(now) {
		return time.Time{}, domain.ErrValidation
	}
	return now, nil
}

func (runner *RecoveryRunner) markRunning() {
	runner.healthMu.Lock()
	runner.health.Running = true
	runner.health.InitialScanComplete = false
	runner.health.LastFailureAt = nil
	runner.healthMu.Unlock()
}

func (runner *RecoveryRunner) markStopped() {
	runner.healthMu.Lock()
	runner.health.Running = false
	runner.healthMu.Unlock()
}

func (runner *RecoveryRunner) recordSuccess(at time.Time) {
	runner.healthMu.Lock()
	runner.health.LastAttemptAt = cloneExecutionRecoveryTime(&at)
	runner.health.LastSuccessAt = cloneExecutionRecoveryTime(&at)
	runner.health.LastFailureAt = nil
	// A full successful retry restores readiness after a transient dependency
	// failure. Every scan covers the complete durable tournament inventory.
	runner.health.InitialScanComplete = true
	runner.healthMu.Unlock()
}

func (runner *RecoveryRunner) recordFailure(at time.Time) {
	runner.healthMu.Lock()
	if !at.IsZero() {
		runner.health.LastAttemptAt = cloneExecutionRecoveryTime(&at)
		runner.health.LastFailureAt = cloneExecutionRecoveryTime(&at)
	}
	runner.health.InitialScanComplete = false
	runner.healthMu.Unlock()
}

func recoveryRunnerConfig(configs []RecoveryRunnerConfig) (RecoveryRunnerConfig, bool) {
	if len(configs) > 1 {
		return RecoveryRunnerConfig{}, false
	}
	config := RecoveryRunnerConfig{Interval: defaultRecoveryRunnerInterval}
	if len(configs) == 1 {
		config = configs[0]
		if config.Interval == 0 {
			config.Interval = defaultRecoveryRunnerInterval
		}
	}
	return config, config.Interval > 0
}

func validateRecoveryTournaments(tournaments []uuid.UUID) error {
	seen := make(map[uuid.UUID]struct{}, len(tournaments))
	for _, tournamentID := range tournaments {
		if tournamentID == uuid.Nil {
			return ErrInvalidRecovery
		}
		if _, duplicate := seen[tournamentID]; duplicate {
			return ErrInvalidRecovery
		}
		seen[tournamentID] = struct{}{}
	}
	if !sort.SliceIsSorted(tournaments, func(left, right int) bool {
		return tournaments[left].String() < tournaments[right].String()
	}) {
		return ErrInvalidRecovery
	}
	return nil
}

func cloneRecoveryRunnerHealth(health RecoveryRunnerHealth) RecoveryRunnerHealth {
	clone := health
	clone.LastAttemptAt = cloneExecutionRecoveryTime(health.LastAttemptAt)
	clone.LastSuccessAt = cloneExecutionRecoveryTime(health.LastSuccessAt)
	clone.LastFailureAt = cloneExecutionRecoveryTime(health.LastFailureAt)
	return clone
}

func cloneExecutionRecoveryTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
