//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestArenaExecutionIsolation(t *testing.T) {
	ctx := context.Background()
	pool, _ := SetupTestDB(t)
	duelState := newDuelFixtureWithPool(pool)
	now := time.Date(2026, time.September, 2, 8, 0, 0, 0, time.UTC)

	first := duelState.makePlayer(t, uniq("isolation_first"))
	second := duelState.makePlayer(t, uniq("isolation_second"))
	queued := duelState.makePlayer(t, uniq("isolation_queued"))
	arenaPlayers := make([]uuid.UUID, domain.ArenaMinParticipants)
	for index := range arenaPlayers {
		arenaPlayers[index] = duelState.makePlayer(t, uniq("isolation_arena")).ID
	}
	task := duelState.makeTask(t, uniq("isolation_task"), domain.DifficultyEasy)
	duel, err := duelState.duels.Create(ctx, first.ID, second.ID, now.Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, duelState.duels.CreateDuelPlayerTask(ctx, duel.ID, first.ID, task.ID))
	require.NoError(t, duelState.duels.CreateDuelPlayerTask(ctx, duel.ID, second.ID, task.ID))
	require.NoError(t, duelState.duels.MarkSolved(ctx, duel.ID, first.ID, now.Add(10*time.Second)))
	require.NoError(t, duelState.history.AddSolved(ctx, first.ID, task.ID, now.Add(10*time.Second)))
	_, err = duelState.duels.Finish(
		ctx,
		duel.ID,
		&first.ID,
		now.Add(10*time.Second),
		domain.DuelStatusFinished,
	)
	require.NoError(t, err)

	redisFixture := sharedRedis(t)
	queueKey := "matchmaking:isolation:" + uniq("queue")
	boardKey := "leaderboard:isolation:" + uniq("board")
	queue := redisadapter.NewMatchmakingRedis(redisFixture.client, queueKey)
	board := redisadapter.NewLeaderboardRedis(redisFixture.client, boardKey)
	t.Cleanup(func() {
		require.NoError(t, redisFixture.client.Del(context.Background(), queueKey, boardKey).Err())
	})
	require.NoError(t, queue.Enqueue(ctx, queued.ID))
	queuedReservation, changed, err := duelState.players.AcquireParticipantReservation(
		ctx,
		queued.ID,
		domain.ParticipantReservationOwnerCasualQueue,
		queued.ID,
		now,
	)
	require.NoError(t, err)
	require.True(t, changed)
	_, changed, err = duelState.players.UpdateStatusIfCurrent(
		ctx,
		queued.ID,
		domain.PlayerStatusIdle,
		domain.PlayerStatusQueued,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, board.IncrementWin(ctx, first.Username))

	beforePostgres := casualPostgresSnapshot(t, pool)
	beforeQueue, err := redisFixture.client.LRange(ctx, queueKey, 0, -1).Result()
	require.NoError(t, err)
	beforeBoard, err := redisFixture.client.ZRangeWithScores(ctx, boardKey, 0, -1).Result()
	require.NoError(t, err)

	tx := postgres.NewTxManager(pool)
	tournaments := postgres.NewArenaTournamentPostgres(tx)
	tournamentID := uuid.New()
	rosterID := uuid.New()
	tournamentUseCase := arena.NewTournamentUseCase(tournaments, fixedIntegrationClock{now: now.Add(time.Minute)})
	created, changed, err := tournamentUseCase.CreateTournament(ctx, arena.TournamentCreateCommand{
		TournamentID: tournamentID,
		RosterID:     rosterID,
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, rosterID, created.RosterID)
	for index, playerID := range arenaPlayers {
		_, added, addErr := tournaments.AddParticipant(ctx, postgres.ArenaParticipantInput{
			ID:         uuid.New(),
			RosterID:   rosterID,
			PlayerID:   playerID,
			Seed:       int32(index + 1),
			Attendance: domain.ArenaAttendanceStateCheckedIn,
			CreatedAt:  now.Add(time.Minute),
		})
		require.NoError(t, addErr)
		require.True(t, added)
	}

	lockUseCase := arena.NewRosterLockUseCase(tournaments, fixedIntegrationClock{now: now.Add(2 * time.Minute)})
	locked, changed, err := lockUseCase.LockRoster(ctx, arena.RosterLockCommand{
		Preflight: arena.RosterPreflightEvidence{
			RosterID:           rosterID,
			RosterRevision:     1,
			CheckedInPlayerIDs: arenaPlayers,
			Approved:           true,
		},
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, locked.LockedAt)

	require.Equal(t, beforePostgres, casualPostgresSnapshot(t, pool))
	afterQueue, err := redisFixture.client.LRange(ctx, queueKey, 0, -1).Result()
	require.NoError(t, err)
	require.Equal(t, beforeQueue, afterQueue)
	afterBoard, err := redisFixture.client.ZRangeWithScores(ctx, boardKey, 0, -1).Result()
	require.NoError(t, err)
	require.Equal(t, beforeBoard, afterBoard)
	afterQueuedReservation, err := duelState.players.GetParticipantReservation(ctx, queued.ID)
	require.NoError(t, err)
	require.Equal(t, queuedReservation, afterQueuedReservation)
}

func casualPostgresSnapshot(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()

	var snapshot string
	err := pool.QueryRow(t.Context(), `
		SELECT jsonb_build_object(
			'players', COALESCE((SELECT jsonb_agg(to_jsonb(row) ORDER BY row.id) FROM players row), '[]'::jsonb),
			'tasks', COALESCE((SELECT jsonb_agg(to_jsonb(row) ORDER BY row.id) FROM tasks row), '[]'::jsonb),
			'duels', COALESCE((SELECT jsonb_agg(to_jsonb(row) ORDER BY row.id) FROM duels row), '[]'::jsonb),
			'duel_player_tasks', COALESCE((SELECT jsonb_agg(to_jsonb(row) ORDER BY row.duel_id, row.player_id) FROM duel_player_tasks row), '[]'::jsonb),
			'player_task_history', COALESCE((SELECT jsonb_agg(to_jsonb(row) ORDER BY row.player_id, row.task_id) FROM player_task_history row), '[]'::jsonb)
		)::text
	`).Scan(&snapshot)
	require.NoError(t, err)
	return snapshot
}
