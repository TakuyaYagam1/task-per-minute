//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func hasUUID(list []uuid.UUID, id uuid.UUID) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}

func TestHistoryRepo_AddSolved_ListRoundTrip(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	p := f.makePlayer(t, uniq("alice"))
	t1 := f.makeTask(t, uniq("t"), domain.DifficultyEasy)
	t2 := f.makeTask(t, uniq("t"), domain.DifficultyMedium)

	require.NoError(t, f.history.AddSolved(ctx, p.ID, t1.ID, time.Now().UTC()))
	require.NoError(t, f.history.AddSolved(ctx, p.ID, t2.ID, time.Now().UTC()))

	ids, err := f.history.ListSolvedTaskIDs(ctx, p.ID)
	require.NoError(t, err)
	require.Len(t, ids, 2, "scoped to fresh player → exactly 2")
	require.True(t, hasUUID(ids, t1.ID))
	require.True(t, hasUUID(ids, t2.ID))
}

func TestHistoryRepo_ListSolvedTaskIDs_Empty(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	p := f.makePlayer(t, uniq("alice"))

	ids, err := f.history.ListSolvedTaskIDs(context.Background(), p.ID)
	require.NoError(t, err)
	require.Empty(t, ids, "fresh player has no history")
}

// SelectUnsolvedTaskByDifficulty must NOT return tasks already in the player's
// history. With other concurrent tests inserting easy tasks, we cannot assert
// "only easy3 is returned" — but we can assert "the returned id is none of the
// solved ids", which is the actual semantic of the query.
func TestHistoryRepo_SelectUnsolvedTaskByDifficulty_SkipsHistory(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	p := f.makePlayer(t, uniq("alice"))
	easy1 := f.makeTask(t, uniq("e"), domain.DifficultyEasy)
	easy2 := f.makeTask(t, uniq("e"), domain.DifficultyEasy)
	require.NoError(t, f.history.AddSolved(ctx, p.ID, easy1.ID, time.Now().UTC()))
	require.NoError(t, f.history.AddSolved(ctx, p.ID, easy2.ID, time.Now().UTC()))

	for i := 0; i < 5; i++ {
		got, err := f.history.SelectUnsolvedTaskByDifficulty(ctx, p.ID, domain.DifficultyEasy)
		require.NoError(t, err, "iter %d", i)
		require.NotEqual(t, easy1.ID, got.ID, "iter %d returned solved easy1", i)
		require.NotEqual(t, easy2.ID, got.ID, "iter %d returned solved easy2", i)
		require.Equal(t, domain.DifficultyEasy, got.Difficulty, "iter %d wrong difficulty", i)
	}
}

func TestHistoryRepo_SelectAnyTaskByDifficulty_IgnoresHistory(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	p := f.makePlayer(t, uniq("alice"))
	easy := f.makeTask(t, uniq("e"), domain.DifficultyEasy)
	require.NoError(t, f.history.AddSolved(ctx, p.ID, easy.ID, time.Now().UTC()))

	got, err := f.history.SelectAnyTaskByDifficulty(ctx, domain.DifficultyEasy)
	require.NoError(t, err, "fallback always returns a task when the bucket is non-empty")
	require.Equal(t, domain.DifficultyEasy, got.Difficulty,
		"the result must come from the requested bucket — id may belong to any concurrent test")
}

func TestHistoryRepo_SelectTaskByDifficulty_SkipsTasksWithoutHints(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	t.Cleanup(cleanup)
	f := newDuelFixtureWithPool(pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO tasks (title, description, category, difficulty, time_limit, flag)
		VALUES ('legacy', 'missing hints', 'web', 'easy', 60, 'FLAG{legacy}')`)
	require.NoError(t, err)
	valid := f.makeTask(t, uniq("valid"), domain.DifficultyEasy)

	got, err := f.history.SelectAnyTaskByDifficulty(ctx, domain.DifficultyEasy)
	require.NoError(t, err)
	require.Equal(t, valid.ID, got.ID)
}

func TestHistoryRepo_RejectsInvalidDifficulty(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	p := f.makePlayer(t, uniq("alice"))
	ctx := context.Background()

	_, err := f.history.SelectUnsolvedTaskByDifficulty(ctx, p.ID, domain.Difficulty("nightmare"))
	require.ErrorIs(t, err, apperr.ErrValidation)

	_, err = f.history.SelectAnyTaskByDifficulty(ctx, domain.Difficulty("nightmare"))
	require.ErrorIs(t, err, apperr.ErrValidation)
}
