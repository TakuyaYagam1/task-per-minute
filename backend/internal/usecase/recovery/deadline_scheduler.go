package recovery

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidScheduler    = errors.New("invalid recovery deadline scheduler")
	ErrSchedulerRunning    = errors.New("recovery deadline scheduler is already running")
	ErrSchedulerNotRunning = errors.New("recovery deadline scheduler is not running")
	ErrDeadlineArmConflict = errors.New("recovery deadline arm conflicts with current revision")
)

const deadlineCommandBuffer = int(MaximumSweepBatchSize)

const DefaultDeadlineHandlerRetryInterval = time.Second

const (
	schedulerCreated = int32(iota)
	schedulerRunning
	schedulerStopped
)

type deadlineKey struct {
	kind DeadlineKind
	id   uuid.UUID
}

type deadlineArmCommand struct {
	deadline PendingDeadline
	reply    chan deadlineArmResult
}

type deadlineArmResult struct {
	changed bool
	err     error
}

type scheduledDeadline struct {
	deadline  PendingDeadline
	nextRunAt time.Time
}

type DeadlineSchedulerConfig struct {
	HandlerRetryInterval time.Duration
}

// DeadlineExecutionHealth contains no handler error text and is safe to merge
// into a process readiness response.
type DeadlineExecutionHealth struct {
	Running             bool
	LastAttemptAt       *time.Time
	LastSuccessAt       *time.Time
	LastFailureAt       *time.Time
	ConsecutiveFailures int64
}

type DeadlineExecutionHealthSource interface {
	ExecutionHealth() DeadlineExecutionHealth
}

// DeadlineScheduler owns process-local timers. Durable state remains the
// source of truth, and DeadlineHandler must compare-and-set ExpectedRevision,
// so simultaneous workers and shutdown races cannot duplicate terminal state.
type DeadlineScheduler struct {
	handler       DeadlineHandler
	retryInterval time.Duration
	commands      chan deadlineArmCommand
	done          chan struct{}
	state         atomic.Int32

	healthMu sync.RWMutex
	health   DeadlineExecutionHealth
}

var _ DeadlineArmSink = (*DeadlineScheduler)(nil)
var _ DeadlineExecutionHealthSource = (*DeadlineScheduler)(nil)

func NewDeadlineScheduler(
	handler DeadlineHandler,
	configs ...DeadlineSchedulerConfig,
) (*DeadlineScheduler, error) {
	config, valid := deadlineSchedulerConfig(configs)
	if handler == nil || !valid {
		return nil, ErrInvalidScheduler
	}
	return &DeadlineScheduler{
		handler:       handler,
		retryInterval: config.HandlerRetryInterval,
		commands:      make(chan deadlineArmCommand, deadlineCommandBuffer),
		done:          make(chan struct{}),
	}, nil
}

func (scheduler *DeadlineScheduler) Run(ctx context.Context) error {
	if ctx == nil || scheduler == nil || scheduler.handler == nil {
		return ErrInvalidScheduler
	}
	if !scheduler.state.CompareAndSwap(schedulerCreated, schedulerRunning) {
		return ErrSchedulerRunning
	}
	scheduler.markExecutionStarted()
	defer func() {
		scheduler.state.Store(schedulerStopped)
		scheduler.markExecutionStopped()
		close(scheduler.done)
	}()

	armed := make(map[deadlineKey]scheduledDeadline)
	var timer *time.Timer
	defer func() { stopDeadlineTimer(timer) }()

	for {
		timer = resetDeadlineTimer(timer, nextDeadlineDelay(armed))
		var timerC <-chan time.Time
		if timer != nil {
			timerC = timer.C
		}

		select {
		case <-ctx.Done():
			return nil
		case command := <-scheduler.commands:
			changed, err := registerDeadline(armed, command.deadline)
			command.reply <- deadlineArmResult{changed: changed, err: err}
		case now := <-timerC:
			scheduler.handleDue(ctx, armed, now.Round(0).UTC())
		}
	}
}

func (scheduler *DeadlineScheduler) ArmDeadline(
	ctx context.Context,
	deadline PendingDeadline,
) (bool, error) {
	if ctx == nil || scheduler == nil || deadline.Validate() != nil {
		return false, ErrInvalidDeadline
	}
	if scheduler.state.Load() != schedulerRunning {
		return false, ErrSchedulerNotRunning
	}
	command := deadlineArmCommand{deadline: deadline, reply: make(chan deadlineArmResult, 1)}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-scheduler.done:
		return false, ErrSchedulerNotRunning
	case scheduler.commands <- command:
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-scheduler.done:
		return false, ErrSchedulerNotRunning
	case result := <-command.reply:
		return result.changed, result.err
	}
}

func (scheduler *DeadlineScheduler) handleDue(
	ctx context.Context,
	armed map[deadlineKey]scheduledDeadline,
	now time.Time,
) {
	due := make([]scheduledDeadline, 0)
	for _, scheduled := range armed {
		if !scheduled.nextRunAt.After(now) {
			due = append(due, scheduled)
		}
	}
	sort.Slice(due, func(left, right int) bool {
		if !due[left].nextRunAt.Equal(due[right].nextRunAt) {
			return due[left].nextRunAt.Before(due[right].nextRunAt)
		}
		leftCursor := due[left].deadline.Cursor()
		rightCursor := due[right].deadline.Cursor()
		if leftCursor.Kind != rightCursor.Kind {
			return deadlineKindOrder(leftCursor.Kind) < deadlineKindOrder(rightCursor.Kind)
		}
		return leftCursor.ID.String() < rightCursor.ID.String()
	})
	for index := range due {
		if ctx.Err() != nil {
			return
		}
		scheduled := due[index]
		key := pendingDeadlineKey(scheduled.deadline)
		_, err := scheduler.handler.HandleDeadline(ctx, scheduled.deadline)
		attemptedAt := time.Now().Round(0).UTC()
		scheduler.recordExecutionAttempt(attemptedAt, err)
		if err != nil {
			scheduled.nextRunAt = attemptedAt.Add(scheduler.retryInterval)
			armed[key] = scheduled
			continue
		}
		delete(armed, key)
	}
}

func registerDeadline(armed map[deadlineKey]scheduledDeadline, deadline PendingDeadline) (bool, error) {
	if deadline.Validate() != nil {
		return false, ErrInvalidDeadline
	}
	key := pendingDeadlineKey(deadline)
	scheduled, exists := armed[key]
	if !exists {
		armed[key] = scheduledDeadline{deadline: deadline, nextRunAt: deadline.DueAt}
		return true, nil
	}
	current := scheduled.deadline
	if deadline.ExpectedRevision < current.ExpectedRevision {
		return false, nil
	}
	if deadline.ExpectedRevision == current.ExpectedRevision {
		if pendingDeadlinesEqual(current, deadline) {
			return false, nil
		}
		return false, ErrDeadlineArmConflict
	}
	armed[key] = scheduledDeadline{deadline: deadline, nextRunAt: deadline.DueAt}
	return true, nil
}

func nextDeadlineDelay(armed map[deadlineKey]scheduledDeadline) time.Duration {
	if len(armed) == 0 {
		return -1
	}
	var next time.Time
	for _, scheduled := range armed {
		if next.IsZero() || scheduled.nextRunAt.Before(next) {
			next = scheduled.nextRunAt
		}
	}
	delay := time.Until(next)
	if delay < 0 {
		return 0
	}
	return delay
}

func deadlineSchedulerConfig(configs []DeadlineSchedulerConfig) (DeadlineSchedulerConfig, bool) {
	if len(configs) > 1 {
		return DeadlineSchedulerConfig{}, false
	}
	config := DeadlineSchedulerConfig{HandlerRetryInterval: DefaultDeadlineHandlerRetryInterval}
	if len(configs) == 1 {
		config = configs[0]
		if config.HandlerRetryInterval == 0 {
			config.HandlerRetryInterval = DefaultDeadlineHandlerRetryInterval
		}
	}
	return config, config.HandlerRetryInterval > 0
}

func (scheduler *DeadlineScheduler) ExecutionHealth() DeadlineExecutionHealth {
	if scheduler == nil {
		return DeadlineExecutionHealth{}
	}
	scheduler.healthMu.RLock()
	health := cloneDeadlineExecutionHealth(scheduler.health)
	scheduler.healthMu.RUnlock()
	return health
}

func (scheduler *DeadlineScheduler) markExecutionStarted() {
	scheduler.healthMu.Lock()
	scheduler.health.Running = true
	scheduler.healthMu.Unlock()
}

func (scheduler *DeadlineScheduler) markExecutionStopped() {
	scheduler.healthMu.Lock()
	scheduler.health.Running = false
	scheduler.healthMu.Unlock()
}

func (scheduler *DeadlineScheduler) recordExecutionAttempt(attemptedAt time.Time, err error) {
	scheduler.healthMu.Lock()
	defer scheduler.healthMu.Unlock()
	attemptedAt = attemptedAt.Round(0).UTC()
	scheduler.health.LastAttemptAt = cloneRecoveryTime(&attemptedAt)
	if err != nil {
		scheduler.health.LastFailureAt = cloneRecoveryTime(&attemptedAt)
		if scheduler.health.ConsecutiveFailures < int64(^uint64(0)>>1) {
			scheduler.health.ConsecutiveFailures++
		}
		return
	}
	scheduler.health.LastSuccessAt = cloneRecoveryTime(&attemptedAt)
	scheduler.health.ConsecutiveFailures = 0
}

func cloneDeadlineExecutionHealth(health DeadlineExecutionHealth) DeadlineExecutionHealth {
	health.LastAttemptAt = cloneRecoveryTime(health.LastAttemptAt)
	health.LastSuccessAt = cloneRecoveryTime(health.LastSuccessAt)
	health.LastFailureAt = cloneRecoveryTime(health.LastFailureAt)
	return health
}

func resetDeadlineTimer(timer *time.Timer, delay time.Duration) *time.Timer {
	if delay < 0 {
		stopDeadlineTimer(timer)
		return nil
	}
	if timer == nil {
		return time.NewTimer(delay)
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
	return timer
}

func stopDeadlineTimer(timer *time.Timer) {
	if timer != nil {
		timer.Stop()
	}
}

func pendingDeadlineKey(deadline PendingDeadline) deadlineKey {
	return deadlineKey{kind: deadline.Kind, id: deadline.ID}
}

func pendingDeadlinesEqual(left, right PendingDeadline) bool {
	return left.Kind == right.Kind && left.ID == right.ID &&
		left.TournamentID == right.TournamentID && left.RosterID == right.RosterID &&
		left.WaveID == right.WaveID && left.SeriesID == right.SeriesID &&
		left.SlotID == right.SlotID && left.GameID == right.GameID &&
		left.PauseID == right.PauseID &&
		left.ReadyWindowRevisionID == right.ReadyWindowRevisionID &&
		left.ParticipantID == right.ParticipantID &&
		left.ExpectedRevision == right.ExpectedRevision && left.DueAt.Equal(right.DueAt)
}
