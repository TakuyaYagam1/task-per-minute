//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
)

func TestAdminTaskUsecase_TaskLifecycle(t *testing.T) {
	t.Parallel()

	uc, _ := newAdminTaskUsecaseFixture()
	ctx := context.Background()

	created, err := uc.CreateTask(ctx, admin.TaskInput{
		Title:       uniq("task"),
		Description: "description",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "FLAG{task}",
		Hints:       defaultTaskHints("task"),
	})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, created.ID)

	got, err := uc.GetTask(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)

	list, err := uc.ListTasks(ctx)
	require.NoError(t, err)
	require.Contains(t, taskIDs(list), created.ID)

	updated, err := uc.UpdateTask(ctx, created.ID, admin.TaskInput{
		Title:       created.Title + "_updated",
		Description: "updated",
		Category:    domain.CategoryCrypto,
		Difficulty:  domain.DifficultyMedium,
		TimeLimit:   120,
		Flag:        "FLAG{updated}",
		Hints:       defaultTaskHints("updated"),
	})
	require.NoError(t, err)
	require.Equal(t, domain.DifficultyMedium, updated.Difficulty)
	require.Equal(t, 120, updated.TimeLimit)

	require.NoError(t, uc.DeleteTask(ctx, created.ID))
	_, err = uc.GetTask(ctx, created.ID)
	require.ErrorIs(t, err, apperr.ErrTaskNotFound)
}

func TestAdminTaskUsecase_CreateTask_InvalidDifficulty(t *testing.T) {
	t.Parallel()

	uc, _ := newAdminTaskUsecaseFixture()
	_, err := uc.CreateTask(context.Background(), admin.TaskInput{
		Title:       uniq("task"),
		Description: "description",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.Difficulty("insane"),
		TimeLimit:   60,
		Flag:        "FLAG{task}",
		Hints:       defaultTaskHints("task"),
	})
	require.ErrorIs(t, err, apperr.ErrTaskValidation)
}

func TestAdminTaskUsecase_DeleteTask_ActiveDuelReturnsTaskInUse(t *testing.T) {
	t.Parallel()

	uc, f := newAdminTaskUsecaseFixture()
	ctx := context.Background()

	task, err := uc.CreateTask(ctx, admin.TaskInput{
		Title:       uniq("task"),
		Description: "description",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "FLAG{task}",
		Hints:       defaultTaskHints("task"),
	})
	require.NoError(t, err)

	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	duel, err := f.duels.Create(ctx, p1.ID, p2.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, p1.ID, task.ID))

	err = uc.DeleteTask(ctx, task.ID)
	require.ErrorIs(t, err, apperr.ErrTaskInUse)
}

func newAdminTaskUsecaseFixture() (*admin.TaskUsecase, *duelFixture) {
	f := newDuelFixture()
	return admin.NewTaskUsecase(f.tasks), f
}

func taskIDs(tasks []*domain.Task) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.ID)
	}
	return out
}
