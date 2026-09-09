//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	taskusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/task"
)

func (f *databaseFixture) makeTask(tb testing.TB, title string, diff domain.Difficulty) *domain.Task {
	tb.Helper()
	task, err := f.tasks.Create(context.Background(), taskusecase.UpdateInput{
		Title:       title,
		Description: "x",
		Category:    domain.CategoryWeb,
		Difficulty:  diff,
		TimeLimit:   60,
		Flag:        "FLAG{" + title + "}",
		Kind:        domain.TaskKindNormal,
		Enabled:     true,
		Hints:       defaultTaskHints(title),
	})
	require.NoError(tb, err)
	return task
}

func mustCreateTask(
	tb testing.TB,
	repo *postgres.TaskPostgres,
	title string,
	diff domain.Difficulty,
) *domain.Task {
	tb.Helper()
	task, err := repo.Create(context.Background(), taskusecase.UpdateInput{
		Title:       title,
		Description: "x",
		Category:    domain.CategoryWeb,
		Difficulty:  diff,
		TimeLimit:   60,
		Flag:        "FLAG{" + title + "}",
		Kind:        domain.TaskKindNormal,
		Enabled:     true,
		Hints:       defaultTaskHints(title),
	})
	require.NoError(tb, err)
	return task
}

func defaultTaskHints(seed string) []string {
	return []string{
		seed + " hint 1",
		seed + " hint 2",
		seed + " hint 3",
	}
}

func nullableOpenAPIHints(hints []string) []*string {
	out := make([]*string, len(hints))
	for i, hint := range hints {
		value := hint
		out[i] = &value
	}
	return out
}
