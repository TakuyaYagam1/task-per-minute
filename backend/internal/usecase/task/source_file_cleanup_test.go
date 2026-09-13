package task_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	taskmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSourceFiles_ClearSourceFile_UpdatesTaskAndRetainsObject(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	storedURL := sourceFileURLForKey(sourceFileTestKey(taskID))
	task := uploadTask(taskID, &storedURL)
	cleared := *task
	cleared.SourceFileURL = nil

	tasks := newCatalogMock(t)
	tasks.On("UpdateTask", mock.Anything, taskID, taskInputWithoutSourceFileURL()).
		Return(&cleared, nil)

	storage := newSourceFileStorageMock(t, nil, nil, nil)

	got, err := taskusecase.NewSourceFiles(tasks, storage, nil).ClearSourceFile(
		t.Context(), taskID, taskInputFromTask(task),
	)

	require.NoError(t, err)
	require.Nil(t, got.SourceFileURL)
}

func TestSourceFiles_ClearSourceFile_UpdateErrorRetainsObject(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	storedURL := sourceFileURLForKey(sourceFileTestKey(taskID))
	task := uploadTask(taskID, &storedURL)
	updateErr := errors.New("db result unavailable")

	tasks := newCatalogMock(t)
	tasks.On("UpdateTask", mock.Anything, taskID, taskInputWithoutSourceFileURL()).
		Return(nil, updateErr)

	got, err := taskusecase.NewSourceFiles(tasks, newSourceFileStorageMock(t, nil, nil, nil), nil).ClearSourceFile(
		t.Context(), taskID, taskInputFromTask(task),
	)

	require.ErrorIs(t, err, updateErr)
	require.Nil(t, got)
}

func TestSourceFiles_ClearSourceFile_SkipsDeleteWhenNoSource(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	task := uploadTask(taskID, nil)

	tasks := newCatalogMock(t)
	tasks.On("UpdateTask", mock.Anything, taskID, taskInputWithoutSourceFileURL()).
		Return(task, nil)

	storage := newSourceFileStorageMock(t, nil, nil, nil)

	got, err := taskusecase.NewSourceFiles(tasks, storage, nil).ClearSourceFile(
		t.Context(), taskID, taskInputFromTask(task),
	)

	require.NoError(t, err)
	require.Nil(t, got.SourceFileURL)
}

func TestSourceFiles_PresignFailureUsesCleanupRunner(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	payload := zipPayload("new")
	task := uploadTask(taskID, nil)
	presignErr := errors.New("presign failed")
	var key string
	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)
	storage := newSourceFileStorageMock(
		t,
		func(_ context.Context, uploadedKey string, _ io.Reader, _ int64) (string, error) {
			key = uploadedKey
			return sourceFileURLForKey(uploadedKey), nil
		},
		func(_ context.Context, _ string, _ time.Duration) (string, error) {
			return "", presignErr
		},
		func(_ context.Context, gotKey string) error {
			require.Equal(t, key, gotKey)
			return nil
		},
	)
	cleanup := newCleanupRunnerMock(t)
	cleanup.EXPECT().
		Run(mock.Anything, 10*time.Second, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ time.Duration, run func(context.Context) error) error {
			return run(ctx)
		}).
		Once()

	_, err := taskusecase.NewSourceFiles(tasks, storage, cleanup).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.ErrorIs(t, err, presignErr)
}

func TestSourceFiles_PresignFailureReportsFailedCleanup(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	payload := zipPayload("new")
	presignErr := errors.New("presign failed")
	cleanupErr := errors.New("cleanup failed")
	var key string
	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(uploadTask(taskID, nil), nil)
	storage := newSourceFileStorageMock(
		t,
		func(_ context.Context, uploadedKey string, _ io.Reader, _ int64) (string, error) {
			key = uploadedKey
			return sourceFileURLForKey(uploadedKey), nil
		},
		func(_ context.Context, _ string, _ time.Duration) (string, error) {
			return "", presignErr
		},
		func(_ context.Context, gotKey string) error {
			require.Equal(t, key, gotKey)
			return cleanupErr
		},
	)
	observer := taskmocks.NewMockSourceFileCleanupObserver(t)
	observer.EXPECT().ObserveSourceFileCleanup(
		mock.Anything,
		mock.MatchedBy(func(failure taskusecase.SourceFileCleanupFailure) bool {
			return failure.Operation == "upload_presign_failed" &&
				failure.TaskID == taskID && failure.ObjectKey == key && errors.Is(failure.Err, cleanupErr)
		}),
	).Once()

	_, err := taskusecase.NewSourceFiles(tasks, storage, nil, observer).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.ErrorIs(t, err, presignErr)
}
