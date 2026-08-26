package admin_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestTaskUseCase_CreateTask(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	in := validTaskInput()
	taskURL := "pwn.example.com:31337"
	in.TaskURL = &taskURL
	created := taskFromInput(uuid.New(), in)
	tasks.On("Create", mock.Anything, in).Return(created, nil)

	got, err := admin.NewTaskUseCase(tasks).CreateTask(t.Context(), in)
	require.NoError(t, err)
	require.Same(t, created, got)
}

func TestTaskUseCase_CreateTask_Validation(t *testing.T) {
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
			_, err := admin.NewTaskUseCase(newTaskRepositoryMock(t)).CreateTask(t.Context(), in)
			require.ErrorIs(t, err, domain.ErrTaskValidation)
		})
	}
}

func TestTaskUseCase_CreateTask_NormalizesPositionalHints(t *testing.T) {
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

			tasks := newTaskRepositoryMock(t)
			in := validTaskInput()
			in.Hints = tt.hints
			normalized := in
			normalized.Hints = tt.want
			created := taskFromInput(uuid.New(), normalized)
			tasks.On("Create", mock.Anything, normalized).Return(created, nil)

			got, err := admin.NewTaskUseCase(tasks).CreateTask(t.Context(), in)
			require.NoError(t, err)
			require.Same(t, created, got)
		})
	}
}

func TestTaskUseCase_GetListUpdate(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	uc := admin.NewTaskUseCase(tasks)
	id := uuid.New()
	in := validTaskInput()
	task := taskFromInput(id, in)

	tasks.On("GetByID", mock.Anything, id).Return(task, nil)
	got, err := uc.GetTask(t.Context(), id)
	require.NoError(t, err)
	require.Same(t, task, got)

	tasks.On("List", mock.Anything).Return([]*domain.Task{task}, nil)
	list, err := uc.ListTasks(t.Context())
	require.NoError(t, err)
	require.Equal(t, []*domain.Task{task}, list)

	updatedInput := validTaskInput()
	updatedInput.Title = "updated"
	updated := taskFromInput(id, updatedInput)
	tasks.On("Update", mock.Anything, id, updatedInput).Return(updated, nil)
	got, err = uc.UpdateTask(t.Context(), id, updatedInput)
	require.NoError(t, err)
	require.Same(t, updated, got)
}

func TestTaskUseCase_UpdateTask_Validation(t *testing.T) {
	t.Parallel()

	in := validTaskInput()
	in.Difficulty = domain.Difficulty("bad")

	_, err := admin.NewTaskUseCase(newTaskRepositoryMock(t)).UpdateTask(t.Context(), uuid.New(), in)
	require.ErrorIs(t, err, domain.ErrTaskValidation)
}

func TestTaskUseCase_DeleteTask_UnusedDeletes(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	id := uuid.New()
	task := taskFromInput(id, validTaskInput())
	tasks.On("GetByID", mock.Anything, id).Return(task, nil)
	tasks.On("IsUsedInActiveDuel", mock.Anything, id).Return(false, nil)
	tasks.On("Delete", mock.Anything, id).Return(nil)

	require.NoError(t, admin.NewTaskUseCase(tasks).DeleteTask(t.Context(), id))
}

func TestTaskUseCase_DeleteTask_ActiveDuelReturnsTaskInUse(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	id := uuid.New()
	task := taskFromInput(id, validTaskInput())
	tasks.On("GetByID", mock.Anything, id).Return(task, nil)
	tasks.On("IsUsedInActiveDuel", mock.Anything, id).Return(true, nil)

	err := admin.NewTaskUseCase(tasks).DeleteTask(t.Context(), id)
	require.ErrorIs(t, err, domain.ErrTaskInUse)
}

func TestTaskUseCase_DeleteTask_MissingReturnsTaskNotFound(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	id := uuid.New()
	tasks.On("GetByID", mock.Anything, id).Return(nil, domain.ErrTaskNotFound)

	err := admin.NewTaskUseCase(tasks).DeleteTask(t.Context(), id)
	require.ErrorIs(t, err, domain.ErrTaskNotFound)
}

func TestTaskUseCase_DeleteTask_RepoErrorIsWrapped(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	id := uuid.New()
	lowLevelErr := errors.New("db down")
	task := taskFromInput(id, validTaskInput())
	tasks.On("GetByID", mock.Anything, id).Return(task, nil)
	tasks.On("IsUsedInActiveDuel", mock.Anything, id).Return(false, nil)
	tasks.On("Delete", mock.Anything, id).Return(lowLevelErr)

	err := admin.NewTaskUseCase(tasks).DeleteTask(t.Context(), id)
	require.ErrorIs(t, err, lowLevelErr)
}

func TestTaskUseCase_DeleteTask_RepoTaskInUseIsPreserved(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	id := uuid.New()
	task := taskFromInput(id, validTaskInput())
	tasks.On("GetByID", mock.Anything, id).Return(task, nil)
	tasks.On("IsUsedInActiveDuel", mock.Anything, id).Return(false, nil)
	tasks.On("Delete", mock.Anything, id).Return(domain.ErrTaskInUse)

	err := admin.NewTaskUseCase(tasks).DeleteTask(t.Context(), id)
	require.ErrorIs(t, err, domain.ErrTaskInUse)
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
