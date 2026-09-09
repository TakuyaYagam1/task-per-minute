//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTaskUseCase_TaskLifecycle(t *testing.T) {
	t.Parallel()

	uc := newTaskUseCaseFixture()
	ctx := context.Background()

	created, err := uc.CreateTask(ctx, taskusecase.CreateInput{
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

	updated, err := uc.UpdateTask(ctx, created.ID, taskusecase.UpdateInput{
		Title:       created.Title + "_updated",
		Description: "updated",
		Category:    domain.CategoryCrypto,
		Difficulty:  domain.DifficultyMedium,
		TimeLimit:   120,
		Flag:        "FLAG{updated}",
		Kind:        domain.TaskKindNormal,
		Enabled:     true,
		Hints:       defaultTaskHints("updated"),
	})
	require.NoError(t, err)
	require.Equal(t, domain.DifficultyMedium, updated.Difficulty)
	require.Equal(t, 120, updated.TimeLimit)

	require.NoError(t, uc.DeleteTask(ctx, created.ID))
	_, err = uc.GetTask(ctx, created.ID)
	require.ErrorIs(t, err, domain.ErrTaskNotFound)
}

func TestTaskUseCase_CreateTask_InvalidDifficulty(t *testing.T) {
	t.Parallel()

	uc := newTaskUseCaseFixture()
	_, err := uc.CreateTask(context.Background(), taskusecase.CreateInput{
		Title:       uniq("task"),
		Description: "description",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.Difficulty("insane"),
		TimeLimit:   60,
		Flag:        "FLAG{task}",
		Hints:       defaultTaskHints("task"),
	})
	require.ErrorIs(t, err, domain.ErrTaskValidation)
}

func TestTaskUseCase_DeleteTask_MissingReturnsTaskNotFound(t *testing.T) {
	t.Parallel()

	uc := newTaskUseCaseFixture()
	err := uc.DeleteTask(context.Background(), uuid.New())
	require.ErrorIs(t, err, domain.ErrTaskNotFound)
}

func newTaskUseCaseFixture() *taskusecase.UseCase {
	tasks := postgres.NewTaskPostgres(postgres.NewTxManager(sharedPool))
	return taskusecase.NewUseCase(tasks)
}

func taskIDs(tasks []*domain.Task) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.ID)
	}
	return out
}
