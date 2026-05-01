//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func hasDuelID(list []*domain.Duel, id uuid.UUID) bool {
	for _, d := range list {
		if d.ID == id {
			return true
		}
	}
	return false
}

func TestDuelRepo_Create_HappyPath(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	deadline := time.Now().Add(5 * time.Minute).UTC()

	d, err := f.duels.Create(ctx, p1.ID, p2.ID, deadline)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, d.ID)
	require.Equal(t, p1.ID, d.Player1ID)
	require.Equal(t, p2.ID, d.Player2ID)
	require.Equal(t, domain.DuelStatusActive, d.Status)
	require.Nil(t, d.WinnerID)
	require.Nil(t, d.FinishedAt)
	require.WithinDuration(t, deadline, d.Deadline, time.Second)
	require.False(t, d.StartedAt.IsZero())
}

func TestDuelRepo_Create_SamePlayerTwice_ReturnsValidation(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	p := f.makePlayer(t, uniq("alice"))
	_, err := f.duels.Create(context.Background(), p.ID, p.ID, time.Now().Add(time.Minute))
	require.ErrorIs(t, err, apperr.ErrValidation)
}

func TestDuelRepo_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	_, err := f.duels.GetByID(context.Background(), uuid.New())
	require.ErrorIs(t, err, apperr.ErrDuelNotFound)
}

func TestDuelRepo_GetActiveByPlayerID(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	stranger := f.makePlayer(t, uniq("charlie"))
	d, err := f.duels.Create(ctx, p1.ID, p2.ID, time.Now().Add(5*time.Minute))
	require.NoError(t, err)

	for _, pid := range []uuid.UUID{p1.ID, p2.ID} {
		got, err := f.duels.GetActiveByPlayerID(ctx, pid)
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, d.ID, got.ID)
	}

	got, err := f.duels.GetActiveByPlayerID(ctx, stranger.ID)
	require.NoError(t, err)
	require.Nil(t, got, "stranger has no active duel")

	finished, err := f.duels.Finish(ctx, d.ID, &p1.ID, time.Now(), domain.DuelStatusFinished)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, finished.Status)

	got, err = f.duels.GetActiveByPlayerID(ctx, p1.ID)
	require.NoError(t, err)
	require.Nil(t, got, "finished duel must NOT be reported as active")
}

func TestDuelRepo_UpdateDeadline(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	d, err := f.duels.Create(ctx, p1.ID, p2.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)

	newDeadline := time.Now().Add(5 * time.Minute).UTC()
	updated, err := f.duels.UpdateDeadline(ctx, d.ID, newDeadline)
	require.NoError(t, err)
	require.Equal(t, d.ID, updated.ID)
	require.WithinDuration(t, newDeadline, updated.Deadline, time.Second)

	_, err = f.duels.UpdateDeadline(ctx, uuid.New(), newDeadline)
	require.ErrorIs(t, err, apperr.ErrDuelNotFound)

	_, err = f.duels.Finish(ctx, d.ID, nil, time.Now().UTC(), domain.DuelStatusFinished)
	require.NoError(t, err)
	_, err = f.duels.UpdateDeadline(ctx, d.ID, time.Now().Add(10*time.Minute))
	require.ErrorIs(t, err, apperr.ErrDuelFinished)
}

func TestDuelRepo_Finish_NormalWin(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()
	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	d, err := f.duels.Create(ctx, p1.ID, p2.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)

	finishedAt := time.Now().UTC()
	got, err := f.duels.Finish(ctx, d.ID, &p1.ID, finishedAt, domain.DuelStatusFinished)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, got.Status)
	require.NotNil(t, got.WinnerID)
	require.Equal(t, p1.ID, *got.WinnerID)
	require.NotNil(t, got.FinishedAt)
	require.WithinDuration(t, finishedAt, *got.FinishedAt, time.Second)
}

func TestDuelRepo_Finish_Draw_NilWinner(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()
	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	d, err := f.duels.Create(ctx, p1.ID, p2.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)

	got, err := f.duels.Finish(ctx, d.ID, nil, time.Now(), domain.DuelStatusFinished)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, got.Status)
	require.Nil(t, got.WinnerID, "draws have no winner")
	require.NotNil(t, got.FinishedAt)
}

func TestDuelRepo_Finish_NotFound(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	_, err := f.duels.Finish(context.Background(), uuid.New(), nil, time.Now(), domain.DuelStatusFinished)
	require.ErrorIs(t, err, apperr.ErrDuelNotFound)
}

func TestDuelRepo_Finish_ConcurrentSecondReturnsFinished(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()
	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	d, err := f.duels.Create(ctx, p1.ID, p2.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)

	errs := make(chan error, 2)
	for _, winner := range []uuid.UUID{p1.ID, p2.ID} {
		go func(winner uuid.UUID) {
			_, err := f.duels.Finish(ctx, d.ID, &winner, time.Now().UTC(), domain.DuelStatusFinished)
			errs <- err
		}(winner)
	}

	var successes, finishedRaces int
	for i := 0; i < 2; i++ {
		err := <-errs
		switch {
		case err == nil:
			successes++
		case errors.Is(err, apperr.ErrDuelFinished):
			finishedRaces++
		default:
			require.NoError(t, err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, finishedRaces)
}

func TestDuelRepo_ListActive_ContainsOurActiveAndExcludesOurFinished(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	p3 := f.makePlayer(t, uniq("charlie"))
	p4 := f.makePlayer(t, uniq("dave"))

	active1, err := f.duels.Create(ctx, p1.ID, p2.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)
	active2, err := f.duels.Create(ctx, p3.ID, p4.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)
	finished, err := f.duels.Create(ctx, p1.ID, p3.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)
	_, err = f.duels.Finish(ctx, finished.ID, &p1.ID, time.Now(), domain.DuelStatusFinished)
	require.NoError(t, err)

	got, err := f.duels.ListActive(ctx)
	require.NoError(t, err)
	require.True(t, hasDuelID(got, active1.ID), "list must contain active1")
	require.True(t, hasDuelID(got, active2.ID), "list must contain active2")
	require.False(t, hasDuelID(got, finished.ID), "finished duel must not appear")
}

func TestDuelRepo_DuelPlayerTask_TwoInsertsInOneTx(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	t1 := f.makeTask(t, uniq("t1"), domain.DifficultyEasy)
	t2 := f.makeTask(t, uniq("t2"), domain.DifficultyEasy)

	var duelID uuid.UUID
	err := f.mgr.Do(ctx, func(txCtx context.Context) error {
		d, err := f.duels.Create(txCtx, p1.ID, p2.ID, time.Now().Add(time.Minute))
		if err != nil {
			return err
		}
		duelID = d.ID
		if err := f.duels.CreateDuelPlayerTask(txCtx, d.ID, p1.ID, t1.ID); err != nil {
			return err
		}
		return f.duels.CreateDuelPlayerTask(txCtx, d.ID, p2.ID, t2.ID)
	})
	require.NoError(t, err)

	dpt1, err := f.duels.GetDuelPlayerTask(ctx, duelID, p1.ID)
	require.NoError(t, err)
	require.Equal(t, t1.ID, dpt1.TaskID)
	require.False(t, dpt1.Solved)
	require.Nil(t, dpt1.SolvedAt)

	dpt2, err := f.duels.GetDuelPlayerTask(ctx, duelID, p2.ID)
	require.NoError(t, err)
	require.Equal(t, t2.ID, dpt2.TaskID)
}

func TestDuelRepo_CreateDuelPlayerTask_FKViolation(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()

	err := f.duels.CreateDuelPlayerTask(context.Background(), uuid.New(), uuid.New(), uuid.New())
	require.Error(t, err)
}

func TestDuelRepo_DuelPlayerTask_TxRollback(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	t1 := f.makeTask(t, uniq("t1"), domain.DifficultyEasy)

	var duelID uuid.UUID
	bust := apperr.ErrInternal
	err := f.mgr.Do(ctx, func(txCtx context.Context) error {
		d, err := f.duels.Create(txCtx, p1.ID, p2.ID, time.Now().Add(time.Minute))
		if err != nil {
			return err
		}
		duelID = d.ID
		if err := f.duels.CreateDuelPlayerTask(txCtx, d.ID, p1.ID, t1.ID); err != nil {
			return err
		}
		return bust
	})
	require.ErrorIs(t, err, bust)

	if duelID != uuid.Nil {
		_, err = f.duels.GetByID(ctx, duelID)
		require.ErrorIs(t, err, apperr.ErrDuelNotFound, "rolled-back duel must not exist")
	}

	got, err := f.duels.GetActiveByPlayerID(ctx, p1.ID)
	require.NoError(t, err)
	require.Nil(t, got, "alice must have no active duel after rollback")
}

func TestDuelRepo_GetDuelPlayerTask_NotParticipant(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	stranger := f.makePlayer(t, uniq("charlie"))
	d, err := f.duels.Create(ctx, p1.ID, p2.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)

	_, err = f.duels.GetDuelPlayerTask(ctx, d.ID, stranger.ID)
	require.ErrorIs(t, err, apperr.ErrNotDuelParticipant)
}

func TestDuelRepo_GetPlayerTask(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	stranger := f.makePlayer(t, uniq("charlie"))
	t1 := f.makeTask(t, uniq("t1"), domain.DifficultyEasy)
	t2 := f.makeTask(t, uniq("t2"), domain.DifficultyHard)
	d, err := f.duels.Create(ctx, p1.ID, p2.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, d.ID, p1.ID, t1.ID))
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, d.ID, p2.ID, t2.ID))

	got, err := f.duels.GetPlayerTask(ctx, d.ID, p1.ID)
	require.NoError(t, err)
	require.Equal(t, t1.ID, got.ID)
	require.Equal(t, domain.DifficultyEasy, got.Difficulty)

	got, err = f.duels.GetPlayerTask(ctx, d.ID, p2.ID)
	require.NoError(t, err)
	require.Equal(t, t2.ID, got.ID)

	_, err = f.duels.GetPlayerTask(ctx, d.ID, stranger.ID)
	require.ErrorIs(t, err, apperr.ErrNotDuelParticipant)
}

func TestDuelRepo_MarkSolved(t *testing.T) {
	t.Parallel()
	f := newDuelFixture()
	ctx := context.Background()

	p1 := f.makePlayer(t, uniq("alice"))
	p2 := f.makePlayer(t, uniq("bob"))
	t1 := f.makeTask(t, uniq("t1"), domain.DifficultyEasy)
	d, err := f.duels.Create(ctx, p1.ID, p2.ID, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.NoError(t, f.duels.CreateDuelPlayerTask(ctx, d.ID, p1.ID, t1.ID))

	solvedAt := time.Now().UTC()
	require.NoError(t, f.duels.MarkSolved(ctx, d.ID, p1.ID, solvedAt))

	got, err := f.duels.GetDuelPlayerTask(ctx, d.ID, p1.ID)
	require.NoError(t, err)
	require.True(t, got.Solved)
	require.NotNil(t, got.SolvedAt)
	require.WithinDuration(t, solvedAt, *got.SolvedAt, time.Second)
}
