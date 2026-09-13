package task

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	MaxSourceFileSize int64 = 100 * 1024 * 1024
	cleanupTimeout          = 10 * time.Second
)

var zipLocalFileHeader = []byte{'P', 'K', 0x03, 0x04}

var allowedSourceFileMediaTypes = map[string]struct{}{
	"application/zip":              {},
	"application/x-zip-compressed": {},
	"application/octet-stream":     {},
}

type SourceFiles struct {
	tasks           Catalog
	storage         SourceFileStorage
	cleanup         CleanupRunner
	cleanupObserver SourceFileCleanupObserver
}

func NewSourceFiles(
	tasks Catalog,
	storage SourceFileStorage,
	cleanup CleanupRunner,
	observers ...SourceFileCleanupObserver,
) *SourceFiles {
	return &SourceFiles{
		tasks:           tasks,
		storage:         storage,
		cleanup:         cleanup,
		cleanupObserver: firstCleanupObserver(observers...),
	}
}

func (s *SourceFiles) UploadSourceFile(
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
	existing, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("SourceFiles - UploadSourceFile - Catalog.GetTask: %w", err)
	}

	key := sourceFileUploadKey(taskID, uuid.New())
	storedURL, err := s.storage.Upload(ctx, key, io.MultiReader(bytes.NewReader(header), reader), size)
	if err != nil {
		return "", fmt.Errorf("SourceFiles - UploadSourceFile - SourceFileStorage.Upload: %w", err)
	}
	presignedURL, err := s.storage.PresignedGetURL(ctx, key, time.Duration(existing.TimeLimit)*time.Second)
	if err != nil {
		s.cleanupKey(ctx, "upload_presign_failed", taskID, key)
		return "", fmt.Errorf("SourceFiles - UploadSourceFile - SourceFileStorage.PresignedGetURL: %w", err)
	}
	if _, err := s.tasks.UpdateTask(ctx, taskID, inputWithSourceFileURL(existing, storedURL)); err != nil {
		// The update may have committed before its result became unavailable.
		// Retaining the unique key keeps an appended task version readable.
		return "", fmt.Errorf("SourceFiles - UploadSourceFile - Catalog.UpdateTask: %w", err)
	}
	// Older keys remain immutable task-version and assignment-snapshot evidence.
	return presignedURL, nil
}

func (s *SourceFiles) ClearSourceFile(
	ctx context.Context,
	taskID uuid.UUID,
	input UpdateInput,
) (*domain.Task, error) {
	input.SourceFileURL = nil
	updated, err := s.tasks.UpdateTask(ctx, taskID, input)
	if err != nil {
		return nil, fmt.Errorf("SourceFiles - ClearSourceFile - Catalog.UpdateTask: %w", err)
	}
	// Clearing the mutable task head must not remove archived object bytes.
	return updated, nil
}

func (s *SourceFiles) PresignedSourceFileURL(ctx context.Context, taskID uuid.UUID) (string, error) {
	existing, err := s.tasks.GetTask(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("SourceFiles - PresignedSourceFileURL - Catalog.GetTask: %w", err)
	}
	if existing.SourceFileURL == nil {
		return "", domain.ErrTaskNotFound
	}
	key, err := sourceFileKeyFromURL(taskID, *existing.SourceFileURL)
	if err != nil {
		return "", fmt.Errorf("SourceFiles - PresignedSourceFileURL - sourceFileKeyFromURL: %w", err)
	}
	presignedURL, err := s.storage.PresignedGetURL(ctx, key, time.Duration(existing.TimeLimit)*time.Second)
	if err != nil {
		return "", fmt.Errorf("SourceFiles - PresignedSourceFileURL - SourceFileStorage.PresignedGetURL: %w", err)
	}
	return presignedURL, nil
}

// PresignCanonicalSourceFileURL signs an already authorized immutable archive
// URL without consulting or updating the mutable task head.
func (s *SourceFiles) PresignCanonicalSourceFileURL(
	ctx context.Context,
	taskID uuid.UUID,
	canonicalURL string,
	ttl time.Duration,
) (string, error) {
	if ctx == nil || s == nil || s.storage == nil || taskID == uuid.Nil || ttl <= 0 {
		return "", domain.ErrValidation
	}
	key, err := sourceFileKeyFromURL(taskID, canonicalURL)
	if err != nil {
		return "", fmt.Errorf("SourceFiles - PresignCanonicalSourceFileURL - sourceFileKeyFromURL: %w", err)
	}
	presignedURL, err := s.storage.PresignedGetURL(ctx, key, ttl)
	if err != nil {
		return "", fmt.Errorf("SourceFiles - PresignCanonicalSourceFileURL - SourceFileStorage.PresignedGetURL: %w", err)
	}
	return presignedURL, nil
}

func (s *SourceFiles) cleanupKey(ctx context.Context, operation string, taskID uuid.UUID, key string) {
	if err := s.runCleanup(ctx, func(cleanupCtx context.Context) error {
		return s.storage.Delete(cleanupCtx, key)
	}); err != nil {
		s.observeCleanupFailure(ctx, operation, taskID, key, err)
	}
}

func (s *SourceFiles) runCleanup(ctx context.Context, cleanup func(context.Context) error) error {
	if s.cleanup == nil {
		return cleanup(ctx)
	}
	return s.cleanup.Run(ctx, cleanupTimeout, cleanup)
}

func (s *SourceFiles) observeCleanupFailure(
	ctx context.Context,
	operation string,
	taskID uuid.UUID,
	objectKey string,
	err error,
) {
	if nilCleanupObserver(s.cleanupObserver) || err == nil {
		return
	}
	s.cleanupObserver.ObserveSourceFileCleanup(ctx, SourceFileCleanupFailure{
		Operation: operation,
		TaskID:    taskID,
		ObjectKey: objectKey,
		Err:       err,
	})
}

func firstCleanupObserver(observers ...SourceFileCleanupObserver) SourceFileCleanupObserver {
	for _, observer := range observers {
		if !nilCleanupObserver(observer) {
			return observer
		}
	}
	return nil
}

func nilCleanupObserver(observer SourceFileCleanupObserver) bool {
	if observer == nil {
		return true
	}
	value := reflect.ValueOf(observer)
	kind := value.Kind()
	canBeNil := kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface ||
		kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Slice
	return canBeNil && value.IsNil()
}

func validateSourceFileMeta(size int64, contentType string) error {
	if size < int64(len(zipLocalFileHeader)) || size > MaxSourceFileSize {
		return domain.ErrTaskValidation
	}
	if strings.TrimSpace(contentType) == "" {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return domain.ErrTaskValidation
	}
	if _, ok := allowedSourceFileMediaTypes[mediaType]; !ok {
		return domain.ErrTaskValidation
	}
	return nil
}

func readZipHeader(reader io.Reader) ([]byte, error) {
	header := make([]byte, len(zipLocalFileHeader))
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, domain.ErrTaskValidation
	}
	if !bytes.Equal(header, zipLocalFileHeader) {
		return nil, domain.ErrTaskValidation
	}
	return header, nil
}

func inputWithSourceFileURL(existing *domain.Task, sourceFileURL string) UpdateInput {
	return UpdateInput{
		Title:         existing.Title,
		Description:   existing.Description,
		Category:      existing.Category,
		Difficulty:    existing.Difficulty,
		TimeLimit:     existing.TimeLimit,
		Flag:          existing.Flag,
		Kind:          existing.Kind,
		Enabled:       existing.Enabled,
		Hints:         append([]string(nil), existing.Hints...),
		TaskURL:       existing.TaskURL,
		SourceFileURL: &sourceFileURL,
	}
}
