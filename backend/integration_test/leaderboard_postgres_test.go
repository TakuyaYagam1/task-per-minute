//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent"
)

// fixedFinishedDuel injects a finished duel with explicit started_at and the
// given participants/winner via raw SQL. Used to make solve-time deterministic
// in leaderboard tests.
func fixedFinishedDuel(t *testing.T, p1, p2, winner uuid.UUID, startedAt, finishedAt time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO duels (player1_id, player2_id, status, winner_id, deadline, started_at, finished_at)
		VALUES ($1, $2, 'finished', $3, $5, $4, $5)
		RETURNING id`,
		p1, p2, winner, startedAt, finishedAt).Scan(&id)
	require.NoError(t, err)
	return id
}

// markSolvedRaw inserts a duel_player_tasks row in solved=true state with an
// explicit solved_at, bypassing the schema CHECK requirement of "create then
// update" used in production code.
func markSolvedRaw(t *testing.T, duelID, playerID, taskID uuid.UUID, solvedAt time.Time) {
	t.Helper()
	_, err := sharedPool.Exec(context.Background(), `
		INSERT INTO duel_player_tasks (duel_id, player_id, task_id, solved, solved_at)
		VALUES ($1, $2, $3, TRUE, $4)`,
		duelID, playerID, taskID, solvedAt)
	require.NoError(t, err)
}

func filterRows(rows []persistent.LeaderboardRow, ids ...uuid.UUID) []persistent.LeaderboardRow {
	want := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	out := make([]persistent.LeaderboardRow, 0, len(ids))
	for _, r := range rows {
		if _, ok := want[r.PlayerID]; ok {
			out = append(out, r)
		}
	}
	return out
}

func containsPlayer(rows []persistent.LeaderboardRow, id uuid.UUID) bool {
	for _, r := range rows {
		if r.PlayerID == id {
			return true
		}
	}
	return false
}

func TestLeaderboardRepo_TotalSolveTimePerPlayer_AggregatesScoped(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	charlie := f.makePlayer(t, uniq("charlie"))
	t1 := f.makeTask(t, uniq("t"), domain.DifficultyEasy)
	t2 := f.makeTask(t, uniq("t"), domain.DifficultyMedium)
	t3 := f.makeTask(t, uniq("t"), domain.DifficultyEasy)

	now := time.Now().UTC().Add(-time.Hour)

	d1 := fixedFinishedDuel(t, alice.ID, bob.ID, alice.ID, now, now.Add(2*time.Second))
	markSolvedRaw(t, d1, alice.ID, t1.ID, now.Add(2*time.Second))

	d2 := fixedFinishedDuel(t, alice.ID, charlie.ID, alice.ID, now.Add(time.Minute), now.Add(time.Minute+3*time.Second))
	markSolvedRaw(t, d2, alice.ID, t2.ID, now.Add(time.Minute+3*time.Second))

	d3 := fixedFinishedDuel(t, bob.ID, charlie.ID, bob.ID, now.Add(2*time.Minute), now.Add(2*time.Minute+10*time.Second))
	markSolvedRaw(t, d3, bob.ID, t3.ID, now.Add(2*time.Minute+10*time.Second))

	all, err := f.board.TotalSolveTimePerPlayer(ctx)
	require.NoError(t, err)

	mine := filterRows(all, alice.ID, bob.ID, charlie.ID)
	require.Len(t, mine, 2, "only alice and bob have wins among our trio")

	by := make(map[uuid.UUID]int64, 2)
	for _, r := range mine {
		by[r.PlayerID] = r.TotalSolveTimeMs
	}
	require.Equal(t, int64(5000), by[alice.ID], "alice = 2000ms + 3000ms")
	require.Equal(t, int64(10000), by[bob.ID])
	require.False(t, containsPlayer(mine, charlie.ID), "charlie has no wins")
}

func TestLeaderboardRepo_TotalSolveTimeForPlayers_AggregatesRequestedUsers(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	charlie := f.makePlayer(t, uniq("charlie"))
	task1 := f.makeTask(t, uniq("t"), domain.DifficultyEasy)
	task2 := f.makeTask(t, uniq("t"), domain.DifficultyEasy)

	now := time.Now().UTC().Add(-time.Hour)
	d1 := fixedFinishedDuel(t, alice.ID, bob.ID, alice.ID, now, now.Add(1500*time.Millisecond))
	markSolvedRaw(t, d1, alice.ID, task1.ID, now.Add(1500*time.Millisecond))

	d2 := fixedFinishedDuel(t, bob.ID, charlie.ID, bob.ID, now.Add(time.Minute), now.Add(time.Minute+4*time.Second))
	markSolvedRaw(t, d2, bob.ID, task2.ID, now.Add(time.Minute+4*time.Second))

	rows, err := f.board.TotalSolveTimeForPlayers(ctx, []string{alice.Username, charlie.Username})
	require.NoError(t, err)
	require.Len(t, rows, 2)

	byUsername := make(map[string]persistent.LeaderboardRow, 2)
	for _, row := range rows {
		byUsername[row.Username] = row
	}
	require.Equal(t, int64(1500), byUsername[alice.Username].TotalSolveTimeMs)
	require.Equal(t, int64(0), byUsername[charlie.Username].TotalSolveTimeMs,
		"requested users with no wins must be present with zero time")
	require.NotContains(t, byUsername, bob.Username, "non-requested winner must stay out of scoped query")
}

func TestLeaderboardRepo_TotalSolveTimePerPlayer_IgnoresActiveDuels(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))

	_, err := f.duels.Create(ctx, alice.ID, bob.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)

	rows, err := f.board.TotalSolveTimePerPlayer(ctx)
	require.NoError(t, err)
	require.False(t, containsPlayer(rows, alice.ID), "alice with only active duel must not be in leaderboard")
	require.False(t, containsPlayer(rows, bob.ID), "bob with only active duel must not be in leaderboard")
}
