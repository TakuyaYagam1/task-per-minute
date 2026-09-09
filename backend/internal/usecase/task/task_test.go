package task_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestTaskUseCase_CreateTask(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	in := validTaskCreateInput()
	taskURL := "pwn.example.com:31337"
	in.TaskURL = &taskURL
	want := validTaskInput()
	want.TaskURL = &taskURL
	created := taskFromInput(uuid.New(), want)
	tasks.On("Create", mock.Anything, want).Return(created, nil)

	got, err := taskusecase.NewUseCase(tasks).CreateTask(t.Context(), in)
	require.NoError(t, err)
	require.Same(t, created, got)
}

func TestTaskUseCase_CreateTask_DefaultsUnspecifiedKind(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	in := validTaskCreateInput()
	in.Kind = ""
	want := validTaskInput()
	want.Kind = domain.TaskKindNormal
	created := taskFromInput(uuid.New(), want)
	tasks.On("Create", mock.Anything, want).Return(created, nil)

	got, err := taskusecase.NewUseCase(tasks).CreateTask(t.Context(), in)
	require.NoError(t, err)
	require.Same(t, created, got)
}

func TestTaskUseCase_CreateTask_DefaultsOmittedEnabledToTrue(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	in := validTaskCreateInput()
	in.Enabled = nil
	want := validTaskInput()
	created := taskFromInput(uuid.New(), want)
	tasks.On("Create", mock.Anything, want).Return(created, nil)

	got, err := taskusecase.NewUseCase(tasks).CreateTask(t.Context(), in)
	require.NoError(t, err)
	require.Same(t, created, got)
}

func TestTaskUseCase_CreateTask_PreservesExplicitDisabled(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	disabled := false
	in := validTaskCreateInput()
	in.Enabled = &disabled
	want := validTaskInput()
	want.Enabled = false
	created := taskFromInput(uuid.New(), want)
	tasks.On("Create", mock.Anything, want).Return(created, nil)

	got, err := taskusecase.NewUseCase(tasks).CreateTask(t.Context(), in)
	require.NoError(t, err)
	require.Same(t, created, got)
}

func TestTaskUseCase_CreateTask_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*taskusecase.CreateInput)
	}{
		{"empty title", func(in *taskusecase.CreateInput) { in.Title = "" }},
		{"too long title", func(in *taskusecase.CreateInput) { in.Title = strings.Repeat("a", 256) }},
		{"empty description", func(in *taskusecase.CreateInput) { in.Description = " " }},
		{"invalid category", func(in *taskusecase.CreateInput) { in.Category = domain.Category("network") }},
		{"invalid difficulty", func(in *taskusecase.CreateInput) { in.Difficulty = domain.Difficulty("insane") }},
		{"zero time limit", func(in *taskusecase.CreateInput) { in.TimeLimit = 0 }},
		{"empty flag", func(in *taskusecase.CreateInput) { in.Flag = "" }},
		{"invalid kind", func(in *taskusecase.CreateInput) { in.Kind = domain.TaskKind("archive") }},
		{"too long flag", func(in *taskusecase.CreateInput) { in.Flag = strings.Repeat("ф", 256) }},
		{"relative task url", func(in *taskusecase.CreateInput) { url := "/tasks/1"; in.TaskURL = &url }},
		{"unsupported task url scheme", func(in *taskusecase.CreateInput) { url := "ftp://example.com/task"; in.TaskURL = &url }},
		{"invalid source file url", func(in *taskusecase.CreateInput) { url := "not-a-url"; in.SourceFileURL = &url }},
		{"unsupported source file url scheme", func(in *taskusecase.CreateInput) { url := "ftp://example.com/source.zip"; in.SourceFileURL = &url }},
		{"too many hints", func(in *taskusecase.CreateInput) { in.Hints = []string{"one", "two", "three", "four"} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			in := validTaskCreateInput()
			tt.mutate(&in)
			_, err := taskusecase.NewUseCase(newTaskRepositoryMock(t)).CreateTask(t.Context(), in)
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
			in := validTaskCreateInput()
			in.Hints = tt.hints
			normalized := validTaskInput()
			normalized.Hints = tt.want
			created := taskFromInput(uuid.New(), normalized)
			tasks.On("Create", mock.Anything, normalized).Return(created, nil)

			got, err := taskusecase.NewUseCase(tasks).CreateTask(t.Context(), in)
			require.NoError(t, err)
			require.Same(t, created, got)
		})
	}
}

func TestTaskUseCase_UpdateTask_PreservesExplicitEnabled(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	id := uuid.New()
	in := validTaskInput()
	in.Enabled = false
	updated := taskFromInput(id, in)
	tasks.On("Update", mock.Anything, id, in).Return(updated, nil)

	got, err := taskusecase.NewUseCase(tasks).UpdateTask(t.Context(), id, in)
	require.NoError(t, err)
	require.Same(t, updated, got)
}

func TestTaskUseCase_GetListUpdate(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	uc := taskusecase.NewUseCase(tasks)
	id := uuid.New()
	in := validTaskInput()
	storedTask := taskFromInput(id, in)

	tasks.On("GetByID", mock.Anything, id).Return(storedTask, nil)
	got, err := uc.GetTask(t.Context(), id)
	require.NoError(t, err)
	require.Same(t, storedTask, got)

	tasks.On("List", mock.Anything).Return([]*domain.Task{storedTask}, nil)
	list, err := uc.ListTasks(t.Context())
	require.NoError(t, err)
	require.Equal(t, []*domain.Task{storedTask}, list)

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

	_, err := taskusecase.NewUseCase(newTaskRepositoryMock(t)).UpdateTask(t.Context(), uuid.New(), in)
	require.ErrorIs(t, err, domain.ErrTaskValidation)
}

func TestTaskUseCase_UpdateTask_RejectsOmittedKind(t *testing.T) {
	t.Parallel()

	in := validTaskInput()
	in.Kind = ""

	_, err := taskusecase.NewUseCase(newTaskRepositoryMock(t)).UpdateTask(t.Context(), uuid.New(), in)
	require.ErrorIs(t, err, domain.ErrTaskValidation)
}

func TestTaskUseCase_DeleteTask_UnusedDeletes(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	id := uuid.New()
	tasks.On("Delete", mock.Anything, id).Return(nil)

	require.NoError(t, taskusecase.NewUseCase(tasks).DeleteTask(t.Context(), id))
}

func TestTaskUseCase_DeleteTask_TournamentReferenceReturnsTaskInUse(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	id := uuid.New()
	tasks.On("Delete", mock.Anything, id).Return(domain.ErrTaskInUse)

	err := taskusecase.NewUseCase(tasks).DeleteTask(t.Context(), id)
	require.ErrorIs(t, err, domain.ErrTaskInUse)
}

func TestTaskUseCase_DeleteTask_MissingReturnsTaskNotFound(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	id := uuid.New()
	tasks.On("Delete", mock.Anything, id).Return(domain.ErrTaskNotFound)

	err := taskusecase.NewUseCase(tasks).DeleteTask(t.Context(), id)
	require.ErrorIs(t, err, domain.ErrTaskNotFound)
}

func TestTaskUseCase_DeleteTask_RepoErrorIsWrapped(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	id := uuid.New()
	lowLevelErr := errors.New("db down")
	tasks.On("Delete", mock.Anything, id).Return(lowLevelErr)

	err := taskusecase.NewUseCase(tasks).DeleteTask(t.Context(), id)
	require.ErrorIs(t, err, lowLevelErr)
}

func TestTaskUseCase_DeleteTask_RepoTaskInUseIsPreserved(t *testing.T) {
	t.Parallel()

	tasks := newTaskRepositoryMock(t)
	id := uuid.New()
	tasks.On("Delete", mock.Anything, id).Return(domain.ErrTaskInUse)

	err := taskusecase.NewUseCase(tasks).DeleteTask(t.Context(), id)
	require.ErrorIs(t, err, domain.ErrTaskInUse)
}

func validTaskInput() taskusecase.UpdateInput {
	return taskusecase.UpdateInput{
		Title:       "task",
		Description: "description",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "FLAG{task}",
		Kind:        domain.TaskKindNormal,
		Enabled:     true,
		Hints:       []string{"first hint", "second hint", "third hint"},
	}
}

func validTaskCreateInput() taskusecase.CreateInput {
	enabled := true
	return taskusecase.CreateInput{
		Title:       "task",
		Description: "description",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "FLAG{task}",
		Kind:        domain.TaskKindNormal,
		Enabled:     &enabled,
		Hints:       []string{"first hint", "second hint", "third hint"},
	}
}

func taskFromInput(id uuid.UUID, in taskusecase.UpdateInput) *domain.Task {
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
		Kind:          in.Kind,
		Enabled:       in.Enabled,
		CreatedAt:     time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
	}
}
