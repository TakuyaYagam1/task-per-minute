package game_test

import (
	"context"
	"sync"
	"testing"
	"time"

	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
	"github.com/stretchr/testify/mock"
)

func newPauseTransactionManager(t *testing.T) *gamemocks.MockTransactionManager {
	t.Helper()
	transactions := gamemocks.NewMockTransactionManager(t)
	transactions.EXPECT().
		Do(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		}).
		Maybe()
	return transactions
}

func newPauseClock(t *testing.T, now time.Time) *gamemocks.MockPauseClock {
	t.Helper()
	clock := gamemocks.NewMockPauseClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

type countingTransactionManager struct {
	*gamemocks.MockTransactionManager

	mu    sync.Mutex
	calls int
}

func newCountingTransactionManager(t *testing.T) *countingTransactionManager {
	t.Helper()
	harness := &countingTransactionManager{}
	transactions := gamemocks.NewMockTransactionManager(t)
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
	*gamemocks.MockPauseClock

	mu    sync.Mutex
	times []time.Time
	calls int
}

func newSequenceClock(t *testing.T, times ...time.Time) *sequenceClock {
	t.Helper()
	harness := &sequenceClock{times: append([]time.Time(nil), times...)}
	clock := gamemocks.NewMockPauseClock(t)
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
