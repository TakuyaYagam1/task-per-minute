package admin_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

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
	task := uploadTask(taskID, nil)
	var uploadedKey string

	tasks := usecasemocks.NewMockTaskRepo(t)
	tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)
	tasks.EXPECT().
		Update(mock.Anything, taskID, taskInputWithVersionedSourceFileURL(taskID)).
		RunAndReturn(func(_ context.Context, _ uuid.UUID, in usecase.TaskInput) (*domain.Task, error) {
			return taskWithSource(task, *in.SourceFileURL), nil
		})

	storage := &sourceFileStorageMock{
		uploadFunc: func(_ context.Context, key string, r io.Reader, size int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			require.Equal(t, int64(len(payload)), size)
			got, err := io.ReadAll(r)
			require.NoError(t, err)
			require.Equal(t, payload, got)
			uploadedKey = key
			return sourceFileURLForKey(key), nil
		},
		presignedGetURLFunc: func(_ context.Context, key string, ttl time.Duration) (string, error) {
			require.Equal(t, uploadedKey, key)
			require.Equal(t, time.Duration(task.TimeLimit)*time.Second, ttl)
			return sourceFileURLForKey(key) + "?X-Amz-Signature=test", nil
		},
	}

	got, err := admin.NewUploadUsecase(tasks, storage).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.NoError(t, err)
	require.Equal(t, sourceFileURLForKey(uploadedKey)+"?X-Amz-Signature=test", got)
	require.Equal(t, []string{"upload", "presigned"}, storage.calls)
}

func TestUploadUsecase_UploadSourceFile_AcceptsCommonZIPContentTypes(t *testing.T) {
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

			tasks := usecasemocks.NewMockTaskRepo(t)
			tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)
			tasks.EXPECT().
				Update(mock.Anything, taskID, taskInputWithVersionedSourceFileURL(taskID)).
				RunAndReturn(func(_ context.Context, _ uuid.UUID, in usecase.TaskInput) (*domain.Task, error) {
					return taskWithSource(task, *in.SourceFileURL), nil
				})

			storage := &sourceFileStorageMock{
				uploadFunc: func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
					requireVersionedSourceKey(t, taskID, key)
					return sourceFileURLForKey(key), nil
				},
				presignedGetURLFunc: func(_ context.Context, key string, _ time.Duration) (string, error) {
					return sourceFileURLForKey(key) + "?X-Amz-Signature=test", nil
				},
			}

			got, err := admin.NewUploadUsecase(tasks, storage).UploadSourceFile(
				t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), tt.contentType,
			)

			require.NoError(t, err)
			require.NotEmpty(t, got)
		})
	}
}

func TestUploadUsecase_UploadSourceFile_ReuploadUsesVersionedKeyAndDeletesOldObject(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	oldKey := admin.SourceFileKey(taskID)
	oldURL := sourceFileURLForKey(oldKey)
	payload := zipPayload("new")
	task := uploadTask(taskID, &oldURL)
	var uploadedKey string

	tasks := usecasemocks.NewMockTaskRepo(t)
	tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)
	tasks.EXPECT().
		Update(mock.Anything, taskID, taskInputWithVersionedSourceFileURL(taskID)).
		RunAndReturn(func(_ context.Context, _ uuid.UUID, in usecase.TaskInput) (*domain.Task, error) {
			return taskWithSource(task, *in.SourceFileURL), nil
		})

	storage := &sourceFileStorageMock{
		deleteFunc: func(_ context.Context, key string) error {
			require.Equal(t, oldKey, key)
			return nil
		},
		uploadFunc: func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			uploadedKey = key
			return sourceFileURLForKey(key), nil
		},
		presignedGetURLFunc: func(_ context.Context, key string, _ time.Duration) (string, error) {
			require.Equal(t, uploadedKey, key)
			return sourceFileURLForKey(key) + "?X-Amz-Signature=test", nil
		},
	}

	_, err := admin.NewUploadUsecase(tasks, storage).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.NoError(t, err)
	require.Equal(t, []string{"upload", "presigned", "delete"}, storage.calls)
}

func TestUploadUsecase_UploadSourceFile_OldDeleteErrorDoesNotFailCommittedUpload(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	oldKey := admin.SourceFileKey(taskID)
	oldURL := sourceFileURLForKey(oldKey)
	payload := zipPayload("new")
	task := uploadTask(taskID, &oldURL)
	var uploadedKey string

	tasks := usecasemocks.NewMockTaskRepo(t)
	tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)
	tasks.EXPECT().
		Update(mock.Anything, taskID, taskInputWithVersionedSourceFileURL(taskID)).
		RunAndReturn(func(_ context.Context, _ uuid.UUID, in usecase.TaskInput) (*domain.Task, error) {
			return taskWithSource(task, *in.SourceFileURL), nil
		})

	storage := &sourceFileStorageMock{
		deleteFunc: func(_ context.Context, key string) error {
			require.Equal(t, oldKey, key)
			return errors.New("cleanup failed")
		},
		uploadFunc: func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			uploadedKey = key
			return sourceFileURLForKey(key), nil
		},
		presignedGetURLFunc: func(_ context.Context, key string, _ time.Duration) (string, error) {
			require.Equal(t, uploadedKey, key)
			return sourceFileURLForKey(key) + "?X-Amz-Signature=test", nil
		},
	}

	var logs bytes.Buffer
	log := newUploadTestLogger(t, &logs)

	got, err := admin.NewUploadUsecase(tasks, storage).
		Configure(admin.WithUploadLogger(log)).
		UploadSourceFile(
			t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
		)

	require.NoError(t, err)
	require.Equal(t, sourceFileURLForKey(uploadedKey)+"?X-Amz-Signature=test", got)
	require.Equal(t, []string{"upload", "presigned", "delete"}, storage.calls)
	require.Contains(t, logs.String(), "source file cleanup failed")
	require.Contains(t, logs.String(), "upload_replace_old")
	require.Contains(t, logs.String(), oldKey)
	require.Contains(t, logs.String(), "cleanup failed")
}

func TestUploadUsecase_UploadSourceFile_ReuploadUploadErrorDoesNotDeleteOldFile(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	oldURL := "http://seaweed/tpm/" + admin.SourceFileKey(taskID)
	payload := zipPayload("new")
	task := uploadTask(taskID, &oldURL)
	lowLevelErr := errors.New("storage down")

	tasks := usecasemocks.NewMockTaskRepo(t)
	tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)

	storage := &sourceFileStorageMock{
		deleteFunc: func(context.Context, string) error {
			t.Fatal("Delete must not be called before a replacement upload")
			return nil
		},
		uploadFunc: func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			return "", lowLevelErr
		},
	}

	_, err := admin.NewUploadUsecase(tasks, storage).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.ErrorIs(t, err, lowLevelErr)
	require.Equal(t, []string{"upload"}, storage.calls)
}

func TestUploadUsecase_UploadSourceFile_UpdateErrorDeletesNewObject(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	payload := zipPayload("new")
	task := uploadTask(taskID, nil)
	lowLevelErr := errors.New("db down")
	var uploadedKey string

	tasks := usecasemocks.NewMockTaskRepo(t)
	tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)
	tasks.EXPECT().
		Update(mock.Anything, taskID, taskInputWithVersionedSourceFileURL(taskID)).
		Return(nil, lowLevelErr)

	storage := &sourceFileStorageMock{
		deleteFunc: func(_ context.Context, key string) error {
			require.Equal(t, uploadedKey, key)
			return nil
		},
		uploadFunc: func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			uploadedKey = key
			return sourceFileURLForKey(key), nil
		},
		presignedGetURLFunc: func(_ context.Context, key string, _ time.Duration) (string, error) {
			require.Equal(t, uploadedKey, key)
			return sourceFileURLForKey(key) + "?X-Amz-Signature=test", nil
		},
	}

	_, err := admin.NewUploadUsecase(tasks, storage).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.ErrorIs(t, err, lowLevelErr)
	require.Equal(t, []string{"upload", "presigned", "delete"}, storage.calls)
}

func TestUploadUsecase_UploadSourceFile_PresignErrorDeletesNewObjectAndSkipsUpdate(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	payload := zipPayload("new")
	task := uploadTask(taskID, nil)
	lowLevelErr := errors.New("presign down")
	var uploadedKey string

	tasks := usecasemocks.NewMockTaskRepo(t)
	tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)

	storage := &sourceFileStorageMock{
		deleteFunc: func(_ context.Context, key string) error {
			require.Equal(t, uploadedKey, key)
			return nil
		},
		uploadFunc: func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			uploadedKey = key
			return sourceFileURLForKey(key), nil
		},
		presignedGetURLFunc: func(_ context.Context, key string, _ time.Duration) (string, error) {
			require.Equal(t, uploadedKey, key)
			return "", lowLevelErr
		},
	}

	_, err := admin.NewUploadUsecase(tasks, storage).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.ErrorIs(t, err, lowLevelErr)
	require.Equal(t, []string{"upload", "presigned", "delete"}, storage.calls)
}

func TestUploadUsecase_UploadSourceFile_AcceptsWebTask(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	payload := zipPayload("new")
	task := uploadTask(taskID, nil)
	task.Category = domain.CategoryWeb
	var uploadedKey string

	tasks := usecasemocks.NewMockTaskRepo(t)
	tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)
	tasks.EXPECT().
		Update(mock.Anything, taskID, taskInputWithVersionedSourceFileURLForCategory(taskID, domain.CategoryWeb)).
		RunAndReturn(func(_ context.Context, _ uuid.UUID, in usecase.TaskInput) (*domain.Task, error) {
			return taskWithSource(task, *in.SourceFileURL), nil
		})

	storage := &sourceFileStorageMock{
		uploadFunc: func(_ context.Context, key string, _ io.Reader, _ int64) (string, error) {
			requireVersionedSourceKey(t, taskID, key)
			uploadedKey = key
			return sourceFileURLForKey(key), nil
		},
		presignedGetURLFunc: func(_ context.Context, key string, _ time.Duration) (string, error) {
			require.Equal(t, uploadedKey, key)
			return sourceFileURLForKey(key) + "?X-Amz-Signature=test", nil
		},
	}

	got, err := admin.NewUploadUsecase(tasks, storage).UploadSourceFile(
		t.Context(), taskID, bytes.NewReader(payload), int64(len(payload)), "application/zip",
	)

	require.NoError(t, err)
	require.Equal(t, sourceFileURLForKey(uploadedKey)+"?X-Amz-Signature=test", got)
	require.Equal(t, []string{"upload", "presigned"}, storage.calls)
}

func TestUploadUsecase_ClearSourceFile_UpdatesTaskAndDeletesObject(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	storedURL := "http://seaweed/tpm/" + admin.SourceFileKey(taskID)
	task := uploadTask(taskID, &storedURL)
	cleared := *task
	cleared.SourceFileURL = nil

	tasks := usecasemocks.NewMockTaskRepo(t)
	tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)
	tasks.EXPECT().
		Update(mock.Anything, taskID, taskInputWithoutSourceFileURL()).
		Return(&cleared, nil)

	storage := &sourceFileStorageMock{
		deleteFunc: func(_ context.Context, key string) error {
			require.Equal(t, admin.SourceFileKey(taskID), key)
			return nil
		},
	}

	got, err := admin.NewUploadUsecase(tasks, storage).ClearSourceFile(
		t.Context(), taskID, taskInputFromTask(task),
	)

	require.NoError(t, err)
	require.Nil(t, got.SourceFileURL)
	require.Equal(t, []string{"delete"}, storage.calls)
}

func TestUploadUsecase_ClearSourceFile_DeleteErrorDoesNotFailCommittedClear(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	storedURL := "http://seaweed/tpm/" + admin.SourceFileKey(taskID)
	task := uploadTask(taskID, &storedURL)
	cleared := *task
	cleared.SourceFileURL = nil

	tasks := usecasemocks.NewMockTaskRepo(t)
	tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)
	tasks.EXPECT().
		Update(mock.Anything, taskID, taskInputWithoutSourceFileURL()).
		Return(&cleared, nil)

	storage := &sourceFileStorageMock{
		deleteFunc: func(_ context.Context, key string) error {
			require.Equal(t, admin.SourceFileKey(taskID), key)
			return errors.New("cleanup failed")
		},
	}

	var logs bytes.Buffer
	log := newUploadTestLogger(t, &logs)

	got, err := admin.NewUploadUsecase(tasks, storage).
		Configure(admin.WithUploadLogger(log)).
		ClearSourceFile(
			t.Context(), taskID, taskInputFromTask(task),
		)

	require.NoError(t, err)
	require.Nil(t, got.SourceFileURL)
	require.Equal(t, []string{"delete"}, storage.calls)
	require.Contains(t, logs.String(), "source file cleanup failed")
	require.Contains(t, logs.String(), "delete_source_file")
	require.Contains(t, logs.String(), admin.SourceFileKey(taskID))
	require.Contains(t, logs.String(), "cleanup failed")
}

func TestUploadUsecase_ClearSourceFile_SkipsDeleteWhenNoSource(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	task := uploadTask(taskID, nil)

	tasks := usecasemocks.NewMockTaskRepo(t)
	tasks.EXPECT().GetByID(mock.Anything, taskID).Return(task, nil)
	tasks.EXPECT().
		Update(mock.Anything, taskID, taskInputWithoutSourceFileURL()).
		Return(task, nil)

	storage := &sourceFileStorageMock{
		deleteFunc: func(context.Context, string) error {
			t.Fatal("Delete must not be called when no source file is stored")
			return nil
		},
	}

	got, err := admin.NewUploadUsecase(tasks, storage).ClearSourceFile(
		t.Context(), taskID, taskInputFromTask(task),
	)

	require.NoError(t, err)
	require.Nil(t, got.SourceFileURL)
	require.Empty(t, storage.calls)
}

func TestUploadUsecase_DeleteSourceFile_DeletesStableKey(t *testing.T) {
	t.Parallel()

	taskID := uuid.New()
	storage := &sourceFileStorageMock{
		deleteFunc: func(_ context.Context, key string) error {
			require.Equal(t, admin.SourceFileKey(taskID), key)
			return nil
		},
	}

	err := admin.NewUploadUsecase(usecasemocks.NewMockTaskRepo(t), storage).DeleteSourceFile(t.Context(), taskID, nil)

	require.NoError(t, err)
	require.Equal(t, []string{"delete"}, storage.calls)
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

func taskInputFromTask(task *domain.Task) usecase.TaskInput {
	return usecase.TaskInput{
		Title:         task.Title,
		Description:   task.Description,
		Category:      task.Category,
		Difficulty:    task.Difficulty,
		TimeLimit:     task.TimeLimit,
		Flag:          task.Flag,
		Hints:         append([]string(nil), task.Hints...),
		TaskURL:       task.TaskURL,
		SourceFileURL: task.SourceFileURL,
	}
}

func taskInputWithVersionedSourceFileURL(taskID uuid.UUID) interface{} {
	return taskInputWithVersionedSourceFileURLForCategory(taskID, domain.CategoryForensics)
}

func taskInputWithVersionedSourceFileURLForCategory(taskID uuid.UUID, category domain.Category) interface{} {
	return mock.MatchedBy(func(in usecase.TaskInput) bool {
		return in.Title == "task" &&
			in.Description == "description" &&
			in.Category == category &&
			in.Difficulty == domain.DifficultyEasy &&
			in.TimeLimit == 90 &&
			in.Flag == "FLAG{task}" &&
			len(in.Hints) == 3 &&
			in.Hints[0] == "first hint" &&
			in.Hints[1] == "second hint" &&
			in.Hints[2] == "third hint" &&
			in.TaskURL == nil &&
			in.SourceFileURL != nil &&
			strings.Contains(*in.SourceFileURL, "tasks/"+taskID.String()+"/sources/") &&
			strings.HasSuffix(*in.SourceFileURL, ".zip")
	})
}

func taskInputWithoutSourceFileURL() interface{} {
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
			in.SourceFileURL == nil
	})
}

func requireVersionedSourceKey(t *testing.T, taskID uuid.UUID, key string) {
	t.Helper()

	require.True(t, strings.HasPrefix(key, "tasks/"+taskID.String()+"/sources/"), "key %q must use versioned source prefix", key)
	require.True(t, strings.HasSuffix(key, ".zip"), "key %q must keep zip suffix", key)
}

func sourceFileURLForKey(key string) string {
	return "http://seaweed/tpm/" + key
}

func newUploadTestLogger(t *testing.T, logs *bytes.Buffer) logkit.Logger {
	t.Helper()

	log, err := logkit.New(
		logkit.WithLevel(logkit.DebugLevel),
		logkit.WithSyncWriter(logs),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, log.Close())
	})
	return log
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
