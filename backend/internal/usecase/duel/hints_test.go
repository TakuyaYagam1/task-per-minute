package duel_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

func TestHintScheduler_PlayerSnapshotIncludesMissedHints(t *testing.T) {
	t.Parallel()

	duelID := uuid.New()
	playerID := uuid.New()
	task := hintTestTask(1)
	duel := &domain.Duel{
		ID:        duelID,
		Player1ID: playerID,
		Deadline:  time.Now().Add(time.Second),
		StartedAt: time.Now().Add(-time.Second),
	}

	scheduler := duelusecase.NewHintScheduler(clock.Real{}, nil)
	scheduler.StartDuel(duel, map[uuid.UUID]*domain.Task{playerID: task})
	t.Cleanup(func() {
		scheduler.StopDuel(duelID)
	})

	require.Eventually(t, func() bool {
		snapshot, ok := scheduler.PlayerSnapshot(duelID, playerID)
		return ok && len(snapshot.Unlocked) == 3
	}, time.Second, 10*time.Millisecond)

	snapshot, ok := scheduler.PlayerSnapshot(duelID, playerID)
	require.True(t, ok)
	require.Equal(t, []domain.UnlockedHint{
		{Index: 1, Text: "hint 1", UnlockedAt: duel.StartedAt.Add(250 * time.Millisecond)},
		{Index: 2, Text: "hint 2", UnlockedAt: duel.StartedAt.Add(500 * time.Millisecond)},
		{Index: 3, Text: "hint 3", UnlockedAt: duel.StartedAt.Add(750 * time.Millisecond)},
	}, snapshot.Unlocked)
}

func TestHintScheduler_FreezeResumeShiftsFutureUnlocks(t *testing.T) {
	t.Parallel()

	startedAt := time.Now().Add(time.Hour)
	pausedAt := startedAt.Add(100 * time.Millisecond)
	resumedAt := startedAt.Add(5 * time.Minute)
	duelID := uuid.New()
	playerID := uuid.New()
	duel := &domain.Duel{
		ID:        duelID,
		Player1ID: playerID,
		Deadline:  startedAt.Add(10 * time.Second),
		StartedAt: startedAt,
	}

	scheduler := duelusecase.NewHintScheduler(clock.Real{}, nil)
	scheduler.StartDuel(duel, map[uuid.UUID]*domain.Task{playerID: hintTestTask(4)})
	t.Cleanup(func() {
		scheduler.StopDuel(duelID)
	})

	before, ok := scheduler.PlayerSnapshot(duelID, playerID)
	require.True(t, ok)
	require.Len(t, before.Schedule, 3)

	require.True(t, scheduler.Freeze(duelID, pausedAt))
	require.True(t, scheduler.Resume(duelID, resumedAt))

	after, ok := scheduler.PlayerSnapshot(duelID, playerID)
	require.True(t, ok)
	require.Equal(t, resumedAt.Add(900*time.Millisecond), after.Schedule[0].UnlockAt)
	require.Equal(t, resumedAt.Add(1900*time.Millisecond), after.Schedule[1].UnlockAt)
	require.Equal(t, resumedAt.Add(2900*time.Millisecond), after.Schedule[2].UnlockAt)
	require.Empty(t, after.Unlocked)
}

func hintTestTask(timeLimit int) *domain.Task {
	return &domain.Task{
		ID:          uuid.New(),
		Title:       "task",
		Description: "description",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   timeLimit,
		Flag:        "FLAG{task}",
		Hints:       []string{"hint 1", "hint 2", "hint 3"},
	}
}
