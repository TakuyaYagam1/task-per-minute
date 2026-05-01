package duel

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

type TimerRegistry struct {
	tx      usecase.TxManager
	duels   usecase.DuelRepo
	players usecase.PlayerRepo
	clock   clock.Clock
	timers  sync.Map // map[uuid.UUID]*timerEntry
}

type timerEntry struct {
	mu        sync.Mutex
	timer     *time.Timer
	deadline  time.Time
	remaining time.Duration
	paused    bool
	done      bool
	onExpire  func()
}

func NewTimerRegistry(
	tx usecase.TxManager,
	duels usecase.DuelRepo,
	players usecase.PlayerRepo,
	clk clock.Clock,
) *TimerRegistry {
	if clk == nil {
		clk = clock.Real{}
	}
	return &TimerRegistry{
		tx:      tx,
		duels:   duels,
		players: players,
		clock:   clk,
	}
}

func (r *TimerRegistry) Start(duelID uuid.UUID, deadline time.Time, onExpire func()) {
	r.Stop(duelID)

	entry := &timerEntry{deadline: deadline, onExpire: onExpire}
	r.timers.Store(duelID, entry)

	delay := deadline.Sub(r.clock.Now())
	if delay < 0 {
		delay = 0
	}

	entry.mu.Lock()
	if !entry.done {
		entry.timer = time.AfterFunc(delay, func() {
			r.expire(duelID, entry)
		})
	}
	entry.mu.Unlock()
}

func (r *TimerRegistry) Stop(duelID uuid.UUID) bool {
	if r == nil {
		return false
	}
	raw, ok := r.timers.Load(duelID)
	if !ok {
		return false
	}
	entry := raw.(*timerEntry)
	if !entry.markDone() {
		return false
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	r.timers.CompareAndDelete(duelID, entry)
	return true
}

func (r *TimerRegistry) Freeze(duelID uuid.UUID, pausedAt time.Time) bool {
	raw, ok := r.timers.Load(duelID)
	if !ok {
		return false
	}

	entry := raw.(*timerEntry)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.done {
		return false
	}
	if entry.paused {
		return true
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.remaining = entry.deadline.Sub(pausedAt)
	if entry.remaining < 0 {
		entry.remaining = 0
	}
	entry.paused = true
	return true
}

func (r *TimerRegistry) Resume(duelID uuid.UUID, resumedAt time.Time) (time.Time, bool) {
	raw, ok := r.timers.Load(duelID)
	if !ok {
		return time.Time{}, false
	}

	entry := raw.(*timerEntry)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.done || !entry.paused {
		return time.Time{}, false
	}

	delay := entry.remaining
	entry.deadline = resumedAt.Add(delay)
	entry.remaining = 0
	entry.paused = false
	entry.timer = time.AfterFunc(delay, func() {
		r.expire(duelID, entry)
	})
	return entry.deadline, true
}

func (r *TimerRegistry) expire(duelID uuid.UUID, entry *timerEntry) {
	if !entry.markDone() {
		return
	}
	r.timers.CompareAndDelete(duelID, entry)

	finished, err := r.finishExpired(context.Background(), duelID)
	if err != nil || finished == nil {
		return
	}
	if entry.onExpire != nil {
		entry.onExpire()
	}
}

func (r *TimerRegistry) finishExpired(ctx context.Context, duelID uuid.UUID) (*domain.Duel, error) {
	return finalizeDuel(ctx, r.tx, r.duels, r.players, r.clock.Now(), duelID, nil)
}

func (e *timerEntry) markDone() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.done {
		return false
	}
	e.done = true
	return true
}
