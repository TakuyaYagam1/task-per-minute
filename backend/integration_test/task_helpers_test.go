//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

func (f *duelFixture) makeTask(t testing.TB, title string, diff domain.Difficulty) *domain.Task {
	t.Helper()
	return f.makeTaskWithLimit(t, title, diff, 60)
}

func (f *duelFixture) makeTaskWithLimit(
	t testing.TB,
	title string,
	diff domain.Difficulty,
	limit int,
) *domain.Task {
	t.Helper()
	return f.makeTaskWithInput(t, persistent.TaskInput{
		Title:       title,
		Description: "x",
		Category:    domain.CategoryWeb,
		Difficulty:  diff,
		TimeLimit:   limit,
		Flag:        "FLAG{" + title + "}",
		Hints:       defaultTaskHints(title),
	})
}

func (f *duelFixture) makeForensicsTask(t testing.TB, title string, limit int) *domain.Task {
	t.Helper()
	return f.makeTaskWithInput(t, persistent.TaskInput{
		Title:       title,
		Description: "download the archive",
		Category:    domain.CategoryForensics,
		Difficulty:  domain.DifficultyEasy,
		TimeLimit:   limit,
		Flag:        "FLAG{" + title + "}",
		Hints:       defaultTaskHints(title),
	})
}

func (f *duelFixture) makeTaskWithInput(t testing.TB, input persistent.TaskInput) *domain.Task {
	t.Helper()
	task, err := f.tasks.Create(context.Background(), input)
	require.NoError(t, err)
	return task
}

func mustCreateTask(
	t testing.TB,
	repo *persistent.TaskPostgres,
	title string,
	diff domain.Difficulty,
) *domain.Task {
	t.Helper()
	task, err := repo.Create(context.Background(), persistent.TaskInput{
		Title:       title,
		Description: "x",
		Category:    domain.CategoryWeb,
		Difficulty:  diff,
		TimeLimit:   60,
		Flag:        "FLAG{" + title + "}",
		Hints:       defaultTaskHints(title),
	})
	require.NoError(t, err)
	return task
}

func defaultTaskHints(seed string) []string {
	return []string{
		seed + " hint 1",
		seed + " hint 2",
		seed + " hint 3",
	}
}

func defaultOpenAPIHints(seed string) []*string {
	return nullableOpenAPIHints(defaultTaskHints(seed))
}

func nullableOpenAPIHints(hints []string) []*string {
	out := make([]*string, len(hints))
	for i, hint := range hints {
		value := hint
		out[i] = &value
	}
	return out
}

func taskForPlayer(t testing.TB, result *duelusecase.MatchResult, playerID uuid.UUID) *domain.Task {
	t.Helper()

	switch playerID {
	case result.Duel.Player1ID:
		return result.Player1Task
	case result.Duel.Player2ID:
		return result.Player2Task
	default:
		t.Fatalf("player %s is not part of duel %s", playerID, result.Duel.ID)
		return nil
	}
}
