package task_test

import (
	"context"
	"errors"
	"testing"
	"time"

	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	taskmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSourceFiles_ClearSourceFile_UpdatesTaskAndDeletesObject(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	storedKey := sourceFileTestKey(taskID)
	storedURL := sourceFileURLForKey(storedKey)
	task := uploadTask(taskID, &storedURL)
	cleared := *task
	cleared.SourceFileURL = nil

	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)
	tasks.On("UpdateTask", mock.Anything, taskID, taskInputWithoutSourceFileURL()).
		Return(&cleared, nil)

	storage := newSourceFileStorageMock(
		t,
		nil,
		nil,
		func(_ context.Context, key string) error {
			require.Equal(t, storedKey, key)
			return nil
		},
	)

	got, err := taskusecase.NewSourceFiles(tasks, storage, nil).ClearSourceFile(
		t.Context(), taskID, taskInputFromTask(task),
	)

	require.NoError(t, err)
	require.Nil(t, got.SourceFileURL)
}

func TestSourceFiles_ClearSourceFile_DeleteErrorDoesNotFailCommittedClear(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	storedKey := sourceFileTestKey(taskID)
	storedURL := sourceFileURLForKey(storedKey)
	task := uploadTask(taskID, &storedURL)
	cleanupErr := errors.New("cleanup failed")
	cleared := *task
	cleared.SourceFileURL = nil

	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)
	tasks.On("UpdateTask", mock.Anything, taskID, taskInputWithoutSourceFileURL()).
		Return(&cleared, nil)

	storage := newSourceFileStorageMock(
		t,
		nil,
		nil,
		func(_ context.Context, key string) error {
			require.Equal(t, storedKey, key)
			return cleanupErr
		},
	)

	observer := taskmocks.NewMockSourceFileCleanupObserver(t)
	observer.EXPECT().ObserveSourceFileCleanup(mock.Anything, taskusecase.SourceFileCleanupFailure{
		Operation: "delete_source_file",
		TaskID:    taskID,
		ObjectKey: storedKey,
		Err:       cleanupErr,
	}).Once()

	got, err := taskusecase.NewSourceFiles(tasks, storage, nil, observer).ClearSourceFile(
		t.Context(), taskID, taskInputFromTask(task),
	)

	require.NoError(t, err)
	require.Nil(t, got.SourceFileURL)
}

func TestSourceFiles_ClearSourceFile_SkipsDeleteWhenNoSource(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	task := uploadTask(taskID, nil)

	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)
	tasks.On("UpdateTask", mock.Anything, taskID, taskInputWithoutSourceFileURL()).
		Return(task, nil)

	storage := newSourceFileStorageMock(t, nil, nil, nil)

	got, err := taskusecase.NewSourceFiles(tasks, storage, nil).ClearSourceFile(
		t.Context(), taskID, taskInputFromTask(task),
	)

	require.NoError(t, err)
	require.Nil(t, got.SourceFileURL)
}

func TestSourceFiles_DeleteSourceFile_DeletesVersionedKey(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	expectedKey := sourceFileTestKey(taskID)
	storedURL := sourceFileURLForKey(expectedKey)
	storage := newSourceFileStorageMock(
		t,
		nil,
		nil,
		func(_ context.Context, key string) error {
			require.Equal(t, expectedKey, key)
			return nil
		},
	)

	err := taskusecase.NewSourceFiles(newCatalogMock(t), storage, nil).DeleteSourceFile(t.Context(), taskID, &storedURL)

	require.NoError(t, err)
}

func TestSourceFiles_DeleteSourceFile_SkipsMissingURL(t *testing.T) {
	t.Parallel()

	storage := newSourceFileStorageMock(t, nil, nil, nil)
	err := taskusecase.NewSourceFiles(newCatalogMock(t), storage, nil).
		DeleteSourceFile(t.Context(), uuid.New(), nil)

	require.NoError(t, err)
}

func TestSourceFiles_DeleteSourceFile_RejectsLegacyKey(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	legacyURL := "http://seaweed/tpm/tasks/" + taskID.String() + "/source.zip"
	storage := newSourceFileStorageMock(t, nil, nil, nil)

	err := taskusecase.NewSourceFiles(newCatalogMock(t), storage, nil).
		DeleteSourceFile(t.Context(), taskID, &legacyURL)

	require.Error(t, err)
}

func TestSourceFiles_DeleteSourceFile_UsesCleanupRunner(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	key := sourceFileTestKey(taskID)
	storedURL := sourceFileURLForKey(key)
	storage := newSourceFileStorageMock(
		t,
		nil,
		nil,
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

	err := taskusecase.NewSourceFiles(newCatalogMock(t), storage, cleanup).
		DeleteSourceFile(t.Context(), taskID, &storedURL)

	require.NoError(t, err)
}
