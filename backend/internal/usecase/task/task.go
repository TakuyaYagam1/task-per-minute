package task

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type UseCase struct {
	tasks Repository
}

func NewUseCase(tasks Repository) *UseCase {
	return &UseCase{tasks: tasks}
}

func (u *UseCase) CreateTask(ctx context.Context, input CreateInput) (*domain.Task, error) {
	normalized, err := normalizeCreateInput(input)
	if err != nil {
		return nil, err
	}
	created, err := u.tasks.Create(ctx, normalized)
	if err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}
	return created, nil
}

func (u *UseCase) GetTask(ctx context.Context, id uuid.UUID) (*domain.Task, error) {
	found, err := u.tasks.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	return found, nil
}

func (u *UseCase) ListTasks(ctx context.Context) ([]*domain.Task, error) {
	tasks, err := u.tasks.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return tasks, nil
}

func (u *UseCase) UpdateTask(ctx context.Context, id uuid.UUID, input UpdateInput) (*domain.Task, error) {
	normalized, err := normalizeInput(input)
	if err != nil {
		return nil, err
	}
	updated, err := u.tasks.Update(ctx, id, normalized)
	if err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}
	return updated, nil
}

func (u *UseCase) DeleteTask(ctx context.Context, id uuid.UUID) error {
	if err := u.tasks.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	return nil
}

func normalizeCreateInput(input CreateInput) (UpdateInput, error) {
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	kind := input.Kind
	if kind == "" {
		kind = domain.TaskKindNormal
	}
	return normalizeInput(UpdateInput{
		Title:         input.Title,
		Description:   input.Description,
		Category:      input.Category,
		Difficulty:    input.Difficulty,
		TimeLimit:     input.TimeLimit,
		Flag:          input.Flag,
		Kind:          kind,
		Enabled:       enabled,
		Hints:         input.Hints,
		TaskURL:       input.TaskURL,
		SourceFileURL: input.SourceFileURL,
	})
}

func normalizeInput(input UpdateInput) (UpdateInput, error) {
	if !domain.IsValidTaskTitle(input.Title) ||
		!domain.IsValidTaskDescription(input.Description) ||
		!input.Category.IsValid() ||
		!input.Difficulty.IsValid() ||
		!input.Kind.IsValid() ||
		!domain.IsValidTaskTimeLimit(input.TimeLimit) ||
		!domain.IsValidTaskFlag(input.Flag) ||
		!domain.IsValidTaskURLShape(input.Category, input.TaskURL, input.SourceFileURL) {
		return UpdateInput{}, domain.ErrTaskValidation
	}
	hints, ok := domain.NormalizeTaskHints(input.Hints)
	if !ok {
		return UpdateInput{}, domain.ErrTaskValidation
	}
	input.Hints = hints
	return input, nil
}
