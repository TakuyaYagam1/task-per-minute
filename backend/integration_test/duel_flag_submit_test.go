//go:build integration

package integration_test

import (
	"context"
	"sync"
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
	require.Equal(t, 1, scores[0].Wins)
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

func TestDuelFlagSubmit_ConcurrentCorrectFlags_OneWinnerSingleLeaderboardBump(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	f := newFlagSubmitFixture(t, now)
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	aliceTask := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 90)
	bobTask := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 90)
	duel := f.makeActiveDuel(t, alice.ID, bob.ID, now.Add(time.Minute))
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, alice.ID, aliceTask.ID))
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, bob.ID, bobTask.ID))

	type submitOutcome struct {
		result duelusecase.Result
		err    error
	}
	results := make([]submitOutcome, 2)

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		r, err := f.uc.SubmitFlag(ctx, duel.ID, alice.ID, aliceTask.Flag)
		results[0] = submitOutcome{result: r, err: err}
	}()
	go func() {
		defer wg.Done()
		<-start
		r, err := f.uc.SubmitFlag(ctx, duel.ID, bob.ID, bobTask.Flag)
		results[1] = submitOutcome{result: r, err: err}
	}()
	close(start)
	wg.Wait()

	for _, out := range results {
		require.NoError(t, out.err, "concurrent loser must NOT receive an error")
	}

	correctCount := 0
	alreadyFinishedCount := 0
	var winnerID string
	for _, out := range results {
		if out.result.Correct {
			correctCount++
			require.NotNil(t, out.result.Winner)
			require.NotNil(t, out.result.FinishedDuel)
			require.Equal(t, domain.DuelStatusFinished, out.result.FinishedDuel.Status)
			winnerID = out.result.Winner.ID.String()
		} else {
			if out.result.AlreadyFinished {
				alreadyFinishedCount++
			}
			require.Nil(t, out.result.Winner, "loser must not carry a Winner pointer")
			require.Nil(t, out.result.FinishedDuel, "loser must not carry a finished duel pointer")
		}
	}
	require.Equal(t, 1, correctCount, "exactly one player must win")
	require.Equal(t, 1, alreadyFinishedCount, "concurrent loser should be reported as already finished")

	gotDuel, err := f.duels.GetByID(ctx, duel.ID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, gotDuel.Status)
	require.NotNil(t, gotDuel.WinnerID)
	require.Equal(t, winnerID, gotDuel.WinnerID.String())
	require.NotNil(t, gotDuel.FinishedAt)

	scores, err := f.store.WinScores(ctx)
	require.NoError(t, err)
	require.Len(t, scores, 1, "exactly one leaderboard bump is expected")
	require.Equal(t, 1, scores[0].Wins, "winner must have score=1")
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
