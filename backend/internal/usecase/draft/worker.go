package draft

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	DefaultDeadlineBatchSize    int32 = 64
	DefaultDeadlinePollInterval       = time.Second
	DefaultDeadlineScanTimeout        = 3 * time.Second
	DefaultDeadlineStaleAfter         = 30 * time.Second
	MaximumDeadlineBatchSize    int32 = 256
)

var (
	ErrInvalidDeadlineWorkerConfig = errors.New("invalid swiss draft deadline worker config")
	ErrDeadlineWorkerRunning       = errors.New("swiss draft deadline worker is already running")
)

// SwissDraftDeadline is the immutable scheduling snapshot used to construct
// one automatic draft action. The timeout use case reloads the aggregate and
// repeats all revision, turn, epoch, and deadline checks before it writes.
type SwissDraftDeadline struct {
	DraftID      uuid.UUID
	RevisionID   uuid.UUID
	Revision     int64
	ServiceEpoch uuid.UUID
	Turn         int
	Deadline     time.Time
}

func (deadline SwissDraftDeadline) Validate() error {
	if deadline.DraftID == uuid.Nil || deadline.RevisionID == uuid.Nil ||
		deadline.ServiceEpoch == uuid.Nil || deadline.Revision < 1 || deadline.Turn < 1 || deadline.Turn > 4 ||
		!domain.IsValidServerTime(deadline.Deadline.Round(0).UTC()) {
		return domain.ErrValidation
	}
	return nil
}

// DeadlineRepository combines the durable due scan with the existing draft
// aggregate repository. Keeping the aggregate methods here makes timeout
// commits use the same CAS and idempotency path as participant actions.
type DeadlineRepository interface {
	Repository
	ListDueSwissDrafts(ctx context.Context, observedAt time.Time, limit int32) ([]SwissDraftDeadline, error)
}

type DeadlineWorkerConfig struct {
	BatchSize    int32
	PollInterval time.Duration
	ScanTimeout  time.Duration
	StaleAfter   time.Duration
}

type DeadlineProcessResult struct {
	Scanned int
	Changed int
}

type DeadlineWorker struct {
	repository DeadlineRepository
	clock      Clock
	timeout    *TimeoutUseCase
	config     DeadlineWorkerConfig

	running atomic.Bool

	stateMu sync.RWMutex
	health  DeadlineWorkerHealth
}

// DeadlineWorkerHealth contains only scheduling liveness metadata and no
// repository error text.
type DeadlineWorkerHealth struct {
	Started             bool
	Running             bool
	InitialScanComplete bool
	StartedAt           *time.Time
	LastAttemptAt       *time.Time
	LastSuccessAt       *time.Time
	LastFailureAt       *time.Time
	ConsecutiveFailures int64
}

func NewDeadlineWorker(
	repository DeadlineRepository,
	clock Clock,
	config DeadlineWorkerConfig,
) (*DeadlineWorker, error) {
	config = deadlineWorkerConfigWithDefaults(config)
	if repository == nil || clock == nil || !validDeadlineWorkerConfig(config) {
		return nil, ErrInvalidDeadlineWorkerConfig
	}
	return &DeadlineWorker{
		repository: repository,
		clock:      clock,
		timeout:    NewTimeoutUseCase(repository, clock),
		config:     config,
	}, nil
}

// Process performs one bounded due scan. A stale snapshot or a competing
// timeout is a benign no-op because Resolve rechecks the immutable aggregate
// and reconciles an already-recorded command.
func (worker *DeadlineWorker) Process(ctx context.Context) (result DeadlineProcessResult, resultErr error) {
	if !validDeadlineWorkerCall(ctx, worker) {
		return DeadlineProcessResult{}, ErrInvalidDeadlineWorkerConfig
	}
	startedAt, ok := worker.now()
	if !ok {
		return DeadlineProcessResult{}, ErrInvalidDeadlineWorkerConfig
	}
	defer func() { worker.recordAttempt(startedAt, resultErr) }()
	if err := ctx.Err(); err != nil {
		return DeadlineProcessResult{}, err
	}

	scanCtx, cancel := context.WithTimeout(ctx, worker.config.ScanTimeout)
	deadlines, err := worker.repository.ListDueSwissDrafts(scanCtx, startedAt, worker.config.BatchSize)
	cancel()
	if err != nil {
		return DeadlineProcessResult{}, fmt.Errorf("swiss draft deadline scan: %w", err)
	}
	if len(deadlines) > int(worker.config.BatchSize) {
		return DeadlineProcessResult{}, errors.New("swiss draft deadline scan exceeded batch size")
	}
	result.Scanned = len(deadlines)

	var processErrors []error
	for _, deadline := range deadlines {
		if err := deadline.Validate(); err != nil {
			processErrors = append(processErrors, fmt.Errorf("invalid swiss draft deadline: %w", err))
			continue
		}
		command := timeoutCommandForDeadline(deadline)
		resolved, err := worker.timeout.Resolve(ctx, command)
		if err != nil {
			if isBenignDeadlineConflict(err) {
				continue
			}
			processErrors = append(processErrors, fmt.Errorf("resolve swiss draft deadline %s: %w", deadline.DraftID, err))
			continue
		}
		if resolved.Changed {
			result.Changed++
		}
	}
	return result, errors.Join(processErrors...)
}

func (worker *DeadlineWorker) Run(ctx context.Context) error {
	if !validDeadlineWorkerCall(ctx, worker) {
		return ErrInvalidDeadlineWorkerConfig
	}
	startedAt, ok := worker.now()
	if !ok {
		return ErrInvalidDeadlineWorkerConfig
	}
	if !worker.running.CompareAndSwap(false, true) {
		return ErrDeadlineWorkerRunning
	}
	worker.markStarted(startedAt)
	defer func() {
		worker.running.Store(false)
		worker.markStopped()
	}()

	backoff := worker.config.PollInterval
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		_, err := worker.Process(ctx)
		if err != nil {
			if !waitDeadlineWorker(ctx, backoff) {
				return nil
			}
			backoff = nextDeadlineBackoff(backoff, worker.config.PollInterval*8)
			continue
		}
		backoff = worker.config.PollInterval
		if !waitDeadlineWorker(ctx, worker.config.PollInterval) {
			return nil
		}
	}
}

func validDeadlineWorkerCall(ctx context.Context, worker *DeadlineWorker) bool {
	return ctx != nil && worker != nil && worker.repository != nil && worker.timeout != nil && worker.clock != nil &&
		validDeadlineWorkerConfig(worker.config)
}

func (worker *DeadlineWorker) Ready() bool {
	if worker == nil {
		return false
	}
	now, ok := worker.now()
	if !ok {
		return false
	}
	health := worker.Health(now)
	return health.Started && health.Running && health.InitialScanComplete &&
		health.LastSuccessAt != nil && health.ConsecutiveFailures == 0 &&
		now.Sub(*health.LastSuccessAt) <= worker.config.StaleAfter
}

func (worker *DeadlineWorker) Health(now time.Time) DeadlineWorkerHealth {
	if worker == nil || !domain.IsValidServerTime(now.Round(0).UTC()) {
		return DeadlineWorkerHealth{}
	}
	worker.stateMu.RLock()
	health := cloneDeadlineWorkerHealth(worker.health)
	worker.stateMu.RUnlock()
	return health
}

func (worker *DeadlineWorker) now() (time.Time, bool) {
	if worker == nil || worker.clock == nil {
		return time.Time{}, false
	}
	now := worker.clock.Now().Round(0).UTC()
	return now, domain.IsValidServerTime(now)
}

func (worker *DeadlineWorker) markStarted(startedAt time.Time) {
	worker.stateMu.Lock()
	worker.health.Started = true
	worker.health.Running = true
	worker.health.InitialScanComplete = false
	worker.health.StartedAt = deadlineTimePointer(startedAt)
	worker.health.LastAttemptAt = nil
	worker.health.LastSuccessAt = nil
	worker.health.LastFailureAt = nil
	worker.health.ConsecutiveFailures = 0
	worker.stateMu.Unlock()
}

func (worker *DeadlineWorker) markStopped() {
	worker.stateMu.Lock()
	worker.health.Running = false
	worker.stateMu.Unlock()
}

func (worker *DeadlineWorker) recordAttempt(attemptedAt time.Time, resultErr error) {
	worker.stateMu.Lock()
	defer worker.stateMu.Unlock()
	worker.health.LastAttemptAt = deadlineTimePointer(attemptedAt)
	if resultErr == nil {
		worker.health.LastSuccessAt = deadlineTimePointer(attemptedAt)
		worker.health.InitialScanComplete = true
		worker.health.LastFailureAt = nil
		worker.health.ConsecutiveFailures = 0
		return
	}
	worker.health.LastFailureAt = deadlineTimePointer(attemptedAt)
	worker.health.ConsecutiveFailures++
}

func timeoutCommandForDeadline(deadline SwissDraftDeadline) TimeoutCommand {
	seed := fmt.Sprintf("%s/%d/%d/%s", deadline.RevisionID, deadline.Revision, deadline.Turn, deadline.Deadline.Round(0).UTC().Format(time.RFC3339Nano))
	commandID := uuid.NewSHA1(deadline.DraftID, []byte("swiss-draft-timeout-command:"+seed))
	return TimeoutCommand{
		DraftID:              deadline.DraftID,
		ExpectedRevisionID:   deadline.RevisionID,
		ExpectedRevision:     deadline.Revision,
		ExpectedServiceEpoch: deadline.ServiceEpoch,
		CurrentServiceEpoch:  deadline.ServiceEpoch,
		ExpectedTurn:         deadline.Turn,
		ExpectedDeadline:     deadline.Deadline.Round(0).UTC(),
		CommandID:            commandID,
		ResultRevisionID:     uuid.NewSHA1(commandID, []byte("result-revision")),
		ActionID:             uuid.NewSHA1(commandID, []byte("action")),
		DecisionEvidenceID:   uuid.NewSHA1(commandID, []byte("decision-evidence")),
	}
}

func isBenignDeadlineConflict(err error) bool {
	return errors.Is(err, ErrTimeoutConflict) || errors.Is(err, ErrTimeoutEarly) || errors.Is(err, ErrNotFound)
}

func deadlineWorkerConfigWithDefaults(config DeadlineWorkerConfig) DeadlineWorkerConfig {
	if config.BatchSize == 0 {
		config.BatchSize = DefaultDeadlineBatchSize
	}
	if config.PollInterval == 0 {
		config.PollInterval = DefaultDeadlinePollInterval
	}
	if config.ScanTimeout == 0 {
		config.ScanTimeout = DefaultDeadlineScanTimeout
	}
	if config.StaleAfter == 0 {
		config.StaleAfter = DefaultDeadlineStaleAfter
	}
	return config
}

func validDeadlineWorkerConfig(config DeadlineWorkerConfig) bool {
	return config.BatchSize > 0 && config.BatchSize <= MaximumDeadlineBatchSize &&
		config.PollInterval > 0 && config.ScanTimeout > 0 && config.StaleAfter > 0
}

func waitDeadlineWorker(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextDeadlineBackoff(current, maximum time.Duration) time.Duration {
	if current <= 0 {
		return maximum
	}
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
}

func cloneDeadlineWorkerHealth(health DeadlineWorkerHealth) DeadlineWorkerHealth {
	health.StartedAt = cloneDeadlineTime(health.StartedAt)
	health.LastAttemptAt = cloneDeadlineTime(health.LastAttemptAt)
	health.LastSuccessAt = cloneDeadlineTime(health.LastSuccessAt)
	health.LastFailureAt = cloneDeadlineTime(health.LastFailureAt)
	return health
}

func deadlineTimePointer(value time.Time) *time.Time {
	cloned := value.Round(0).UTC()
	return &cloned
}

func cloneDeadlineTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	return deadlineTimePointer(*value)
}
