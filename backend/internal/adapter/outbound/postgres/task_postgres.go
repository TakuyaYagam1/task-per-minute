package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
)

type TaskPostgres struct {
	tx *TxManager
}

var _ taskusecase.Repository = (*TaskPostgres)(nil)

func NewTaskPostgres(tx *TxManager) *TaskPostgres {
	return &TaskPostgres{tx: tx}
}

func (r *TaskPostgres) Create(ctx context.Context, in taskusecase.UpdateInput) (*domain.Task, error) {
	normalized, err := normalizeTaskInput(in)
	if err != nil {
		return nil, err
	}
	var row sqlc.CreateTaskRow
	err = r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		var createErr error
		row, createErr = querier.CreateTask(txCtx, createTaskParams(normalized))
		if createErr != nil {
			return fmt.Errorf("TaskPostgres - Create - Querier.CreateTask: %w", createErr)
		}
		if _, createErr = querier.CreateTaskVersionContentValidationAttestation(
			txCtx,
			sqlc.CreateTaskVersionContentValidationAttestationParams{
				TaskID:      row.ID,
				TaskVersion: row.CurrentVersion,
			},
		); createErr != nil {
			return fmt.Errorf("TaskPostgres - Create - content validation attestation: %w", createErr)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return createTaskToDomain(row), nil
}

func (r *TaskPostgres) GetByID(ctx context.Context, id uuid.UUID) (*domain.Task, error) {
	row, err := r.tx.Querier(ctx).GetTaskByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrTaskNotFound
		}
		return nil, fmt.Errorf("TaskPostgres - GetByID - Querier.GetTaskByID: %w", err)
	}
	return getTaskToDomain(row), nil
}

func (r *TaskPostgres) List(ctx context.Context) ([]*domain.Task, error) {
	rows, err := r.tx.Querier(ctx).ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("TaskPostgres - List - Querier.ListTasks: %w", err)
	}
	out := make([]*domain.Task, 0, len(rows))
	for _, row := range rows {
		out = append(out, listTaskToDomain(row))
	}
	return out, nil
}

func getTaskToDomain(row sqlc.GetTaskByIDRow) *domain.Task {
	return taskValuesToDomain(
		row.ID, row.Title, row.Description, row.Category, row.Difficulty, row.TimeLimit, row.Flag,
		row.Hint1, row.Hint2, row.Hint3, row.TaskUrl, row.SourceFileUrl, row.Kind, row.Enabled,
		row.CurrentVersion, row.CreatedAt.Time,
	)
}

func listTaskToDomain(row sqlc.ListTasksRow) *domain.Task {
	return taskValuesToDomain(
		row.ID, row.Title, row.Description, row.Category, row.Difficulty, row.TimeLimit, row.Flag,
		row.Hint1, row.Hint2, row.Hint3, row.TaskUrl, row.SourceFileUrl, row.Kind, row.Enabled,
		row.CurrentVersion, row.CreatedAt.Time,
	)
}

func (r *TaskPostgres) Update(ctx context.Context, id uuid.UUID, in taskusecase.UpdateInput) (*domain.Task, error) {
	normalized, err := normalizeTaskInput(in)
	if err != nil {
		return nil, err
	}
	var row sqlc.UpdateTaskRow
	err = r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		var updateErr error
		row, updateErr = querier.UpdateTask(txCtx, updateTaskParams(id, normalized))
		if updateErr != nil {
			return fmt.Errorf("TaskPostgres - Update - Querier.UpdateTask: %w", updateErr)
		}
		if _, updateErr = querier.CreateTaskVersionContentValidationAttestation(
			txCtx,
			sqlc.CreateTaskVersionContentValidationAttestationParams{
				TaskID:      row.ID,
				TaskVersion: row.CurrentVersion,
			},
		); updateErr != nil {
			return fmt.Errorf("TaskPostgres - Update - content validation attestation: %w", updateErr)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrTaskNotFound
		}
		return nil, err
	}
	return updateTaskToDomain(row), nil
}

func normalizeTaskInput(in taskusecase.UpdateInput) (taskusecase.UpdateInput, error) {
	if !domain.IsValidTaskTitle(in.Title) {
		return taskusecase.UpdateInput{}, domain.ErrTaskValidation
	}
	if !domain.IsValidTaskDescription(in.Description) {
		return taskusecase.UpdateInput{}, domain.ErrTaskValidation
	}
	if !in.Category.IsValid() || !in.Difficulty.IsValid() || !in.Kind.IsValid() {
		return taskusecase.UpdateInput{}, domain.ErrTaskValidation
	}
	if !domain.IsValidTaskTimeLimit(in.TimeLimit) {
		return taskusecase.UpdateInput{}, domain.ErrTaskValidation
	}
	if !domain.IsValidTaskFlag(in.Flag) {
		return taskusecase.UpdateInput{}, domain.ErrTaskValidation
	}
	if !domain.IsValidTaskURLShape(in.Category, in.TaskURL, in.SourceFileURL) {
		return taskusecase.UpdateInput{}, domain.ErrTaskValidation
	}
	hints, ok := domain.NormalizeTaskHints(in.Hints)
	if !ok {
		return taskusecase.UpdateInput{}, domain.ErrTaskValidation
	}
	in.Hints = hints
	return in, nil
}

func createTaskParams(in taskusecase.UpdateInput) sqlc.CreateTaskParams {
	hint1, hint2, hint3 := taskHintPointers(in.Hints)
	return sqlc.CreateTaskParams{
		Title:         in.Title,
		Description:   in.Description,
		Category:      string(in.Category),
		Difficulty:    string(in.Difficulty),
		TimeLimit:     int32(in.TimeLimit), //nolint:gosec // normalizeTaskInput rejects values outside the PostgreSQL int4 range.
		Flag:          in.Flag,
		Hint1:         hint1,
		Hint2:         hint2,
		Hint3:         hint3,
		TaskUrl:       in.TaskURL,
		SourceFileUrl: in.SourceFileURL,
		Kind:          string(in.Kind),
		Enabled:       in.Enabled,
	}
}

func updateTaskParams(id uuid.UUID, in taskusecase.UpdateInput) sqlc.UpdateTaskParams {
	hint1, hint2, hint3 := taskHintPointers(in.Hints)
	return sqlc.UpdateTaskParams{
		ID:            id,
		Title:         in.Title,
		Description:   in.Description,
		Category:      string(in.Category),
		Difficulty:    string(in.Difficulty),
		TimeLimit:     int32(in.TimeLimit), //nolint:gosec // normalizeTaskInput rejects values outside the PostgreSQL int4 range.
		Flag:          in.Flag,
		Hint1:         hint1,
		Hint2:         hint2,
		Hint3:         hint3,
		TaskUrl:       in.TaskURL,
		SourceFileUrl: in.SourceFileURL,
		Kind:          string(in.Kind),
		Enabled:       in.Enabled,
	}
}

func taskHintPointers(hints []string) (*string, *string, *string) {
	normalized, _ := domain.NormalizeTaskHints(hints)
	return taskHintPointer(normalized[0]), taskHintPointer(normalized[1]), taskHintPointer(normalized[2])
}

func taskHintPointer(hint string) *string {
	if hint == "" {
		return nil
	}
	return &hint
}

func (r *TaskPostgres) Delete(ctx context.Context, id uuid.UUID) error {
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if _, lockErr := querier.LockTaskForContentMutation(txCtx, id); lockErr != nil {
			if errors.Is(lockErr, pgx.ErrNoRows) {
				return domain.ErrTaskNotFound
			}
			return fmt.Errorf("TaskPostgres - Delete - lock task: %w", lockErr)
		}
		referenced, referenceErr := querier.TaskReferencedByTournament(txCtx, id)
		if referenceErr != nil {
			return fmt.Errorf("TaskPostgres - Delete - check published references: %w", referenceErr)
		}
		if referenced {
			return domain.ErrTaskInUse
		}
		if deleteErr := querier.DeleteTask(txCtx, id); deleteErr != nil {
			if errors.Is(deleteErr, pgx.ErrNoRows) {
				return domain.ErrTaskNotFound
			}
			if isForeignKeyViolation(deleteErr) {
				return domain.WrapError(deleteErr, domain.ErrTaskInUse)
			}
			return fmt.Errorf("TaskPostgres - Delete - Querier.DeleteTask: %w", deleteErr)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}
