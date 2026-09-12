//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
)

func newTaskRepo(pools ...*pgxpool.Pool) *postgres.TaskPostgres {
	pool := sharedPool
	if len(pools) > 0 && pools[0] != nil {
		pool = pools[0]
	}
	return postgres.NewTaskPostgres(postgres.NewTxManager(pool))
}

// hasTaskID is a parallel-safe replacement for asserting list length: it only
// checks whether the test's known IDs are present, ignoring rows from other
// concurrent tests.
func hasTaskID(list []*domain.Task, id uuid.UUID) bool {
	for _, t := range list {
		if t.ID == id {
			return true
		}
	}
	return false
}

func TestTaskRepo_Create_HappyPath(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo := newTaskRepo(pool)
	ctx := context.Background()
	taskURL := "https://example.com/" + uniq("task")

	got, err := repo.Create(ctx, taskusecase.UpdateInput{
		Title:       uniq("Easy SQLi"),
		Description: "find the flag",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "FLAG{" + uuid.NewString() + "}",
		Kind:        domain.TaskKindNormal,
		Enabled:     true,
		Hints:       defaultTaskHints("easy sqli"),
		TaskURL:     &taskURL,
	})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, got.ID)
	require.Equal(t, domain.DifficultyEasy, got.Difficulty)
	require.Equal(t, domain.CategoryWeb, got.Category)
	require.Equal(t, 60, got.TimeLimit)
	require.NotNil(t, got.TaskURL)
	require.Equal(t, taskURL, *got.TaskURL)
	require.Nil(t, got.SourceFileURL)
}

func TestTaskRepo_Create_AllowsHostPortTaskURL(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo := newTaskRepo(pool)
	ctx := context.Background()
	taskURL := "pwn.example.com:31337"

	got, err := repo.Create(ctx, taskusecase.UpdateInput{
		Title:       uniq("Pwn"),
		Description: "connect with nc",
		Category:    domain.CategoryPwn,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "FLAG{" + uuid.NewString() + "}",
		Kind:        domain.TaskKindNormal,
		Enabled:     true,
		Hints:       defaultTaskHints("pwn"),
		TaskURL:     &taskURL,
	})
	require.NoError(t, err)
	require.NotNil(t, got.TaskURL)
	require.Equal(t, taskURL, *got.TaskURL)
}

func TestTaskRepo_Create_RejectsInvalidEnums(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo := newTaskRepo(pool)
	ctx := context.Background()
	base := taskusecase.UpdateInput{
		Title:       uniq("X"),
		Description: "x",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "FLAG{x}",
		Kind:        domain.TaskKindNormal,
		Enabled:     true,
		Hints:       defaultTaskHints("x"),
	}
	tests := []struct {
		name  string
		patch func(*taskusecase.UpdateInput)
	}{
		{"empty_title", func(in *taskusecase.UpdateInput) { in.Title = "" }},
		{"empty_description", func(in *taskusecase.UpdateInput) { in.Description = " " }},
		{"invalid_category", func(in *taskusecase.UpdateInput) { in.Category = domain.Category("nope") }},
		{"invalid_difficulty", func(in *taskusecase.UpdateInput) { in.Difficulty = domain.Difficulty("insane") }},
		{"non-positive_time_limit", func(in *taskusecase.UpdateInput) { in.TimeLimit = 0 }},
		{"too_long_flag", func(in *taskusecase.UpdateInput) { in.Flag = strings.Repeat("x", 256) }},
		{"relative_task_url", func(in *taskusecase.UpdateInput) { raw := "/relative"; in.TaskURL = &raw }},
		{"invalid_source_file_url", func(in *taskusecase.UpdateInput) { raw := "not-a-url"; in.SourceFileURL = &raw }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := base
			tt.patch(&in)
			_, err := repo.Create(ctx, in)
			require.ErrorIs(t, err, domain.ErrTaskValidation)
		})
	}
}

func TestTaskRepo_GetByID(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo := newTaskRepo(pool)
	ctx := context.Background()
	created := mustCreateTask(t, repo, uniq("t"), domain.DifficultyEasy)

	got, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, created.Title, got.Title)
}

func TestTaskRepo_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	_, err := newTaskRepo(pool).GetByID(context.Background(), uuid.New())
	require.ErrorIs(t, err, domain.ErrTaskNotFound)
}

func TestTaskRepo_List_ContainsCreated(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo := newTaskRepo(pool)
	ctx := context.Background()

	t1 := mustCreateTask(t, repo, uniq("t"), domain.DifficultyEasy)
	t2 := mustCreateTask(t, repo, uniq("t"), domain.DifficultyMedium)
	t3 := mustCreateTask(t, repo, uniq("t"), domain.DifficultyHard)

	got, err := repo.List(ctx)
	require.NoError(t, err)
	require.True(t, hasTaskID(got, t1.ID), "list must contain t1")
	require.True(t, hasTaskID(got, t2.ID), "list must contain t2")
	require.True(t, hasTaskID(got, t3.ID), "list must contain t3")
}

func TestTaskRepo_Update(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo := newTaskRepo(pool)
	ctx := context.Background()
	created := mustCreateTask(t, repo, uniq("t"), domain.DifficultyEasy)

	src := "https://cdn.example/" + uniq("src")
	updated, err := repo.Update(ctx, created.ID, taskusecase.UpdateInput{
		Title:         created.Title + "_updated",
		Description:   "new desc",
		Category:      domain.CategoryForensics,
		Difficulty:    domain.DifficultyHard,
		TimeLimit:     120,
		Flag:          "FLAG{updated}",
		Kind:          domain.TaskKindNormal,
		Enabled:       true,
		Hints:         defaultTaskHints("updated"),
		SourceFileURL: &src,
	})
	require.NoError(t, err)
	require.Equal(t, created.ID, updated.ID)
	require.Equal(t, domain.CategoryForensics, updated.Category)
	require.Equal(t, domain.DifficultyHard, updated.Difficulty)
	require.Equal(t, 120, updated.TimeLimit)
	require.NotNil(t, updated.SourceFileURL)
	require.Equal(t, src, *updated.SourceFileURL)
	require.True(t, strings.HasSuffix(updated.Title, "_updated"))
}

func TestTaskRepo_Update_NotFound(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	_, err := newTaskRepo(pool).Update(context.Background(), uuid.New(), taskusecase.UpdateInput{
		Title: "x", Description: "x",
		Category: domain.CategoryWeb, Difficulty: domain.DifficultyEasy,
		TimeLimit: 60, Flag: "x", Kind: domain.TaskKindNormal, Enabled: true, Hints: defaultTaskHints("x"),
	})
	require.ErrorIs(t, err, domain.ErrTaskNotFound)
}

func TestTaskRepo_Update_RejectsInvalidInput(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo := newTaskRepo(pool)
	ctx := context.Background()
	created := mustCreateTask(t, repo, uniq("t"), domain.DifficultyEasy)

	_, err := repo.Update(ctx, created.ID, taskusecase.UpdateInput{
		Title:       "x",
		Description: "x",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.Difficulty("impossible"),
		TimeLimit:   60,
		Flag:        "x",
		Kind:        domain.TaskKindNormal,
		Enabled:     true,
		Hints:       defaultTaskHints("x"),
	})
	require.ErrorIs(t, err, domain.ErrTaskValidation)
}

func TestTaskRepo_Update_RejectsInvalidTaskAssetURLs(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo := newTaskRepo(pool)
	ctx := context.Background()
	created := mustCreateTask(t, repo, uniq("t"), domain.DifficultyEasy)

	taskURL := "/relative/" + uniq("task")
	_, err := repo.Update(ctx, created.ID, taskusecase.UpdateInput{
		Title:       "x",
		Description: "x",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "x",
		Kind:        domain.TaskKindNormal,
		Enabled:     true,
		Hints:       defaultTaskHints("x"),
		TaskURL:     &taskURL,
	})
	require.ErrorIs(t, err, domain.ErrTaskValidation)

	sourceURL := "ftp://files.example/" + uniq("source") + ".zip"
	_, err = repo.Update(ctx, created.ID, taskusecase.UpdateInput{
		Title:         "x",
		Description:   "x",
		Category:      domain.CategoryWeb,
		Difficulty:    domain.DifficultyEasy,
		TimeLimit:     60,
		Flag:          "x",
		Kind:          domain.TaskKindNormal,
		Enabled:       true,
		Hints:         defaultTaskHints("x"),
		SourceFileURL: &sourceURL,
	})
	require.ErrorIs(t, err, domain.ErrTaskValidation)
}

func TestTaskRepo_Delete(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	repo := newTaskRepo(pool)
	ctx := context.Background()
	created := mustCreateTask(t, repo, uniq("t"), domain.DifficultyEasy)

	require.NoError(t, repo.Delete(ctx, created.ID))
	_, err := repo.GetByID(ctx, created.ID)
	require.ErrorIs(t, err, domain.ErrTaskNotFound)
}

func TestTaskRepo_Delete_MissingReturnsTaskNotFound(t *testing.T) {
	t.Parallel()
	pool := newParallelTestDB(t)
	require.ErrorIs(t, newTaskRepo(pool).Delete(context.Background(), uuid.New()), domain.ErrTaskNotFound)
}

func TestTaskRepo_TournamentReferenceProtectsTask(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createResultAuditMigrationFixture(ctx, t)
	var taskID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT task_id
		FROM assignments
		WHERE id = $1`, fixture.assignmentID).Scan(&taskID))

	repo := newTaskRepo()
	require.ErrorIs(t, repo.Delete(ctx, taskID), domain.ErrTaskInUse)
}
