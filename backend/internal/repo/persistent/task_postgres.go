package persistent

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

type TaskPostgres struct {
	tx *TxManager
}

func NewTaskPostgres(tx *TxManager) *TaskPostgres {
	return &TaskPostgres{tx: tx}
}

type TaskInput = usecase.TaskInput

func (r *TaskPostgres) Create(ctx context.Context, in TaskInput) (*domain.Task, error) {
	if err := validateTaskInput(in); err != nil {
		return nil, err
	}
	row, err := r.tx.Querier(ctx).CreateTask(ctx, createTaskParams(in))
	if err != nil {
		return nil, fmt.Errorf("TaskPostgres - Create - Querier.CreateTask: %w", err)
	}
	return taskToDomain(row), nil
}

func (r *TaskPostgres) GetByID(ctx context.Context, id uuid.UUID) (*domain.Task, error) {
	row, err := r.tx.Querier(ctx).GetTaskByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrTaskNotFound
		}
		return nil, fmt.Errorf("TaskPostgres - GetByID - Querier.GetTaskByID: %w", err)
	}
	return taskToDomain(row), nil
}

func (r *TaskPostgres) List(ctx context.Context) ([]*domain.Task, error) {
	rows, err := r.tx.Querier(ctx).ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("TaskPostgres - List - Querier.ListTasks: %w", err)
	}
	out := make([]*domain.Task, 0, len(rows))
	for _, row := range rows {
		out = append(out, taskToDomain(row))
	}
	return out, nil
}

func (r *TaskPostgres) ListByDifficulty(ctx context.Context, difficulty domain.Difficulty) ([]*domain.Task, error) {
	if !difficulty.IsValid() {
		return nil, apperr.ErrValidation
	}
	rows, err := r.tx.Querier(ctx).ListTasksByDifficulty(ctx, string(difficulty))
	if err != nil {
		return nil, fmt.Errorf("TaskPostgres - ListByDifficulty - Querier.ListTasksByDifficulty: %w", err)
	}
	out := make([]*domain.Task, 0, len(rows))
	for _, row := range rows {
		out = append(out, taskToDomain(row))
	}
	return out, nil
}

func (r *TaskPostgres) Update(ctx context.Context, id uuid.UUID, in TaskInput) (*domain.Task, error) {
	if err := validateTaskInput(in); err != nil {
		return nil, err
	}
	row, err := r.tx.Querier(ctx).UpdateTask(ctx, updateTaskParams(id, in))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrTaskNotFound
		}
		return nil, fmt.Errorf("TaskPostgres - Update - Querier.UpdateTask: %w", err)
	}
	return taskToDomain(row), nil
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
	if !domain.IsValidTaskURLShape(in.Category, in.TaskURL, in.SourceFileURL) {
		return apperr.ErrTaskValidation
	}
	if !domain.IsValidTaskHints(in.Hints) {
		return apperr.ErrTaskValidation
	}
	return nil
}

func createTaskParams(in TaskInput) sqlc.CreateTaskParams {
	hint1, hint2, hint3 := taskHintPointers(in.Hints)
	return sqlc.CreateTaskParams{
		Title:         in.Title,
		Description:   in.Description,
		Category:      string(in.Category),
		Difficulty:    string(in.Difficulty),
		TimeLimit:     int32(in.TimeLimit), //nolint:gosec // validateTaskInput rejects values outside the PostgreSQL int4 range.
		Flag:          in.Flag,
		Hint1:         hint1,
		Hint2:         hint2,
		Hint3:         hint3,
		TaskUrl:       in.TaskURL,
		SourceFileUrl: in.SourceFileURL,
	}
}

func updateTaskParams(id uuid.UUID, in TaskInput) sqlc.UpdateTaskParams {
	hint1, hint2, hint3 := taskHintPointers(in.Hints)
	return sqlc.UpdateTaskParams{
		ID:            id,
		Title:         in.Title,
		Description:   in.Description,
		Category:      string(in.Category),
		Difficulty:    string(in.Difficulty),
		TimeLimit:     int32(in.TimeLimit), //nolint:gosec // validateTaskInput rejects values outside the PostgreSQL int4 range.
		Flag:          in.Flag,
		Hint1:         hint1,
		Hint2:         hint2,
		Hint3:         hint3,
		TaskUrl:       in.TaskURL,
		SourceFileUrl: in.SourceFileURL,
	}
}

func taskHintPointers(hints []string) (*string, *string, *string) {
	hint1, hint2, hint3 := hints[0], hints[1], hints[2]
	return &hint1, &hint2, &hint3
}

func (r *TaskPostgres) Delete(ctx context.Context, id uuid.UUID) error {
	if err := r.tx.Querier(ctx).DeleteTask(ctx, id); err != nil {
		if isForeignKeyViolation(err) {
			return apperr.Wrap(err, apperr.ErrTaskInUse)
		}
		return fmt.Errorf("TaskPostgres - Delete - Querier.DeleteTask: %w", err)
	}
	return nil
}

func (r *TaskPostgres) IsUsedInActiveDuel(ctx context.Context, id uuid.UUID) (bool, error) {
	used, err := r.tx.Querier(ctx).TaskInActiveDuel(ctx, id)
	if err != nil {
		return false, fmt.Errorf("TaskPostgres - IsUsedInActiveDuel - Querier.TaskInActiveDuel: %w", err)
	}
	return used, nil
}

func (r *TaskPostgres) CountByDifficulty(ctx context.Context, difficulty domain.Difficulty) (int64, error) {
	if !difficulty.IsValid() {
		return 0, apperr.ErrValidation
	}
	n, err := r.tx.Querier(ctx).CountTasksByDifficulty(ctx, string(difficulty))
	if err != nil {
		return 0, fmt.Errorf("TaskPostgres - CountByDifficulty - Querier.CountTasksByDifficulty: %w", err)
	}
	return n, nil
}

func (r *TaskPostgres) CountSolvedByDifficulty(ctx context.Context, playerID uuid.UUID, difficulty domain.Difficulty) (int64, error) {
	if !difficulty.IsValid() {
		return 0, apperr.ErrValidation
	}
	n, err := r.tx.Querier(ctx).CountSolvedTasksByDifficulty(ctx, sqlc.CountSolvedTasksByDifficultyParams{
		PlayerID:   playerID,
		Difficulty: string(difficulty),
	})
	if err != nil {
		return 0, fmt.Errorf("TaskPostgres - CountSolvedByDifficulty - Querier.CountSolvedTasksByDifficulty: %w", err)
	}
	return n, nil
}
