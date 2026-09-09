package task_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
)

func TestSourceFiles_PresignedSourceFileURL_HappyPath(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	key := sourceFileTestKey(taskID)
	sourceURL := sourceFileURLForKey(key)
	task := uploadTask(taskID, &sourceURL)

	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)

	storage := newSourceFileStorageMock(
		t,
		nil,
		func(_ context.Context, gotKey string, ttl time.Duration) (string, error) {
			require.Equal(t, key, gotKey)
			require.Equal(t, time.Duration(task.TimeLimit)*time.Second, ttl)
			return sourceURL + "?X-Amz-Signature=test", nil
		},
		nil,
	)

	got, err := taskusecase.NewSourceFiles(tasks, storage, nil).PresignedSourceFileURL(t.Context(), taskID)

	require.NoError(t, err)
	require.Equal(t, sourceURL+"?X-Amz-Signature=test", got)
}

func TestSourceFiles_PresignedSourceFileURL_NoSource(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(uploadTask(taskID, nil), nil)

	storage := newSourceFileStorageMock(t, nil, nil, nil)
	_, err := taskusecase.NewSourceFiles(tasks, storage, nil).PresignedSourceFileURL(t.Context(), taskID)

	require.ErrorIs(t, err, domain.ErrTaskNotFound)
}

func TestSourceFiles_PresignedSourceFileURL_RejectsLegacyKey(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	legacyURL := "http://seaweed/tpm/tasks/" + taskID.String() + "/source.zip"
	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(uploadTask(taskID, &legacyURL), nil)

	storage := newSourceFileStorageMock(t, nil, nil, nil)
	_, err := taskusecase.NewSourceFiles(tasks, storage, nil).PresignedSourceFileURL(t.Context(), taskID)

	require.Error(t, err)
}
