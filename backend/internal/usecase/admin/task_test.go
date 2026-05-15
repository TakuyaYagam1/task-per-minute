package admin_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
	usecasemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/mocks"
)

func TestTaskUsecase_CreateTask(t *testing.T) {
	t.Parallel()

	tasks := usecasemocks.NewMockTaskRepo(t)
	in := validTaskInput()
	taskURL := "pwn.example.com:31337"
	in.TaskURL = &taskURL
	created := taskFromInput(uuid.New(), in)
	tasks.EXPECT().Create(mock.Anything, in).Return(created, nil)

	got, err := admin.NewTaskUsecase(tasks).CreateTask(t.Context(), in)
	require.NoError(t, err)
	require.Same(t, created, got)
}

func TestTaskUsecase_CreateTask_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*admin.TaskInput)
	}{
		{"empty title", func(in *admin.TaskInput) { in.Title = "" }},
		{"too long title", func(in *admin.TaskInput) { in.Title = strings.Repeat("a", 256) }},
		{"empty description", func(in *admin.TaskInput) { in.Description = " " }},
		{"invalid category", func(in *admin.TaskInput) { in.Category = domain.Category("network") }},
		{"invalid difficulty", func(in *admin.TaskInput) { in.Difficulty = domain.Difficulty("insane") }},
		{"zero time limit", func(in *admin.TaskInput) { in.TimeLimit = 0 }},
		{"empty flag", func(in *admin.TaskInput) { in.Flag = "" }},
		{"too long flag", func(in *admin.TaskInput) { in.Flag = strings.Repeat("ф", 256) }},
		{"relative task url", func(in *admin.TaskInput) { url := "/tasks/1"; in.TaskURL = &url }},
		{"unsupported task url scheme", func(in *admin.TaskInput) { url := "ftp://example.com/task"; in.TaskURL = &url }},
		{"invalid source file url", func(in *admin.TaskInput) { url := "not-a-url"; in.SourceFileURL = &url }},
		{"unsupported source file url scheme", func(in *admin.TaskInput) { url := "ftp://example.com/source.zip"; in.SourceFileURL = &url }},
		{"too many hints", func(in *admin.TaskInput) { in.Hints = []string{"one", "two", "three", "four"} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := validTaskInput()
			tt.mutate(&in)
			_, err := admin.NewTaskUsecase(usecasemocks.NewMockTaskRepo(t)).CreateTask(t.Context(), in)
			require.ErrorIs(t, err, apperr.ErrTaskValidation)
		})
	}
}

func TestTaskUsecase_CreateTask_NormalizesPositionalHints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		hints []string
		want  []string
	}{
		{name: "missing", hints: nil, want: []string{"", "", ""}},
		{name: "first only", hints: []string{" first "}, want: []string{"first", "", ""}},
		{name: "third only", hints: []string{"", " ", " third "}, want: []string{"", "", "third"}},
		{name: "first and third", hints: []string{" first ", "", " third "}, want: []string{"first", "", "third"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tasks := usecasemocks.NewMockTaskRepo(t)
			in := validTaskInput()
			in.Hints = tt.hints
			normalized := in
			normalized.Hints = tt.want
			created := taskFromInput(uuid.New(), normalized)
			tasks.EXPECT().Create(mock.Anything, normalized).Return(created, nil)

			got, err := admin.NewTaskUsecase(tasks).CreateTask(t.Context(), in)
			require.NoError(t, err)
			require.Same(t, created, got)
		})
	}
}

func TestTaskUsecase_GetListUpdate(t *testing.T) {
	t.Parallel()

	tasks := usecasemocks.NewMockTaskRepo(t)
	uc := admin.NewTaskUsecase(tasks)
	id := uuid.New()
	in := validTaskInput()
	task := taskFromInput(id, in)

	tasks.EXPECT().GetByID(mock.Anything, id).Return(task, nil)
	got, err := uc.GetTask(t.Context(), id)
	require.NoError(t, err)
	require.Same(t, task, got)

	tasks.EXPECT().List(mock.Anything).Return([]*domain.Task{task}, nil)
	list, err := uc.ListTasks(t.Context())
	require.NoError(t, err)
	require.Equal(t, []*domain.Task{task}, list)

	updatedInput := validTaskInput()
	updatedInput.Title = "updated"
	updated := taskFromInput(id, updatedInput)
	tasks.EXPECT().Update(mock.Anything, id, updatedInput).Return(updated, nil)
	got, err = uc.UpdateTask(t.Context(), id, updatedInput)
	require.NoError(t, err)
	require.Same(t, updated, got)
}

func TestTaskUsecase_UpdateTask_Validation(t *testing.T) {
	t.Parallel()

	in := validTaskInput()
	in.Difficulty = domain.Difficulty("bad")

	_, err := admin.NewTaskUsecase(usecasemocks.NewMockTaskRepo(t)).UpdateTask(t.Context(), uuid.New(), in)
	require.ErrorIs(t, err, apperr.ErrTaskValidation)
}

func TestTaskUsecase_DeleteTask_UnusedDeletes(t *testing.T) {
	t.Parallel()

	tasks := usecasemocks.NewMockTaskRepo(t)
	id := uuid.New()
	task := taskFromInput(id, validTaskInput())
	tasks.EXPECT().GetByID(mock.Anything, id).Return(task, nil)
	tasks.EXPECT().IsUsedInActiveDuel(mock.Anything, id).Return(false, nil)
	tasks.EXPECT().Delete(mock.Anything, id).Return(nil)

	require.NoError(t, admin.NewTaskUsecase(tasks).DeleteTask(t.Context(), id))
}

func TestTaskUsecase_DeleteTask_ActiveDuelReturnsTaskInUse(t *testing.T) {
	t.Parallel()

	tasks := usecasemocks.NewMockTaskRepo(t)
	id := uuid.New()
	task := taskFromInput(id, validTaskInput())
	tasks.EXPECT().GetByID(mock.Anything, id).Return(task, nil)
	tasks.EXPECT().IsUsedInActiveDuel(mock.Anything, id).Return(true, nil)

	err := admin.NewTaskUsecase(tasks).DeleteTask(t.Context(), id)
	require.ErrorIs(t, err, apperr.ErrTaskInUse)
}

func TestTaskUsecase_DeleteTask_MissingReturnsTaskNotFound(t *testing.T) {
	t.Parallel()

	tasks := usecasemocks.NewMockTaskRepo(t)
	id := uuid.New()
	tasks.EXPECT().GetByID(mock.Anything, id).Return(nil, apperr.ErrTaskNotFound)

	err := admin.NewTaskUsecase(tasks).DeleteTask(t.Context(), id)
	require.ErrorIs(t, err, apperr.ErrTaskNotFound)
}

func TestTaskUsecase_DeleteTask_RepoErrorIsWrapped(t *testing.T) {
	t.Parallel()

	tasks := usecasemocks.NewMockTaskRepo(t)
	id := uuid.New()
	lowLevelErr := errors.New("db down")
	task := taskFromInput(id, validTaskInput())
	tasks.EXPECT().GetByID(mock.Anything, id).Return(task, nil)
	tasks.EXPECT().IsUsedInActiveDuel(mock.Anything, id).Return(false, nil)
	tasks.EXPECT().Delete(mock.Anything, id).Return(lowLevelErr)

	err := admin.NewTaskUsecase(tasks).DeleteTask(t.Context(), id)
	require.ErrorIs(t, err, lowLevelErr)
}

func TestTaskUsecase_DeleteTask_RepoTaskInUseIsPreserved(t *testing.T) {
	t.Parallel()

	tasks := usecasemocks.NewMockTaskRepo(t)
	id := uuid.New()
	task := taskFromInput(id, validTaskInput())
	tasks.EXPECT().GetByID(mock.Anything, id).Return(task, nil)
	tasks.EXPECT().IsUsedInActiveDuel(mock.Anything, id).Return(false, nil)
	tasks.EXPECT().Delete(mock.Anything, id).Return(apperr.ErrTaskInUse)

	err := admin.NewTaskUsecase(tasks).DeleteTask(t.Context(), id)
	require.ErrorIs(t, err, apperr.ErrTaskInUse)
}

func validTaskInput() admin.TaskInput {
	return admin.TaskInput{
		Title:       "task",
		Description: "description",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "FLAG{task}",
		Hints:       []string{"first hint", "second hint", "third hint"},
	}
}

func taskFromInput(id uuid.UUID, in admin.TaskInput) *domain.Task {
	return &domain.Task{
		ID:            id,
		Title:         in.Title,
		Description:   in.Description,
		Category:      in.Category,
		Difficulty:    in.Difficulty,
		TimeLimit:     in.TimeLimit,
		Flag:          in.Flag,
		Hints:         append([]string(nil), in.Hints...),
		TaskURL:       in.TaskURL,
		SourceFileURL: in.SourceFileURL,
		CreatedAt:     time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	}
}
