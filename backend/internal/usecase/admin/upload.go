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

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

const (
	MaxSourceFileSize int64 = 100 * 1024 * 1024
)

var zipLocalFileHeader = []byte{'P', 'K', 0x03, 0x04}

var allowedSourceFileMediaTypes = map[string]struct{}{
	"application/zip":              {},
	"application/x-zip-compressed": {},
	"application/octet-stream":     {},
}

type UploadUsecase struct {
	tasks   usecase.TaskRepo
	storage usecase.SourceFileStorage
}

func NewUploadUsecase(tasks usecase.TaskRepo, storage usecase.SourceFileStorage) *UploadUsecase {
	return &UploadUsecase{
		tasks:   tasks,
		storage: storage,
	}
}

func (u *UploadUsecase) UploadSourceFile(
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

	key := SourceFileKey(taskID)
	storedURL, err := u.storage.Upload(ctx, key, io.MultiReader(bytes.NewReader(header), reader), size)
	if err != nil {
		return "", fmt.Errorf("UploadUsecase - UploadSourceFile - SourceFileStorage.Upload: %w", err)
	}

	if _, err := u.tasks.Update(ctx, taskID, taskInputWithSourceFileURL(task, storedURL)); err != nil {
		return "", fmt.Errorf("UploadUsecase - UploadSourceFile - TaskRepo.Update: %w", err)
	}

	presignedURL, err := u.storage.PresignedGetURL(ctx, key, time.Duration(task.TimeLimit)*time.Second)
	if err != nil {
		return "", fmt.Errorf("UploadUsecase - UploadSourceFile - SourceFileStorage.PresignedGetURL: %w", err)
	}
	return presignedURL, nil
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
