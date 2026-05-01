//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	redisrepo "github.com/TakuyaYagam1/task-per-minute/internal/repo/redis"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

func TestDuelFlagSubmit_SuccessFinishesDuelAndUpdatesHistoryAndLeaderboard(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	f := newFlagSubmitFixture(t, now)
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	task := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 90)
	bobTask := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 90)
	duel := f.makeActiveDuel(t, alice.ID, bob.ID, now.Add(time.Minute))
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, alice.ID, task.ID))
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, bob.ID, bobTask.ID))

	result, err := f.uc.SubmitFlag(ctx, duel.ID, alice.ID, task.Flag)
	require.NoError(t, err)
	require.True(t, result.Correct)
	require.Equal(t, alice.ID, result.Winner.ID)
	require.Equal(t, domain.DuelStatusFinished, result.FinishedDuel.Status)
	require.Equal(t, alice.ID, *result.FinishedDuel.WinnerID)

	gotDuel, err := f.duels.GetByID(ctx, duel.ID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, gotDuel.Status)
	require.Equal(t, alice.ID, *gotDuel.WinnerID)
	require.NotNil(t, gotDuel.FinishedAt)

	dpt, err := f.duels.GetDuelPlayerTask(ctx, duel.ID, alice.ID)
	require.NoError(t, err)
	require.True(t, dpt.Solved)
	require.NotNil(t, dpt.SolvedAt)
	require.WithinDuration(t, now, *dpt.SolvedAt, time.Second)

	solvedIDs, err := f.history.ListSolvedTaskIDs(ctx, alice.ID)
	require.NoError(t, err)
	require.True(t, hasUUID(solvedIDs, task.ID))

	gotAlice, err := f.players.GetByID(ctx, alice.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusIdle, gotAlice.Status)
	gotBob, err := f.players.GetByID(ctx, bob.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusIdle, gotBob.Status)

	scores, err := f.store.WinScores(ctx)
	require.NoError(t, err)
	require.Len(t, scores, 1)
	require.Equal(t, alice.Username, scores[0].Username)
	require.Equal(t, 1, scores[0].TasksSolved)
}

func TestDuelFlagSubmit_DeadlinePassed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	f := newFlagSubmitFixture(t, now)
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	task := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 90)
	duel := f.makeActiveDuel(t, alice.ID, bob.ID, now.Add(-time.Second))
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, alice.ID, task.ID))

	_, err := f.uc.SubmitFlag(ctx, duel.ID, alice.ID, task.Flag)
	require.ErrorIs(t, err, apperr.ErrDuelDeadlinePassed)

	gotDuel, err := f.duels.GetByID(ctx, duel.ID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusActive, gotDuel.Status)

	dpt, err := f.duels.GetDuelPlayerTask(ctx, duel.ID, alice.ID)
	require.NoError(t, err)
	require.False(t, dpt.Solved)
}

func TestDuelFlagSubmit_IncorrectFlagLeavesDuelActive(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	f := newFlagSubmitFixture(t, now)
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	task := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 90)
	duel := f.makeActiveDuel(t, alice.ID, bob.ID, now.Add(time.Minute))
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, alice.ID, task.ID))

	_, err := f.uc.SubmitFlag(ctx, duel.ID, alice.ID, "FLAG{wrong}")
	require.ErrorIs(t, err, apperr.ErrFlagIncorrect)

	gotDuel, err := f.duels.GetByID(ctx, duel.ID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusActive, gotDuel.Status)

	dpt, err := f.duels.GetDuelPlayerTask(ctx, duel.ID, alice.ID)
	require.NoError(t, err)
	require.False(t, dpt.Solved)

	scores, err := f.store.WinScores(ctx)
	require.NoError(t, err)
	require.Empty(t, scores)
}

type flagSubmitFixture struct {
	*duelFixture
	store *redisrepo.LeaderboardRedis
	uc    *duelusecase.FlagSubmitUsecase
}

func newFlagSubmitFixture(t *testing.T, now time.Time) *flagSubmitFixture {
	t.Helper()
	f := newDuelFixture()
	store := redisrepo.NewLeaderboardRedis(sharedRedis(t).client, "leaderboard:"+uniq("z"))
	uc := duelusecase.NewFlagSubmitUsecase(
		f.mgr,
		f.duels,
		f.players,
		f.history,
		store,
		fixedIntegrationClock{now: now},
	)
	return &flagSubmitFixture{duelFixture: f, store: store, uc: uc}
}
