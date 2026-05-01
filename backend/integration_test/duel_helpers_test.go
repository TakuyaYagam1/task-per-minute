//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

func (f *duelFixture) makeActiveDuel(
	t testing.TB,
	player1ID,
	player2ID uuid.UUID,
	deadline time.Time,
) *domain.Duel {
	t.Helper()
	ctx := context.Background()
	duel, err := f.duels.Create(ctx, player1ID, player2ID, deadline)
	require.NoError(t, err)
	f.setPlayersInDuel(t, player1ID, player2ID)
	return duel
}

func (f *duelFixture) setPlayersInDuel(t testing.TB, playerIDs ...uuid.UUID) {
	t.Helper()
	for _, playerID := range playerIDs {
		_, err := f.players.UpdateStatus(context.Background(), playerID, domain.PlayerStatusInDuel)
		require.NoError(t, err)
	}
}

func (f *duelFixture) markSolvedAt(
	t testing.TB,
	playerID uuid.UUID,
	start time.Time,
	tasks ...*domain.Task,
) {
	t.Helper()
	for i, task := range tasks {
		require.NoError(t, f.history.AddSolved(
			context.Background(),
			playerID,
			task.ID,
			start.Add(time.Duration(i)*time.Second),
		))
	}
}

func (f *duelFixture) createSolvedWin(
	t testing.TB,
	player1ID,
	player2ID,
	winnerID,
	taskID uuid.UUID,
	startedAt time.Time,
	solveTime time.Duration,
) {
	t.Helper()
	createSolvedWinOnPool(t, f.pool, player1ID, player2ID, winnerID, taskID, startedAt, solveTime)
}

func createSolvedWin(
	t testing.TB,
	player1ID,
	player2ID,
	winnerID,
	taskID uuid.UUID,
	startedAt time.Time,
	solveTime time.Duration,
) {
	t.Helper()
	createSolvedWinOnPool(t, sharedPool, player1ID, player2ID, winnerID, taskID, startedAt, solveTime)
}

func createSolvedWinOnPool(
	t testing.TB,
	pool *pgxpool.Pool,
	player1ID,
	player2ID,
	winnerID,
	taskID uuid.UUID,
	startedAt time.Time,
	solveTime time.Duration,
) {
	t.Helper()
	finishedAt := startedAt.Add(solveTime)
	var duelID uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO duels (player1_id, player2_id, status, winner_id, deadline, started_at, finished_at)
		VALUES ($1, $2, 'finished', $3, $5, $4, $5)
		RETURNING id`,
		player1ID, player2ID, winnerID, startedAt, finishedAt).Scan(&duelID)
	require.NoError(t, err)

	_, err = pool.Exec(context.Background(), `
		INSERT INTO duel_player_tasks (duel_id, player_id, task_id, solved, solved_at)
		VALUES ($1, $2, $3, TRUE, $4)`,
		duelID, winnerID, taskID, finishedAt)
	require.NoError(t, err)
}

func joinPlayersConcurrently(
	t testing.TB,
	uc *duelusecase.MatchmakingUsecase,
	playerIDs ...uuid.UUID,
) []*duelusecase.MatchResult {
	t.Helper()

	results := make([]*duelusecase.MatchResult, len(playerIDs))
	errs := make([]error, len(playerIDs))
	var wg sync.WaitGroup
	wg.Add(len(playerIDs))
	for i, playerID := range playerIDs {
		go func(i int, playerID uuid.UUID) {
			defer wg.Done()
			results[i], errs[i] = uc.JoinQueue(context.Background(), playerID)
		}(i, playerID)
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	return results
}

func nonNilMatchResults(results []*duelusecase.MatchResult) []*duelusecase.MatchResult {
	out := make([]*duelusecase.MatchResult, 0, len(results))
	for _, result := range results {
		if result != nil {
			out = append(out, result)
		}
	}
	return out
}

type fixedIntegrationClock struct {
	now time.Time
}

func (c fixedIntegrationClock) Now() time.Time {
	return c.now
}
