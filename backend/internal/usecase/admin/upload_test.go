package admin_test

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

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
	usecasemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/mocks"
)

func TestUploadUsecase_UploadSourceFile_HappyPath(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	payload := zipPayload("hello")
	storedURL := "http://seaweed/tpm/" + admin.SourceFileKey(taskID)
	presignedURL := storedURL + "?X-Amz-Signature=test"
	task := uploadTask(taskID, nil)

	tasks := usecasemocks.NewMockTaskRepo(t)
	tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)
	tasks.EXPECT().
		Update(mock.Anything, taskID, taskInputWithSourceFileURL(storedURL)).
		Return(taskWithSource(task, storedURL), nil)

	storage := &sourceFileStorageMock{
		uploadFunc: func(_ context.Context, key string, r io.Reader, size int64) (string, error) {
			require.Equal(t, admin.SourceFileKey(taskID), key)
			require.Equal(t, int64(len(payload)), size)
			got, err := io.ReadAll(r)
			require.NoError(t, err)
			require.Equal(t, payload, got)
			return storedURL, nil
		},
		presignedGetURLFunc: func(_ context.Context, key string, ttl time.Duration) (string, error) {
			require.Equal(t, admin.SourceFileKey(taskID), key)
			require.Equal(t, time.Duration(task.TimeLimit)*time.Second, ttl)
			return presignedURL, nil
		},
	}

	got, err := admin.NewUploadUsecase(tasks, storage).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.NoError(t, err)
	require.Equal(t, presignedURL, got)
	require.Equal(t, []string{"upload", "presigned"}, storage.calls)
}

func TestUploadUsecase_UploadSourceFile_ReuploadDeletesOldFile(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	oldURL := "http://seaweed/tpm/" + admin.SourceFileKey(taskID)
	payload := zipPayload("new")
	storedURL := oldURL
	task := uploadTask(taskID, &oldURL)

	tasks := usecasemocks.NewMockTaskRepo(t)
	tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)
	tasks.EXPECT().
		Update(mock.Anything, taskID, taskInputWithSourceFileURL(storedURL)).
		Return(taskWithSource(task, storedURL), nil)

	storage := &sourceFileStorageMock{
		deleteFunc: func(_ context.Context, key string) error {
			require.Equal(t, admin.SourceFileKey(taskID), key)
			return nil
		},
		uploadFunc: func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			require.Equal(t, admin.SourceFileKey(taskID), key)
			return storedURL, nil
		},
		presignedGetURLFunc: func(_ context.Context, _ string, _ time.Duration) (string, error) {
			return storedURL + "?X-Amz-Signature=test", nil
		},
	}

	_, err := admin.NewUploadUsecase(tasks, storage).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.NoError(t, err)
	require.Equal(t, []string{"delete", "upload", "presigned"}, storage.calls)
}

func TestUploadUsecase_UploadSourceFile_Validation(t *testing.T) {
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
			size:        admin.MaxSourceFileSize + 1,
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

			got, err := admin.NewUploadUsecase(
				usecasemocks.NewMockTaskRepo(t),
				&sourceFileStorageMock{},
			).UploadSourceFile(t.Context(), uuid.New(), bytes.NewReader(tt.payload), tt.size, tt.contentType)

			require.Empty(t, got)
			require.ErrorIs(t, err, apperr.ErrTaskValidation)
		})
	}
}

func TestUploadUsecase_UploadSourceFile_StorageErrorIsWrapped(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	payload := zipPayload("x")
	task := uploadTask(taskID, nil)
	lowLevelErr := errors.New("storage down")

	tasks := usecasemocks.NewMockTaskRepo(t)
	tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)
	storage := &sourceFileStorageMock{
		uploadFunc: func(_ context.Context, _ string, _ io.Reader, _ int64) (string, error) {
			return "", lowLevelErr
		},
	}

	_, err := admin.NewUploadUsecase(tasks, storage).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.ErrorIs(t, err, lowLevelErr)
}

func zipPayload(body string) []byte {
	return append([]byte{'P', 'K', 0x03, 0x04}, []byte(body)...)
}

func uploadTask(id uuid.UUID, sourceFileURL *string) *domain.Task {
	return &domain.Task{
		ID:            id,
		Title:         "task",
		Description:   "description",
		Category:      domain.CategoryForensics,
		Difficulty:    domain.DifficultyEasy,
		TimeLimit:     90,
		Flag:          "FLAG{task}",
		Hints:         []string{"first hint", "second hint", "third hint"},
		SourceFileURL: sourceFileURL,
		CreatedAt:     time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	}
}

func taskWithSource(task *domain.Task, sourceFileURL string) *domain.Task {
	updated := *task
	updated.SourceFileURL = &sourceFileURL
	return &updated
}

func taskInputWithSourceFileURL(sourceFileURL string) interface{} {
	return mock.MatchedBy(func(in usecase.TaskInput) bool {
		return in.Title == "task" &&
			in.Description == "description" &&
			in.Category == domain.CategoryForensics &&
			in.Difficulty == domain.DifficultyEasy &&
			in.TimeLimit == 90 &&
			in.Flag == "FLAG{task}" &&
			len(in.Hints) == 3 &&
			in.Hints[0] == "first hint" &&
			in.Hints[1] == "second hint" &&
			in.Hints[2] == "third hint" &&
			in.TaskURL == nil &&
			in.SourceFileURL != nil &&
			*in.SourceFileURL == sourceFileURL
	})
}

type sourceFileStorageMock struct {
	calls               []string
	uploadFunc          func(context.Context, string, io.Reader, int64) (string, error)
	presignedGetURLFunc func(context.Context, string, time.Duration) (string, error)
	deleteFunc          func(context.Context, string) error
}

func (m *sourceFileStorageMock) Upload(ctx context.Context, key string, r io.Reader, size int64) (string, error) {
	m.calls = append(m.calls, "upload")
	if m.uploadFunc == nil {
		return "", nil
	}
	return m.uploadFunc(ctx, key, r, size)
}

func (m *sourceFileStorageMock) PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	m.calls = append(m.calls, "presigned")
	if m.presignedGetURLFunc == nil {
		return "", nil
	}
	return m.presignedGetURLFunc(ctx, key, ttl)
}

func (m *sourceFileStorageMock) Delete(ctx context.Context, key string) error {
	m.calls = append(m.calls, "delete")
	if m.deleteFunc == nil {
		return nil
	}
	return m.deleteFunc(ctx, key)
}
