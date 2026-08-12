package admin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"

	"github.com/google/uuid"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/ctxutil"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	MaxSourceFileSize        int64 = 100 * 1024 * 1024
	sourceFileCleanupTimeout       = 10 * time.Second
)

var zipLocalFileHeader = []byte{'P', 'K', 0x03, 0x04}

var allowedSourceFileMediaTypes = map[string]struct{}{
	"application/zip":              {},
	"application/x-zip-compressed": {},
	"application/octet-stream":     {},
}

type UploadUseCase struct {
	tasks   UploadTaskRepository
	storage SourceFileStorage
	log     logkit.Logger
}

func NewUploadUseCase(tasks UploadTaskRepository, storage SourceFileStorage) *UploadUseCase {
	return &UploadUseCase{
		tasks:   tasks,
		storage: storage,
	}
}

type UploadOption func(*UploadUseCase)

func WithUploadLogger(log logkit.Logger) UploadOption {
	return func(u *UploadUseCase) {
		u.log = log
	}
}

func (u *UploadUseCase) Configure(options ...UploadOption) *UploadUseCase {
	for _, option := range options {
		option(u)
	}
	return u
}

func (u *UploadUseCase) UploadSourceFile(
	ctx context.Context,
	taskID uuid.UUID,
	reader io.Reader,
	size int64,
	contentType string,
) (string, error) {
	if err := validateSourceFileMeta(size, contentType); err != nil {
		return "", err
	}

	header, err := readZipHeader(reader)
	if err != nil {
		return "", err
	}

	task, err := u.tasks.GetByID(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("UploadUsecase - UploadSourceFile - TaskRepo.GetByID: %w", err)
	}

	key := domain.TaskSourceFileUploadKey(taskID, uuid.New())
	storedURL, err := u.storage.Upload(ctx, key, io.MultiReader(bytes.NewReader(header), reader), size)
	if err != nil {
		return "", fmt.Errorf("UploadUsecase - UploadSourceFile - SourceFileStorage.Upload: %w", err)
	}

	presignedURL, err := u.storage.PresignedGetURL(ctx, key, time.Duration(task.TimeLimit)*time.Second)
	if err != nil {
		u.deleteSourceFileKeyDetached(ctx, "upload_presign_failed", taskID, key)
		return "", fmt.Errorf("UploadUsecase - UploadSourceFile - SourceFileStorage.PresignedGetURL: %w", err)
	}

	if _, err := u.tasks.Update(ctx, taskID, taskInputWithSourceFileURL(task, storedURL)); err != nil {
		u.deleteSourceFileKeyDetached(ctx, "upload_update_failed", taskID, key)
		return "", fmt.Errorf("UploadUsecase - UploadSourceFile - TaskRepo.Update: %w", err)
	}
	if task.SourceFileURL != nil {
		oldKey := domain.TaskSourceFileKeyFromURL(taskID, *task.SourceFileURL)
		if oldKey != key {
			u.deleteSourceFileKeyDetached(ctx, "upload_replace_old", taskID, oldKey)
		}
	}

	return presignedURL, nil
}

func (u *UploadUseCase) ClearSourceFile(ctx context.Context, taskID uuid.UUID, in TaskInput) (*domain.Task, error) {
	if err := validateTaskInput(in); err != nil {
		return nil, err
	}
	existing, err := u.tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("UploadUsecase - ClearSourceFile - TaskRepo.GetByID: %w", err)
	}

	in.SourceFileURL = nil
	updated, err := u.tasks.Update(ctx, taskID, in)
	if err != nil {
		return nil, fmt.Errorf("UploadUsecase - ClearSourceFile - TaskRepo.Update: %w", err)
	}
	if existing.SourceFileURL != nil {
		cleanupCtx, cleanupCancel := ctxutil.DetachedWithTimeout(ctx, sourceFileCleanupTimeout)
		defer cleanupCancel()
		_ = u.DeleteSourceFile(cleanupCtx, taskID, existing.SourceFileURL)
	}
	return updated, nil
}

func (u *UploadUseCase) PresignedSourceFileURL(ctx context.Context, taskID uuid.UUID) (string, error) {
	task, err := u.tasks.GetByID(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("UploadUsecase - PresignedSourceFileURL - TaskRepo.GetByID: %w", err)
	}
	if task.SourceFileURL == nil {
		return "", apperr.ErrTaskNotFound
	}

	key := domain.TaskSourceFileKeyFromURL(taskID, *task.SourceFileURL)
	presignedURL, err := u.storage.PresignedGetURL(ctx, key, time.Duration(task.TimeLimit)*time.Second)
	if err != nil {
		return "", fmt.Errorf("UploadUsecase - PresignedSourceFileURL - SourceFileStorage.PresignedGetURL: %w", err)
	}
	return presignedURL, nil
}

func (u *UploadUseCase) DeleteSourceFile(ctx context.Context, taskID uuid.UUID, sourceFileURL *string) error {
	key := SourceFileKey(taskID)
	if sourceFileURL != nil {
		key = domain.TaskSourceFileKeyFromURL(taskID, *sourceFileURL)
	}
	if err := u.storage.Delete(ctx, key); err != nil {
		u.logSourceCleanupError("delete_source_file", taskID, key, err)
		return fmt.Errorf("UploadUsecase - DeleteSourceFile - SourceFileStorage.Delete: %w", err)
	}
	return nil
}

func (u *UploadUseCase) deleteSourceFileKey(ctx context.Context, operation string, taskID uuid.UUID, key string) {
	if err := u.storage.Delete(ctx, key); err != nil {
		u.logSourceCleanupError(operation, taskID, key, err)
	}
}

func (u *UploadUseCase) deleteSourceFileKeyDetached(ctx context.Context, operation string, taskID uuid.UUID, key string) {
	cleanupCtx, cleanupCancel := ctxutil.DetachedWithTimeout(ctx, sourceFileCleanupTimeout)
	defer cleanupCancel()
	u.deleteSourceFileKey(cleanupCtx, operation, taskID, key)
}

func (u *UploadUseCase) logSourceCleanupError(operation string, taskID uuid.UUID, key string, err error) {
	if u.log == nil || err == nil {
		return
	}
	u.log.Error("source file cleanup failed", logkit.Fields{
		"operation": operation,
		"task_id":   taskID.String(),
		"key":       key,
		"error":     err.Error(),
	})
}

func SourceFileKey(taskID uuid.UUID) string {
	return domain.TaskSourceFileKey(taskID)
}

func validateSourceFileMeta(size int64, contentType string) error {
	if size < int64(len(zipLocalFileHeader)) || size > MaxSourceFileSize {
		return apperr.ErrTaskValidation
	}

	if strings.TrimSpace(contentType) == "" {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return apperr.ErrTaskValidation
	}
	if _, ok := allowedSourceFileMediaTypes[mediaType]; !ok {
		return apperr.ErrTaskValidation
	}
	return nil
}

func readZipHeader(reader io.Reader) ([]byte, error) {
	header := make([]byte, len(zipLocalFileHeader))
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, apperr.ErrTaskValidation
	}
	if !bytes.Equal(header, zipLocalFileHeader) {
		return nil, apperr.ErrTaskValidation
	}
	return header, nil
}

func taskInputWithSourceFileURL(task *domain.Task, sourceFileURL string) TaskInput {
	return TaskInput{
		Title:         task.Title,
		Description:   task.Description,
		Category:      task.Category,
		Difficulty:    task.Difficulty,
		TimeLimit:     task.TimeLimit,
		Flag:          task.Flag,
		Hints:         append([]string(nil), task.Hints...),
		TaskURL:       task.TaskURL,
		SourceFileURL: &sourceFileURL,
	}
}
