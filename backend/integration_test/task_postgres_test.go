//go:build integration

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent"
)

func newTaskRepo() *persistent.TaskPostgres {
	return persistent.NewTaskPostgres(persistent.NewTxManager(sharedPool))
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
	repo := newTaskRepo()
	ctx := context.Background()
	taskURL := "https://example.com/" + uniq("task")

	got, err := repo.Create(ctx, persistent.TaskInput{
		Title:       uniq("Easy SQLi"),
		Description: "find the flag",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "FLAG{" + uuid.NewString() + "}",
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

func TestTaskRepo_Create_RejectsInvalidEnums(t *testing.T) {
	t.Parallel()
	repo := newTaskRepo()
	ctx := context.Background()
	base := persistent.TaskInput{
		Title:       uniq("X"),
		Description: "x",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   60,
		Flag:        "FLAG{x}",
		Hints:       defaultTaskHints("x"),
	}
	tests := []struct {
		name  string
		patch func(*persistent.TaskInput)
	}{
		{"invalid_category", func(in *persistent.TaskInput) { in.Category = domain.Category("nope") }},
		{"invalid_difficulty", func(in *persistent.TaskInput) { in.Difficulty = domain.Difficulty("insane") }},
		{"non-positive_time_limit", func(in *persistent.TaskInput) { in.TimeLimit = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := base
			tt.patch(&in)
			_, err := repo.Create(ctx, in)
			require.ErrorIs(t, err, apperr.ErrTaskValidation)
		})
	}
}

func TestTaskRepo_GetByID(t *testing.T) {
	t.Parallel()
	repo := newTaskRepo()
	ctx := context.Background()
	created := mustCreateTask(t, repo, uniq("t"), domain.DifficultyEasy)

	got, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, created.Title, got.Title)
}

func TestTaskRepo_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	_, err := newTaskRepo().GetByID(context.Background(), uuid.New())
	require.ErrorIs(t, err, apperr.ErrTaskNotFound)
}

func TestTaskRepo_List_ContainsCreated(t *testing.T) {
	t.Parallel()
	repo := newTaskRepo()
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

func TestTaskRepo_ListByDifficulty_FiltersCorrectly(t *testing.T) {
	t.Parallel()
	repo := newTaskRepo()
	ctx := context.Background()

	easy := mustCreateTask(t, repo, uniq("e"), domain.DifficultyEasy)
	hard := mustCreateTask(t, repo, uniq("h"), domain.DifficultyHard)

	easyList, err := repo.ListByDifficulty(ctx, domain.DifficultyEasy)
	require.NoError(t, err)
	require.True(t, hasTaskID(easyList, easy.ID), "easy bucket must contain our easy task")
	require.False(t, hasTaskID(easyList, hard.ID), "easy bucket must NOT contain our hard task")

	hardList, err := repo.ListByDifficulty(ctx, domain.DifficultyHard)
	require.NoError(t, err)
	require.True(t, hasTaskID(hardList, hard.ID))
	require.False(t, hasTaskID(hardList, easy.ID))
}

func TestTaskRepo_Update(t *testing.T) {
	t.Parallel()
	repo := newTaskRepo()
	ctx := context.Background()
	created := mustCreateTask(t, repo, uniq("t"), domain.DifficultyEasy)

	src := "https://cdn.example/" + uniq("src")
	updated, err := repo.Update(ctx, created.ID, persistent.TaskInput{
		Title:         created.Title + "_updated",
		Description:   "new desc",
		Category:      domain.CategoryCrypto,
		Difficulty:    domain.DifficultyHard,
		TimeLimit:     120,
		Flag:          "FLAG{updated}",
		Hints:         defaultTaskHints("updated"),
		SourceFileURL: &src,
	})
	require.NoError(t, err)
	require.Equal(t, created.ID, updated.ID)
	require.Equal(t, domain.DifficultyHard, updated.Difficulty)
	require.Equal(t, 120, updated.TimeLimit)
	require.NotNil(t, updated.SourceFileURL)
	require.Equal(t, src, *updated.SourceFileURL)
	require.True(t, strings.HasSuffix(updated.Title, "_updated"))
}

func TestTaskRepo_Update_NotFound(t *testing.T) {
	t.Parallel()
	_, err := newTaskRepo().Update(context.Background(), uuid.New(), persistent.TaskInput{
		Title: "x", Description: "x",
		Category: domain.CategoryWeb, Difficulty: domain.DifficultyEasy,
		TimeLimit: 60, Flag: "x", Hints: defaultTaskHints("x"),
	})
	require.ErrorIs(t, err, apperr.ErrTaskNotFound)
}

func TestTaskRepo_Update_RejectsInvalidInput(t *testing.T) {
	t.Parallel()
	repo := newTaskRepo()
	ctx := context.Background()
	created := mustCreateTask(t, repo, uniq("t"), domain.DifficultyEasy)

	_, err := repo.Update(ctx, created.ID, persistent.TaskInput{
		Title:       "x",
		Description: "x",
		Category:    domain.CategoryWeb,
		Difficulty:  domain.Difficulty("impossible"),
		TimeLimit:   60,
		Flag:        "x",
		Hints:       defaultTaskHints("x"),
	})
	require.ErrorIs(t, err, apperr.ErrTaskValidation)
}

func TestTaskRepo_Delete(t *testing.T) {
	t.Parallel()
	repo := newTaskRepo()
	ctx := context.Background()
	created := mustCreateTask(t, repo, uniq("t"), domain.DifficultyEasy)

	require.NoError(t, repo.Delete(ctx, created.ID))
	_, err := repo.GetByID(ctx, created.ID)
	require.ErrorIs(t, err, apperr.ErrTaskNotFound)
}

func TestTaskRepo_Delete_Idempotent(t *testing.T) {
	t.Parallel()
	require.NoError(t, newTaskRepo().Delete(context.Background(), uuid.New()),
		"DELETE on missing id is a no-op for sqlc :exec")
}

func TestTaskRepo_CountByDifficulty_UsesIsolatedDB(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	t.Cleanup(cleanup)

	repo := persistent.NewTaskPostgres(persistent.NewTxManager(pool))
	ctx := context.Background()

	easyBefore, err := repo.CountByDifficulty(ctx, domain.DifficultyEasy)
	require.NoError(t, err)
	require.Equal(t, int64(0), easyBefore)

	_ = mustCreateTask(t, repo, uniq("e"), domain.DifficultyEasy)
	_ = mustCreateTask(t, repo, uniq("e"), domain.DifficultyEasy)
	_ = mustCreateTask(t, repo, uniq("h"), domain.DifficultyHard)

	easy, err := repo.CountByDifficulty(ctx, domain.DifficultyEasy)
	require.NoError(t, err)
	require.Equal(t, int64(2), easy)

	hard, err := repo.CountByDifficulty(ctx, domain.DifficultyHard)
	require.NoError(t, err)
	require.Equal(t, int64(1), hard)

	medium, err := repo.CountByDifficulty(ctx, domain.DifficultyMedium)
	require.NoError(t, err)
	require.Equal(t, int64(0), medium)

	_, err = repo.CountByDifficulty(ctx, domain.Difficulty("impossible"))
	require.ErrorIs(t, err, apperr.ErrValidation)

	TruncateTables(t, pool)
	easyAfterTruncate, err := repo.CountByDifficulty(ctx, domain.DifficultyEasy)
	require.NoError(t, err)
	require.Equal(t, int64(0), easyAfterTruncate)
}

func TestTaskRepo_CountByDifficulty_ExcludesTasksWithoutHints(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	t.Cleanup(cleanup)

	repo := persistent.NewTaskPostgres(persistent.NewTxManager(pool))
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag)
		VALUES ('legacy', 'missing hints', 'web', 'easy', 60, 'FLAG{legacy}')`)
	require.NoError(t, err)
	_ = mustCreateTask(t, repo, uniq("ready"), domain.DifficultyEasy)

	got, err := repo.CountByDifficulty(ctx, domain.DifficultyEasy)
	require.NoError(t, err)
	require.Equal(t, int64(1), got)
}

func TestTaskRepo_IsUsedInActiveDuel(t *testing.T) {
	t.Parallel()
	repo := newTaskRepo()
	f := newDuelFixture()
	ctx := context.Background()

	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	used := mustCreateTask(t, repo, uniq("used"), domain.DifficultyEasy)
	unused := mustCreateTask(t, repo, uniq("unused"), domain.DifficultyEasy)
	d, err := f.duels.Create(ctx, p1.ID, p2.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, d.ID, p1.ID, used.ID))

	isUsed, err := repo.IsUsedInActiveDuel(ctx, used.ID)
	require.NoError(t, err)
	require.True(t, isUsed)

	isUsed, err = repo.IsUsedInActiveDuel(ctx, unused.ID)
	require.NoError(t, err)
	require.False(t, isUsed)

	_, err = f.duels.Finish(ctx, d.ID, nil, time.Now().UTC(), domain.DuelStatusFinished)
	require.NoError(t, err)

	isUsed, err = repo.IsUsedInActiveDuel(ctx, used.ID)
	require.NoError(t, err)
	require.False(t, isUsed, "finished duel must not count as active usage")
}

func TestTaskRepo_Delete_ReferencedTaskReturnsFKError(t *testing.T) {
	t.Parallel()
	repo := newTaskRepo()
	f := newDuelFixture()
	ctx := context.Background()

	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	task := mustCreateTask(t, repo, uniq("used"), domain.DifficultyEasy)
	d, err := f.duels.Create(ctx, p1.ID, p2.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, d.ID, p1.ID, task.ID))

	require.Error(t, repo.Delete(ctx, task.ID))
}

func TestTaskRepo_CountSolvedByDifficulty(t *testing.T) {
	t.Parallel()
	repo := newTaskRepo()
	ctx := context.Background()

	playerRepo := persistent.NewPlayerPostgres(persistent.NewTxManager(sharedPool))
	historyRepo := persistent.NewHistoryPostgres(persistent.NewTxManager(sharedPool))

	alice, err := playerRepo.Create(ctx, uniq("alice"))
	require.NoError(t, err)
	bob, err := playerRepo.Create(ctx, uniq("bob"))
	require.NoError(t, err)

	easy1 := mustCreateTask(t, repo, uniq("e"), domain.DifficultyEasy)
	easy2 := mustCreateTask(t, repo, uniq("e"), domain.DifficultyEasy)
	hard := mustCreateTask(t, repo, uniq("h"), domain.DifficultyHard)

	require.NoError(t, historyRepo.AddSolved(ctx, alice.ID, easy1.ID, time.Now().UTC()))
	require.NoError(t, historyRepo.AddSolved(ctx, alice.ID, easy2.ID, time.Now().UTC()))
	require.NoError(t, historyRepo.AddSolved(ctx, alice.ID, hard.ID, time.Now().UTC()))
	require.NoError(t, historyRepo.AddSolved(ctx, bob.ID, easy1.ID, time.Now().UTC()))

	aliceEasy, err := repo.CountSolvedByDifficulty(ctx, alice.ID, domain.DifficultyEasy)
	require.NoError(t, err)
	require.Equal(t, int64(2), aliceEasy, "scoped to alice → 2 easy")

	aliceHard, err := repo.CountSolvedByDifficulty(ctx, alice.ID, domain.DifficultyHard)
	require.NoError(t, err)
	require.Equal(t, int64(1), aliceHard)

	bobEasy, err := repo.CountSolvedByDifficulty(ctx, bob.ID, domain.DifficultyEasy)
	require.NoError(t, err)
	require.Equal(t, int64(1), bobEasy, "scoped to bob → 1 easy")

	aliceMedium, err := repo.CountSolvedByDifficulty(ctx, alice.ID, domain.DifficultyMedium)
	require.NoError(t, err)
	require.Equal(t, int64(0), aliceMedium)
}
