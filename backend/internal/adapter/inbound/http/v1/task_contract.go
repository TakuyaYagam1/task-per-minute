package v1

import (
	"context"
	"io"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
)

type AdminTaskService interface {
	CreateTask(ctx context.Context, in taskusecase.CreateInput) (*domain.Task, error)
	GetTask(ctx context.Context, id uuid.UUID) (*domain.Task, error)
	ListTasks(ctx context.Context) ([]*domain.Task, error)
	UpdateTask(ctx context.Context, id uuid.UUID, in taskusecase.UpdateInput) (*domain.Task, error)
	DeleteTask(ctx context.Context, id uuid.UUID) error
}

type UploadService interface {
	UploadSourceFile(
		ctx context.Context,
		taskID uuid.UUID,
		reader io.Reader,
		size int64,
		contentType string,
	) (string, error)
	ClearSourceFile(ctx context.Context, taskID uuid.UUID, in taskusecase.UpdateInput) (*domain.Task, error)
	PresignedSourceFileURL(ctx context.Context, taskID uuid.UUID) (string, error)
}
