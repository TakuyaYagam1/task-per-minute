package websocket

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestTaskAssignedPayloadPreservesSparseHintScheduleIndexes(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	duel := &domain.Duel{ID: uuid.New(), StartedAt: startedAt, Deadline: startedAt.Add(100 * time.Second)}
	task := websocketTestTask()
	task.TimeLimit = 100
	task.Hints = []string{"", "", "third"}

	payload := taskAssignedPayload(duel, task)

	require.Equal(t, []HintScheduleEntry{
		{HintIndex: 3, UnlockAt: startedAt.Add(75 * time.Second)},
	}, payload.Task.HintSchedule)
}

func TestTaskAssignedPayloadOmitsNoHintSchedule(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	duel := &domain.Duel{ID: uuid.New(), StartedAt: startedAt, Deadline: startedAt.Add(100 * time.Second)}
	task := websocketTestTask()
	task.Hints = []string{"", "", ""}

	payload := taskAssignedPayload(duel, task)

	require.Empty(t, payload.Task.HintSchedule)
}

func websocketTestTask() *domain.Task {
	return &domain.Task{
		ID:          uuid.New(),
		Title:       "task",
		Description: "description",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "FLAG{task}",
		Hints:       []string{"first", "second", "third"},
	}
}
