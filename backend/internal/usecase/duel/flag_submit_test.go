package duel_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	usecasemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/mocks"
)

func TestFlagSubmitUsecase_SubmitFlag_CorrectFinishesDuel(t *testing.T) {
	t.Parallel()

	f := newFlagFixture(t)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	duel := activeDuel(now.Add(time.Minute))
	playerID := duel.Player1ID
	task := &domain.Task{ID: uuid.New(), Flag: "FLAG{ok}"}
	winner := &domain.Player{ID: playerID, Username: "alice", Status: domain.PlayerStatusInDuel}
	timers := &timerStopperSpy{}
	finished := *duel
	finished.Status = domain.DuelStatusFinished
	finished.WinnerID = &playerID
	finished.FinishedAt = &now

	f.tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(runTx)
	f.duels.EXPECT().GetByID(mock.Anything, duel.ID).Return(duel, nil)
	f.duels.EXPECT().GetPlayerTask(mock.Anything, duel.ID, playerID).Return(task, nil)
	f.players.EXPECT().GetByID(mock.Anything, playerID).Return(winner, nil)
	f.duels.EXPECT().Finish(mock.Anything, duel.ID, &playerID, now, domain.DuelStatusFinished).Return(&finished, nil)
	f.duels.EXPECT().MarkSolved(mock.Anything, duel.ID, playerID, now).Return(nil)
	f.history.EXPECT().AddSolved(mock.Anything, playerID, task.ID, now).Return(nil)
	f.players.EXPECT().UpdateStatus(mock.Anything, duel.Player1ID, domain.PlayerStatusIdle).Return(withStatus(winner, domain.PlayerStatusIdle), nil)
	f.players.EXPECT().UpdateStatus(mock.Anything, duel.Player2ID, domain.PlayerStatusIdle).Return(&domain.Player{ID: duel.Player2ID, Username: "bob", Status: domain.PlayerStatusIdle}, nil)
	f.board.EXPECT().IncrementWin(mock.Anything, winner.Username).Return(nil)

	got, err := duelusecase.NewFlagSubmitUsecase(
		f.tx, f.duels, f.players, f.history, f.board, fixedClock{now: now}, timers,
	).SubmitFlag(t.Context(), duel.ID, playerID, "FLAG{ok}")

	require.NoError(t, err)
	require.True(t, got.Correct)
	require.Same(t, winner, got.Winner)
	require.Equal(t, domain.DuelStatusFinished, got.FinishedDuel.Status)
	require.Equal(t, playerID, *got.FinishedDuel.WinnerID)
	require.Equal(t, []uuid.UUID{duel.ID}, timers.stopped)
}

func TestFlagSubmitUsecase_SubmitFlag_DeadlinePassed(t *testing.T) {
	t.Parallel()

	f := newFlagFixture(t)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	duel := activeDuel(now)

	f.tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(runTx)
	f.duels.EXPECT().GetByID(mock.Anything, duel.ID).Return(duel, nil)

	_, err := duelusecase.NewFlagSubmitUsecase(
		f.tx, f.duels, f.players, f.history, f.board, fixedClock{now: now},
	).SubmitFlag(t.Context(), duel.ID, duel.Player1ID, "FLAG{ok}")

	require.ErrorIs(t, err, apperr.ErrDuelDeadlinePassed)
}

func TestFlagSubmitUsecase_SubmitFlag_IncorrectFlag(t *testing.T) {
	t.Parallel()

	f := newFlagFixture(t)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	duel := activeDuel(now.Add(time.Minute))
	task := &domain.Task{ID: uuid.New(), Flag: "FLAG{ok}"}

	f.tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(runTx)
	f.duels.EXPECT().GetByID(mock.Anything, duel.ID).Return(duel, nil)
	f.duels.EXPECT().GetPlayerTask(mock.Anything, duel.ID, duel.Player1ID).Return(task, nil)

	_, err := duelusecase.NewFlagSubmitUsecase(
		f.tx, f.duels, f.players, f.history, f.board, fixedClock{now: now},
	).SubmitFlag(t.Context(), duel.ID, duel.Player1ID, "FLAG{bad}")

	require.ErrorIs(t, err, apperr.ErrFlagIncorrect)
}

func TestFlagSubmitUsecase_SubmitFlag_FinishedDuel(t *testing.T) {
	t.Parallel()

	f := newFlagFixture(t)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	duel := activeDuel(now.Add(time.Minute))
	duel.Status = domain.DuelStatusFinished

	f.tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(runTx)
	f.duels.EXPECT().GetByID(mock.Anything, duel.ID).Return(duel, nil)

	got, err := duelusecase.NewFlagSubmitUsecase(
		f.tx, f.duels, f.players, f.history, f.board, fixedClock{now: now},
	).SubmitFlag(t.Context(), duel.ID, duel.Player1ID, "FLAG{ok}")

	require.NoError(t, err)
	require.True(t, got.AlreadyFinished)
	require.False(t, got.Correct)
	require.Nil(t, got.Winner)
	require.Nil(t, got.FinishedDuel)
}

func TestFlagSubmitUsecase_SubmitFlag_FinishRaceReturnsAlreadyFinished(t *testing.T) {
	t.Parallel()

	f := newFlagFixture(t)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	duel := activeDuel(now.Add(time.Minute))
	playerID := duel.Player1ID
	task := &domain.Task{ID: uuid.New(), Flag: "FLAG{ok}"}
	winner := &domain.Player{ID: playerID, Username: "alice", Status: domain.PlayerStatusInDuel}
	timers := &timerStopperSpy{}

	f.tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(runTx)
	f.duels.EXPECT().GetByID(mock.Anything, duel.ID).Return(duel, nil)
	f.duels.EXPECT().GetPlayerTask(mock.Anything, duel.ID, playerID).Return(task, nil)
	f.players.EXPECT().GetByID(mock.Anything, playerID).Return(winner, nil)
	f.duels.EXPECT().Finish(mock.Anything, duel.ID, &playerID, now, domain.DuelStatusFinished).
		Return(nil, apperr.ErrDuelFinished)

	got, err := duelusecase.NewFlagSubmitUsecase(
		f.tx, f.duels, f.players, f.history, f.board, fixedClock{now: now}, timers,
	).SubmitFlag(t.Context(), duel.ID, playerID, "FLAG{ok}")

	require.NoError(t, err)
	require.True(t, got.AlreadyFinished)
	require.False(t, got.Correct)
	require.Nil(t, got.Winner)
	require.Nil(t, got.FinishedDuel)
	require.Empty(t, timers.stopped)
}

func TestFlagSubmitUsecase_SubmitFlag_TimerNotStoppedOnTxRollback(t *testing.T) {
	t.Parallel()

	f := newFlagFixture(t)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	duel := activeDuel(now.Add(time.Minute))
	playerID := duel.Player1ID
	task := &domain.Task{ID: uuid.New(), Flag: "FLAG{ok}"}
	winner := &domain.Player{ID: playerID, Username: "alice", Status: domain.PlayerStatusInDuel}
	finishErr := errors.New("simulated db failure")
	timers := &timerStopperSpy{}

	f.tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(runTx)
	f.duels.EXPECT().GetByID(mock.Anything, duel.ID).Return(duel, nil)
	f.duels.EXPECT().GetPlayerTask(mock.Anything, duel.ID, playerID).Return(task, nil)
	f.players.EXPECT().GetByID(mock.Anything, playerID).Return(winner, nil)
	f.duels.EXPECT().Finish(mock.Anything, duel.ID, &playerID, now, domain.DuelStatusFinished).Return(nil, finishErr)

	_, err := duelusecase.NewFlagSubmitUsecase(
		f.tx, f.duels, f.players, f.history, f.board, fixedClock{now: now}, timers,
	).SubmitFlag(t.Context(), duel.ID, playerID, "FLAG{ok}")

	require.ErrorIs(t, err, finishErr)
	require.Empty(t, timers.stopped, "timer must remain armed when the duel-finish tx rolls back")
}

func TestFlagSubmitUsecase_SubmitFlag_LeaderboardFailureDoesNotFailRequest(t *testing.T) {
	t.Parallel()

	f := newFlagFixture(t)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	duel := activeDuel(now.Add(time.Minute))
	playerID := duel.Player1ID
	task := &domain.Task{ID: uuid.New(), Flag: "FLAG{ok}"}
	winner := &domain.Player{ID: playerID, Username: "alice", Status: domain.PlayerStatusInDuel}
	timers := &timerStopperSpy{}
	finished := *duel
	finished.Status = domain.DuelStatusFinished
	finished.WinnerID = &playerID
	finished.FinishedAt = &now

	f.tx.EXPECT().Do(mock.Anything, mock.Anything).RunAndReturn(runTx)
	f.duels.EXPECT().GetByID(mock.Anything, duel.ID).Return(duel, nil)
	f.duels.EXPECT().GetPlayerTask(mock.Anything, duel.ID, playerID).Return(task, nil)
	f.players.EXPECT().GetByID(mock.Anything, playerID).Return(winner, nil)
	f.duels.EXPECT().Finish(mock.Anything, duel.ID, &playerID, now, domain.DuelStatusFinished).Return(&finished, nil)
	f.duels.EXPECT().MarkSolved(mock.Anything, duel.ID, playerID, now).Return(nil)
	f.history.EXPECT().AddSolved(mock.Anything, playerID, task.ID, now).Return(nil)
	f.players.EXPECT().UpdateStatus(mock.Anything, duel.Player1ID, domain.PlayerStatusIdle).Return(withStatus(winner, domain.PlayerStatusIdle), nil)
	f.players.EXPECT().UpdateStatus(mock.Anything, duel.Player2ID, domain.PlayerStatusIdle).Return(&domain.Player{ID: duel.Player2ID, Username: "bob", Status: domain.PlayerStatusIdle}, nil)
	f.board.EXPECT().IncrementWin(mock.Anything, winner.Username).Return(errors.New("redis is down"))

	got, err := duelusecase.NewFlagSubmitUsecase(
		f.tx, f.duels, f.players, f.history, f.board, fixedClock{now: now}, timers,
	).SubmitFlag(t.Context(), duel.ID, playerID, "FLAG{ok}")

	require.NoError(t, err)
	require.True(t, got.Correct)
	require.Equal(t, []uuid.UUID{duel.ID}, timers.stopped)
}

type flagFixture struct {
	tx      *usecasemocks.MockTxManager
	duels   *usecasemocks.MockDuelRepo
	players *usecasemocks.MockPlayerRepo
	history *usecasemocks.MockHistoryRepo
	board   *usecasemocks.MockLeaderboardStore
}

func newFlagFixture(t *testing.T) *flagFixture {
	t.Helper()
	return &flagFixture{
		tx:      usecasemocks.NewMockTxManager(t),
		duels:   usecasemocks.NewMockDuelRepo(t),
		players: usecasemocks.NewMockPlayerRepo(t),
		history: usecasemocks.NewMockHistoryRepo(t),
		board:   usecasemocks.NewMockLeaderboardStore(t),
	}
}

func runTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

func activeDuel(deadline time.Time) *domain.Duel {
	return &domain.Duel{
		ID:        uuid.New(),
		Player1ID: uuid.New(),
		Player2ID: uuid.New(),
		Status:    domain.DuelStatusActive,
		Deadline:  deadline,
		StartedAt: deadline.Add(-time.Minute),
	}
}

type timerStopperSpy struct {
	stopped []uuid.UUID
}

func (s *timerStopperSpy) Stop(duelID uuid.UUID) bool {
	s.stopped = append(s.stopped, duelID)
	return true
}
