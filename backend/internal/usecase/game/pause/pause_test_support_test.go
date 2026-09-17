package pause_test

import (
	"context"
	"sync"
	"testing"
	"time"

	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	entermocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/enter/mocks"
	"github.com/stretchr/testify/mock"
)

func newPauseTransactionManager(t *testing.T) *entermocks.MockTransactionManager {
	t.Helper()
	transactions := entermocks.NewMockTransactionManager(t)
	transactions.EXPECT().
		Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		}).
		Maybe()
	return transactions
}

func newPauseClock(t *testing.T, now time.Time) *entermocks.MockPauseClock {
	t.Helper()
	clock := entermocks.NewMockPauseClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

type countingTransactionManager struct {
	*entermocks.MockTransactionManager

	mu    sync.Mutex
	calls int
}

func newCountingTransactionManager(t *testing.T) *countingTransactionManager {
	t.Helper()
	harness := &countingTransactionManager{}
	transactions := entermocks.NewMockTransactionManager(t)
	transactions.EXPECT().
		Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			harness.mu.Lock()
			harness.calls++
			harness.mu.Unlock()
			return fn(ctx)
		}).
		Maybe()
	harness.MockTransactionManager = transactions
	return harness
}

func (m *countingTransactionManager) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

type sequenceClock struct {
	*entermocks.MockPauseClock

	mu    sync.Mutex
	times []time.Time
	calls int
}

func newSequenceClock(t *testing.T, times ...time.Time) *sequenceClock {
	t.Helper()
	harness := &sequenceClock{times: append([]time.Time(nil), times...)}
	clock := entermocks.NewMockPauseClock(t)
	clock.EXPECT().Now().RunAndReturn(harness.current).Maybe()
	harness.MockPauseClock = clock
	return harness
}

func (c *sequenceClock) current() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	index := c.calls
	if index >= len(c.times) {
		index = len(c.times) - 1
	}
	c.calls++
	return c.times[index]
}

func (c *sequenceClock) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func cloneTournamentRecord(record gameusecase.TournamentRecord) *gameusecase.TournamentRecord {
	cloned := record
	if record.PausedFromState != nil {
		state := *record.PausedFromState
		cloned.PausedFromState = &state
	}
	if record.StartedAt != nil {
		value := *record.StartedAt
		cloned.StartedAt = &value
	}
	if record.FinishedAt != nil {
		value := *record.FinishedAt
		cloned.FinishedAt = &value
	}
	return &cloned
}
