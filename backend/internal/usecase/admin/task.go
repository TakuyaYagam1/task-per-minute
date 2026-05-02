package admin

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

type TaskInput = usecase.TaskInput

type TaskUsecase struct {
	tasks usecase.TaskRepo
}

func NewTaskUsecase(tasks usecase.TaskRepo) *TaskUsecase {
	return &TaskUsecase{tasks: tasks}
}

func (u *TaskUsecase) CreateTask(ctx context.Context, in TaskInput) (*domain.Task, error) {
	if err := validateTaskInput(in); err != nil {
		return nil, err
	}
	task, err := u.tasks.Create(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("TaskUsecase - CreateTask - TaskRepo.Create: %w", err)
	}
	return task, nil
}

func (u *TaskUsecase) GetTask(ctx context.Context, id uuid.UUID) (*domain.Task, error) {
	task, err := u.tasks.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("TaskUsecase - GetTask - TaskRepo.GetByID: %w", err)
	}
	return task, nil
}

func (u *TaskUsecase) ListTasks(ctx context.Context) ([]*domain.Task, error) {
	tasks, err := u.tasks.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("TaskUsecase - ListTasks - TaskRepo.List: %w", err)
	}
	return tasks, nil
}

func (u *TaskUsecase) UpdateTask(ctx context.Context, id uuid.UUID, in TaskInput) (*domain.Task, error) {
	if err := validateTaskInput(in); err != nil {
		return nil, err
	}
	task, err := u.tasks.Update(ctx, id, in)
	if err != nil {
		return nil, fmt.Errorf("TaskUsecase - UpdateTask - TaskRepo.Update: %w", err)
	}
	return task, nil
}

func (u *TaskUsecase) DeleteTask(ctx context.Context, id uuid.UUID) error {
	if _, err := u.tasks.GetByID(ctx, id); err != nil {
		return fmt.Errorf("TaskUsecase - DeleteTask - TaskRepo.GetByID: %w", err)
	}
	used, err := u.tasks.IsUsedInActiveDuel(ctx, id)
	if err != nil {
		return fmt.Errorf("TaskUsecase - DeleteTask - TaskRepo.IsUsedInActiveDuel: %w", err)
	}
	if used {
		return apperr.ErrTaskInUse
	}
	if err := u.tasks.Delete(ctx, id); err != nil {
		return fmt.Errorf("TaskUsecase - DeleteTask - TaskRepo.Delete: %w", err)
	}
	return nil
}

func validateTaskInput(in TaskInput) error {
	if !domain.IsValidTaskTitle(in.Title) {
		return apperr.ErrTaskValidation
	}
	if !domain.IsValidTaskDescription(in.Description) {
		return apperr.ErrTaskValidation
	}
	if !in.Category.IsValid() || !in.Difficulty.IsValid() {
		return apperr.ErrTaskValidation
	}
	if !domain.IsValidTaskTimeLimit(in.TimeLimit) {
		return apperr.ErrTaskValidation
	}
	if !domain.IsValidTaskFlag(in.Flag) {
		return apperr.ErrTaskValidation
	}
	if !domain.IsValidOptionalTaskURL(in.TaskURL) {
		return apperr.ErrTaskValidation
	}
	if !domain.IsValidTaskHints(in.Hints) {
		return apperr.ErrTaskValidation
	}
	return nil
}
