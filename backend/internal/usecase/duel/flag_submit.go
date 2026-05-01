package duel

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

// Result aliases usecase.FlagSubmitResult so callers may use either
// duel.Result or usecase.FlagSubmitResult.
type Result = usecase.FlagSubmitResult

type FlagSubmitUsecase struct {
	tx      usecase.TxManager
	duels   usecase.DuelRepo
	players usecase.PlayerRepo
	history usecase.HistoryRepo
	board   usecase.LeaderboardStore
	timers  TimerStopper
	clock   clock.Clock
}

type TimerStopper interface {
	Stop(duelID uuid.UUID) bool
}

func NewFlagSubmitUsecase(
	tx usecase.TxManager,
	duels usecase.DuelRepo,
	players usecase.PlayerRepo,
	history usecase.HistoryRepo,
	board usecase.LeaderboardStore,
	clk clock.Clock,
	timers ...TimerStopper,
) *FlagSubmitUsecase {
	if clk == nil {
		clk = clock.Real{}
	}
	var timer TimerStopper
	if len(timers) > 0 {
		timer = timers[0]
	}
	return &FlagSubmitUsecase{
		tx:      tx,
		duels:   duels,
		players: players,
		history: history,
		board:   board,
		timers:  timer,
		clock:   clk,
	}
}

func (u *FlagSubmitUsecase) SubmitFlag(ctx context.Context, duelID, playerID uuid.UUID, flag string) (Result, error) {
	now := u.clock.Now()
	var result Result

	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		duel, task, err := u.validateSubmission(txCtx, duelID, playerID, flag, now)
		if err != nil {
			return err
		}

		var finishErr error
		result, finishErr = u.finishCorrectFlag(txCtx, duel, task, playerID, now)
		if finishErr != nil {
			return finishErr
		}
		return nil
	}); err != nil {
		return Result{}, err
	}

	return result, nil
}

func (u *FlagSubmitUsecase) validateSubmission(
	ctx context.Context,
	duelID uuid.UUID,
	playerID uuid.UUID,
	flag string,
	now time.Time,
) (*domain.Duel, *domain.Task, error) {
	duel, err := u.duels.GetByID(ctx, duelID)
	if err != nil {
		return nil, nil, fmt.Errorf("FlagSubmitUsecase - validateSubmission - DuelRepo.GetByID: %w", err)
	}
	if duel.Status != domain.DuelStatusActive {
		return nil, nil, apperr.ErrDuelFinished
	}
	if !now.Before(duel.Deadline) {
		return nil, nil, apperr.ErrDuelDeadlinePassed
	}

	task, err := u.duels.GetPlayerTask(ctx, duelID, playerID)
	if err != nil {
		return nil, nil, fmt.Errorf("FlagSubmitUsecase - validateSubmission - DuelRepo.GetPlayerTask: %w", err)
	}
	if task.Flag != flag {
		return nil, nil, apperr.ErrFlagIncorrect
	}
	return duel, task, nil
}

func (u *FlagSubmitUsecase) finishCorrectFlag(
	ctx context.Context,
	duel *domain.Duel,
	task *domain.Task,
	playerID uuid.UUID,
	now time.Time,
) (Result, error) {
	if u.timers != nil {
		u.timers.Stop(duel.ID)
	}

	winner, err := u.players.GetByID(ctx, playerID)
	if err != nil {
		return Result{}, fmt.Errorf("FlagSubmitUsecase - finishCorrectFlag - PlayerRepo.GetByID: %w", err)
	}

	finished, err := u.duels.Finish(ctx, duel.ID, &playerID, now, domain.DuelStatusFinished)
	if err != nil {
		return Result{}, fmt.Errorf("FlagSubmitUsecase - finishCorrectFlag - DuelRepo.Finish: %w", err)
	}
	if err := u.duels.MarkSolved(ctx, duel.ID, playerID, now); err != nil {
		return Result{}, fmt.Errorf("FlagSubmitUsecase - finishCorrectFlag - DuelRepo.MarkSolved: %w", err)
	}
	if err := u.history.AddSolved(ctx, playerID, task.ID, now); err != nil {
		return Result{}, fmt.Errorf("FlagSubmitUsecase - finishCorrectFlag - HistoryRepo.AddSolved: %w", err)
	}
	if _, err := u.players.UpdateStatus(ctx, duel.Player1ID, domain.PlayerStatusIdle); err != nil {
		return Result{}, fmt.Errorf("FlagSubmitUsecase - finishCorrectFlag - PlayerRepo.UpdateStatus player1: %w", err)
	}
	if _, err := u.players.UpdateStatus(ctx, duel.Player2ID, domain.PlayerStatusIdle); err != nil {
		return Result{}, fmt.Errorf("FlagSubmitUsecase - finishCorrectFlag - PlayerRepo.UpdateStatus player2: %w", err)
	}
	if err := u.board.IncrementWin(ctx, winner.Username); err != nil {
		return Result{}, fmt.Errorf("FlagSubmitUsecase - finishCorrectFlag - LeaderboardStore.IncrementWin: %w", err)
	}

	return Result{
		Correct:      true,
		FinishedDuel: finished,
		Winner:       winner,
	}, nil
}
