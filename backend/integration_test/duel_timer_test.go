//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

func TestDuelTimer_ExpiresDuelAsDraw(t *testing.T) {
	t.Parallel()

	f := newDuelFixture()
	ctx := context.Background()
	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	aliceTask := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 2)
	bobTask := f.makeTaskWithLimit(t, uniq("easy"), domain.DifficultyEasy, 2)
	deadline := time.Now().UTC().Add(2 * time.Second)
	duel := f.makeActiveDuel(t, alice.ID, bob.ID, deadline)
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, alice.ID, aliceTask.ID))
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, duel.ID, bob.ID, bobTask.ID))

	registry := duelusecase.NewTimerRegistry(f.mgr, f.duels, f.players, nil)
	expired := make(chan struct{})
	registry.Start(duel.ID, deadline, func() { close(expired) })

	select {
	case <-expired:
	case <-time.After(4 * time.Second):
		t.Fatal("timer did not expire duel")
	}

	got, err := f.duels.GetByID(ctx, duel.ID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, got.Status)
	require.Nil(t, got.WinnerID)
	require.NotNil(t, got.FinishedAt)

	gotAlice, err := f.players.GetByID(ctx, alice.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusIdle, gotAlice.Status)
	gotBob, err := f.players.GetByID(ctx, bob.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusIdle, gotBob.Status)

	aliceSolved, err := f.history.ListSolvedTaskIDs(ctx, alice.ID)
	require.NoError(t, err)
	require.Empty(t, aliceSolved, "expired duel must not write player history")
}
