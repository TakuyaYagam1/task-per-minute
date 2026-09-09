package task_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
)

func TestSourceFiles_UploadSourceFile_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		payload     []byte
		size        int64
		contentType string
	}{
		{
			name:        "too large",
			payload:     zipPayload("x"),
			size:        taskusecase.MaxSourceFileSize + 1,
			contentType: "application/zip",
		},
		{
			name:        "invalid content type",
			payload:     zipPayload("x"),
			size:        int64(len(zipPayload("x"))),
			contentType: "text/plain",
		},
		{
			name:        "corrupt zip signature",
			payload:     []byte("NOPE archive"),
			size:        int64(len("NOPE archive")),
			contentType: "application/zip",
		},
		{
			name:        "too small",
			payload:     []byte("PK"),
			size:        2,
			contentType: "application/zip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			storage := newSourceFileStorageMock(t, nil, nil, nil)
			got, err := taskusecase.NewSourceFiles(
				newCatalogMock(t),
				storage,
				nil,
			).UploadSourceFile(t.Context(), uuid.New(), bytes.NewReader(tt.payload), tt.size, tt.contentType)

			require.Empty(t, got)
			require.ErrorIs(t, err, domain.ErrTaskValidation)
		})
	}
}

func TestSourceFiles_UploadSourceFile_StorageErrorIsWrapped(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	payload := zipPayload("x")
	task := uploadTask(taskID, nil)
	lowLevelErr := errors.New("storage down")

	tasks := newCatalogMock(t)
	tasks.On("GetTask", mock.Anything, taskID).Return(task, nil)
	storage := newSourceFileStorageMock(
		t,
		func(_ context.Context, _ string, _ io.Reader, _ int64) (string, error) {
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
