package task_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	taskmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task/mocks"
)

func TestSourceFiles_UploadSourceFile_HappyPath(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	payload := zipPayload("hello")
	task := uploadTask(taskID, nil)
	var uploadedKey string

	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)
	tasks.On("UpdateTask", mock.Anything, taskID, taskInputWithVersionedSourceFileURL(taskID)).
		Return(func(_ context.Context, _ uuid.UUID, in taskusecase.UpdateInput) (*domain.Task, error) {
			return taskWithSource(task, *in.SourceFileURL), nil
		})

	storage := newSourceFileStorageMock(
		t,
		func(_ context.Context, key string, r io.Reader, size int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			require.Equal(t, int64(len(payload)), size)
			got, err := io.ReadAll(r)
			require.NoError(t, err)
			require.Equal(t, payload, got)
			uploadedKey = key
			return sourceFileURLForKey(key), nil
		},
		func(_ context.Context, key string, ttl time.Duration) (string, error) {
			require.Equal(t, uploadedKey, key)
			require.Equal(t, time.Duration(task.TimeLimit)*time.Second, ttl)
			return sourceFileURLForKey(key) + "?X-Amz-Signature=test", nil
		},
		nil,
	)

	got, err := taskusecase.NewSourceFiles(tasks, storage, nil).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.NoError(t, err)
	require.Equal(t, sourceFileURLForKey(uploadedKey)+"?X-Amz-Signature=test", got)
}

func TestSourceFiles_UploadSourceFile_AcceptsCommonZIPContentTypes(t *testing.T) {
	t.Parallel()

	contentTypes := []struct {
		name        string
		contentType string
	}{
		{"zip with params", "application/zip; charset=binary"},
		{"x zip compressed", "application/x-zip-compressed"},
		{"octet stream", "application/octet-stream"},
		{"empty", ""},
	}

	for _, tt := range contentTypes {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			taskID := uuid.New()
			payload := zipPayload("x")
			task := uploadTask(taskID, nil)

			tasks := newCatalogMock(t)
			tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)
			tasks.On("UpdateTask", mock.Anything, taskID, taskInputWithVersionedSourceFileURL(taskID)).
				Return(func(_ context.Context, _ uuid.UUID, in taskusecase.UpdateInput) (*domain.Task, error) {
					return taskWithSource(task, *in.SourceFileURL), nil
				})

			storage := newSourceFileStorageMock(
				t,
				func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
					requireVersionedSourceKey(t, taskID, key)
					return sourceFileURLForKey(key), nil
				},
				func(_ context.Context, key string, _ time.Duration) (string, error) {
					return sourceFileURLForKey(key) + "?X-Amz-Signature=test", nil
				},
				nil,
			)

			got, err := taskusecase.NewSourceFiles(tasks, storage, nil).UploadSourceFile(
				t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), tt.contentType,
			)

			require.NoError(t, err)
			require.NotEmpty(t, got)
		})
	}
}

func TestSourceFiles_UploadSourceFile_ReuploadUsesVersionedKeyAndDeletesOldObject(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	oldKey := sourceFileTestKey(taskID)
	oldURL := sourceFileURLForKey(oldKey)
	payload := zipPayload("new")
	task := uploadTask(taskID, &oldURL)
	var uploadedKey string

	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)
	tasks.On("UpdateTask", mock.Anything, taskID, taskInputWithVersionedSourceFileURL(taskID)).
		Return(func(_ context.Context, _ uuid.UUID, in taskusecase.UpdateInput) (*domain.Task, error) {
			return taskWithSource(task, *in.SourceFileURL), nil
		})

	storage := newSourceFileStorageMock(
		t,
		func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			uploadedKey = key
			return sourceFileURLForKey(key), nil
		},
		func(_ context.Context, key string, _ time.Duration) (string, error) {
			require.Equal(t, uploadedKey, key)
			return sourceFileURLForKey(key) + "?X-Amz-Signature=test", nil
		},
		func(_ context.Context, key string) error {
			require.Equal(t, oldKey, key)
			return nil
		},
	)

	_, err := taskusecase.NewSourceFiles(tasks, storage, nil).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.NoError(t, err)
}

func TestSourceFiles_UploadSourceFile_DoesNotDeleteLegacyObject(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	legacyURL := "http://seaweed/tpm/tasks/" + taskID.String() + "/source.zip"
	payload := zipPayload("new")
	task := uploadTask(taskID, &legacyURL)

	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)
	tasks.On("UpdateTask", mock.Anything, taskID, taskInputWithVersionedSourceFileURL(taskID)).
		Return(func(_ context.Context, _ uuid.UUID, in taskusecase.UpdateInput) (*domain.Task, error) {
			return taskWithSource(task, *in.SourceFileURL), nil
		})

	storage := newSourceFileStorageMock(
		t,
		func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			return sourceFileURLForKey(key), nil
		},
		func(_ context.Context, key string, _ time.Duration) (string, error) {
			return sourceFileURLForKey(key), nil
		},
		nil,
	)

	_, err := taskusecase.NewSourceFiles(tasks, storage, nil).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.NoError(t, err)
}

func TestSourceFiles_UploadSourceFile_OldDeleteErrorDoesNotFailCommittedUpload(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	oldKey := sourceFileTestKey(taskID)
	oldURL := sourceFileURLForKey(oldKey)
	payload := zipPayload("new")
	task := uploadTask(taskID, &oldURL)
	cleanupErr := errors.New("cleanup failed")
	var uploadedKey string

	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)
	tasks.On("UpdateTask", mock.Anything, taskID, taskInputWithVersionedSourceFileURL(taskID)).
		Return(func(_ context.Context, _ uuid.UUID, in taskusecase.UpdateInput) (*domain.Task, error) {
			return taskWithSource(task, *in.SourceFileURL), nil
		})

	storage := newSourceFileStorageMock(
		t,
		func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			uploadedKey = key
			return sourceFileURLForKey(key), nil
		},
		func(_ context.Context, key string, _ time.Duration) (string, error) {
			require.Equal(t, uploadedKey, key)
			return sourceFileURLForKey(key) + "?X-Amz-Signature=test", nil
		},
		func(_ context.Context, key string) error {
			require.Equal(t, oldKey, key)
			return cleanupErr
		},
	)

	observer := taskmocks.NewMockSourceFileCleanupObserver(t)
	observer.EXPECT().ObserveSourceFileCleanup(mock.Anything, taskusecase.SourceFileCleanupFailure{
		Operation: "upload_replace_old",
		TaskID:    taskID,
		ObjectKey: oldKey,
		Err:       cleanupErr,
	}).Once()

	got, err := taskusecase.NewSourceFiles(tasks, storage, nil, observer).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.NoError(t, err)
	require.Equal(t, sourceFileURLForKey(uploadedKey)+"?X-Amz-Signature=test", got)
}

func TestSourceFiles_UploadSourceFile_ReuploadUploadErrorDoesNotDeleteOldFile(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	oldURL := sourceFileURLForKey(sourceFileTestKey(taskID))
	payload := zipPayload("new")
	task := uploadTask(taskID, &oldURL)
	lowLevelErr := errors.New("storage down")

	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)

	storage := newSourceFileStorageMock(
		t,
		func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			return "", lowLevelErr
		},
		nil,
		nil,
	)

	_, err := taskusecase.NewSourceFiles(tasks, storage, nil).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.ErrorIs(t, err, lowLevelErr)
}

func TestSourceFiles_UploadSourceFile_UpdateErrorDeletesNewObject(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	payload := zipPayload("new")
	task := uploadTask(taskID, nil)
	lowLevelErr := errors.New("db down")
	var uploadedKey string

	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)
	tasks.On("UpdateTask", mock.Anything, taskID, taskInputWithVersionedSourceFileURL(taskID)).
		Return(nil, lowLevelErr)

	storage := newSourceFileStorageMock(
		t,
		func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			uploadedKey = key
			return sourceFileURLForKey(key), nil
		},
		func(_ context.Context, key string, _ time.Duration) (string, error) {
			require.Equal(t, uploadedKey, key)
			return sourceFileURLForKey(key) + "?X-Amz-Signature=test", nil
		},
		func(_ context.Context, key string) error {
			require.Equal(t, uploadedKey, key)
			return nil
		},
	)

	_, err := taskusecase.NewSourceFiles(tasks, storage, nil).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.ErrorIs(t, err, lowLevelErr)
}

func TestSourceFiles_UploadSourceFile_PresignErrorDeletesNewObjectAndSkipsUpdate(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	payload := zipPayload("new")
	task := uploadTask(taskID, nil)
	lowLevelErr := errors.New("presign down")
	var uploadedKey string

	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)

	storage := newSourceFileStorageMock(
		t,
		func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			uploadedKey = key
			return sourceFileURLForKey(key), nil
		},
		func(_ context.Context, key string, _ time.Duration) (string, error) {
			require.Equal(t, uploadedKey, key)
			return "", lowLevelErr
		},
		func(_ context.Context, key string) error {
			require.Equal(t, uploadedKey, key)
			return nil
		},
	)

	_, err := taskusecase.NewSourceFiles(tasks, storage, nil).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.ErrorIs(t, err, lowLevelErr)
}

func TestSourceFiles_UploadSourceFile_AcceptsWebTask(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	payload := zipPayload("new")
	task := uploadTask(taskID, nil)
	task.Category = domain.CategoryWeb
	var uploadedKey string

	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)
	tasks.On("UpdateTask", mock.Anything, taskID, taskInputWithVersionedSourceFileURLForCategory(taskID, domain.CategoryWeb)).
		Return(func(_ context.Context, _ uuid.UUID, in taskusecase.UpdateInput) (*domain.Task, error) {
			return taskWithSource(task, *in.SourceFileURL), nil
		})

	storage := newSourceFileStorageMock(
		t,
		func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			uploadedKey = key
			return sourceFileURLForKey(key), nil
		},
		func(_ context.Context, key string, _ time.Duration) (string, error) {
			require.Equal(t, uploadedKey, key)
			return sourceFileURLForKey(key) + "?X-Amz-Signature=test", nil
		},
		nil,
	)

	got, err := taskusecase.NewSourceFiles(tasks, storage, nil).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.NoError(t, err)
	require.Equal(t, sourceFileURLForKey(uploadedKey)+"?X-Amz-Signature=test", got)
}
