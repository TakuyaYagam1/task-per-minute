package task_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	taskmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task/mocks"
)

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
		Kind:          domain.TaskKindNormal,
		Enabled:       true,
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

func taskInputFromTask(task *domain.Task) taskusecase.UpdateInput {
	return taskusecase.UpdateInput{
		Title:         task.Title,
		Description:   task.Description,
		Category:      task.Category,
		Difficulty:    task.Difficulty,
		TimeLimit:     task.TimeLimit,
		Flag:          task.Flag,
		Kind:          task.Kind,
		Enabled:       task.Enabled,
		Hints:         append([]string(nil), task.Hints...),
		TaskURL:       task.TaskURL,
		SourceFileURL: task.SourceFileURL,
	}
}

func taskInputWithVersionedSourceFileURL(taskID uuid.UUID) interface{} {
	return taskInputWithVersionedSourceFileURLForCategory(taskID, domain.CategoryForensics)
}

func taskInputWithVersionedSourceFileURLForCategory(taskID uuid.UUID, category domain.Category) interface{} {
	return mock.MatchedBy(func(in taskusecase.UpdateInput) bool {
		return in.Title == "task" &&
			in.Description == "description" &&
			in.Category == category &&
			in.Difficulty == domain.DifficultyEasy &&
			in.TimeLimit == 90 &&
			in.Flag == "FLAG{task}" &&
			in.Kind == domain.TaskKindNormal &&
			in.Enabled &&
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
	return mock.MatchedBy(func(in taskusecase.UpdateInput) bool {
		return in.Title == "task" &&
			in.Description == "description" &&
			in.Category == domain.CategoryForensics &&
			in.Difficulty == domain.DifficultyEasy &&
			in.TimeLimit == 90 &&
			in.Flag == "FLAG{task}" &&
			in.Kind == domain.TaskKindNormal &&
			in.Enabled &&
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

func sourceFileTestKey(taskID uuid.UUID) string {
	return "tasks/" + taskID.String() + "/sources/" + uuid.NewString() + ".zip"
}

func newCatalogMock(t *testing.T) *taskmocks.MockCatalog {
	t.Helper()
	return taskmocks.NewMockCatalog(t)
}

func newCleanupRunnerMock(t *testing.T) *taskmocks.MockCleanupRunner {
	t.Helper()
	return taskmocks.NewMockCleanupRunner(t)
}

func newSourceFileStorageMock(
	t *testing.T,
	upload func(context.Context, string, io.Reader, int64) (string, error),
	presignedGetURL func(context.Context, string, time.Duration) (string, error),
	deleteObject func(context.Context, string) error,
) *taskmocks.MockSourceFileStorage {
	t.Helper()

	storage := taskmocks.NewMockSourceFileStorage(t)
	var previous *mock.Call
	if upload != nil {
		previous = storage.On(
			"Upload",
			mock.Anything,
			mock.Anything,
			mock.Anything,
			mock.Anything,
		).Return(upload).Once()
	}
	if presignedGetURL != nil {
		call := storage.On(
			"PresignedGetURL",
			mock.Anything,
			mock.Anything,
			mock.Anything,
		).Return(presignedGetURL).Once()
		if previous != nil {
			call.NotBefore(previous)
		}
		previous = call
	}
	if deleteObject != nil {
		call := storage.On(
			"Delete",
			mock.Anything,
			mock.Anything,
		).Return(deleteObject).Once()
		if previous != nil {
			call.NotBefore(previous)
		}
	}
	return storage
}
