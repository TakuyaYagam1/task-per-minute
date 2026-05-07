//go:build integration

package integration_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// TestDuel_RealStack_ProductionScenarioAntiRepeat covers the exact bug
// reported on production: takuya has solved цезарь via correct flag,
// then queues with a brand-new account. The matchmaker must assign the
// remaining easy task (бинарь) to BOTH players, never reassigning цезарь
// to the new account. Lockdown for fix 4f024b5.
func TestDuel_RealStack_ProductionScenarioAntiRepeat(t *testing.T) {
	f := newDuelScenarioFixture(t)

	caesar := f.makeTaskWithLimit(t, uniq("caesar"), domain.DifficultyEasy, 60)
	binary := f.makeTaskWithLimit(t, uniq("binary"), domain.DifficultyEasy, 60)

	takuya := f.makePlayer(t, uniq("takuya"))
	newbie := f.makePlayer(t, uniq("newbie"))

	f.markSolved(t, takuya.ID, caesar)

	result := f.matchPlayers(t, takuya.ID, newbie.ID)

	require.Equal(t, binary.ID, taskForPlayer(t, result, takuya.ID).ID,
		"takuya must get the only task he hasn't solved")
	require.Equal(t, binary.ID, taskForPlayer(t, result, newbie.ID).ID,
		"newbie must get the same task, NOT caesar (which takuya already solved)")
}

// TestDuel_RealStack_FlagSubmitDrivesAntiRepeat exercises the FULL
// production code path end-to-end: takuya wins a real duel by submitting
// the correct flag (which writes player_task_history through
// FlagSubmitUsecase.finishCorrectFlag inside the same Postgres tx),
// then immediately re-queues with a fresh account. The matchmaker must
// see the just-committed history row and assign the remaining task to
// both players. This catches any regression in the read-after-commit
// path between flag submission and matchmaking.
func TestDuel_RealStack_FlagSubmitDrivesAntiRepeat(t *testing.T) {
	f := newDuelScenarioFixture(t)

	caesar := f.makeTaskWithLimit(t, uniq("caesar"), domain.DifficultyEasy, 60)
	binary := f.makeTaskWithLimit(t, uniq("binary"), domain.DifficultyEasy, 60)

	takuya := f.makePlayer(t, uniq("takuya"))
	sparring := f.makePlayer(t, uniq("sparring"))
	newbie := f.makePlayer(t, uniq("newbie"))

	firstDuel := f.matchPlayers(t, takuya.ID, sparring.ID)
	f.submitWinningFlag(t, firstDuel, takuya.ID)

	takuyaSolvedID := taskForPlayer(t, firstDuel, takuya.ID).ID
	var unsolvedID uuid.UUID
	switch takuyaSolvedID {
	case caesar.ID:
		unsolvedID = binary.ID
	case binary.ID:
		unsolvedID = caesar.ID
	default:
		t.Fatalf("first duel returned an unexpected task %s", takuyaSolvedID)
	}

	secondDuel := f.matchPlayers(t, takuya.ID, newbie.ID)

	require.Equal(t, unsolvedID, taskForPlayer(t, secondDuel, takuya.ID).ID,
		"takuya must get the task he has not solved")
	require.Equal(t, unsolvedID, taskForPlayer(t, secondDuel, newbie.ID).ID,
		"newbie must get the same unsolved task; never the one takuya already solved")
}

// TestDuel_RealStack_FourTasksOneSolved locks down the same-pool branch:
// when there are >=2 tasks unsolved by both players, both players must get
// the same shared-unsolved task and never a task already solved by either.
func TestDuel_RealStack_FourTasksOneSolved(t *testing.T) {
	f := newDuelScenarioFixture(t)

	pool := make([]*domain.Task, 0, 4)
	for range 4 {
		pool = append(pool, f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 60))
	}

	takuya := f.makePlayer(t, uniq("takuya"))
	newbie := f.makePlayer(t, uniq("newbie"))

	solved := pool[0]
	f.markSolved(t, takuya.ID, solved)

	unsolvedIDs := map[uuid.UUID]struct{}{
		pool[1].ID: {},
		pool[2].ID: {},
		pool[3].ID: {},
	}

	result := f.matchPlayers(t, takuya.ID, newbie.ID)
	takuyaTask := taskForPlayer(t, result, takuya.ID)
	newbieTask := taskForPlayer(t, result, newbie.ID)

	require.Equal(t, takuyaTask.ID, newbieTask.ID,
		"with >=2 shared-unsolved tasks, players must get the same task")
	require.Contains(t, unsolvedIDs, takuyaTask.ID,
		"takuya's task must be from the unsolved-by-both set, never the solved one")
	require.Contains(t, unsolvedIDs, newbieTask.ID,
		"newbie's task must be from the unsolved-by-both set, never the one takuya solved")
}
