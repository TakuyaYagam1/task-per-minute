package duel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
	logkit "github.com/wahrwelt-kit/go-logkit"
)

type FlagSubmitUseCase struct {
	tx      TransactionManager
	duels   FlagDuelRepository
	players FinalizationPlayerRepository
	history SolvedHistoryWriter
	board   LeaderboardBumper
	timers  TimerStopper
	log     logkit.Logger
	clock   Clock
}

type TimerStopper interface {
	Stop(duelID uuid.UUID) bool
}

type FlagSubmitOption func(*FlagSubmitUseCase)

func WithFlagSubmitLogger(log logkit.Logger) FlagSubmitOption {
	return func(u *FlagSubmitUseCase) {
		u.log = log
	}
}

func NewFlagSubmitUseCase(
	tx TransactionManager,
	duels FlagDuelRepository,
	players FinalizationPlayerRepository,
	history SolvedHistoryWriter,
	board LeaderboardBumper,
	clk Clock,
	timers ...TimerStopper,
) *FlagSubmitUseCase {
	var timer TimerStopper
	if len(timers) > 0 {
		timer = timers[0]
	}
	return &FlagSubmitUseCase{
		tx:      tx,
		duels:   duels,
		players: players,
		history: history,
		board:   board,
		timers:  timer,
		clock:   clk,
	}
}

func (u *FlagSubmitUseCase) Configure(options ...FlagSubmitOption) *FlagSubmitUseCase {
	for _, opt := range options {
		if opt != nil {
			opt(u)
		}
	}
	return u
}

func (u *FlagSubmitUseCase) SubmitFlag(ctx context.Context, duelID, playerID uuid.UUID, flag string) (Result, error) {
	now := u.clock.Now()
	var result Result

	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		duel, task, err := u.validateSubmission(txCtx, duelID, playerID, flag, now)
		if err != nil {
			if errors.Is(err, domain.ErrDuelFinished) {
				result = Result{AlreadyFinished: true}
				return nil
			}
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

	if u.timers != nil && result.Correct {
		u.timers.Stop(duelID)
	}

	if result.Winner != nil {
		bumpLeaderboard(ctx, u.board, u.log, duelID, result.Winner.Username)
	}

	return result, nil
}

func (u *FlagSubmitUseCase) validateSubmission(
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
		return nil, nil, domain.ErrDuelFinished
	}
	if !now.Before(duel.Deadline) {
		return nil, nil, domain.ErrDuelDeadlinePassed
	}

	task, err := u.duels.GetPlayerTask(ctx, duelID, playerID)
	if err != nil {
		return nil, nil, fmt.Errorf("FlagSubmitUsecase - validateSubmission - DuelRepo.GetPlayerTask: %w", err)
	}
	if task.Flag != flag {
		return nil, nil, domain.ErrFlagIncorrect
	}
	return duel, task, nil
}

func (u *FlagSubmitUseCase) finishCorrectFlag(
	ctx context.Context,
	duel *domain.Duel,
	task *domain.Task,
	playerID uuid.UUID,
	now time.Time,
) (Result, error) {
	winner, err := u.players.GetByID(ctx, playerID)
	if err != nil {
		return Result{}, fmt.Errorf("FlagSubmitUsecase - finishCorrectFlag - PlayerRepo.GetByID: %w", err)
	}

	finished, err := u.duels.Finish(ctx, duel.ID, &playerID, now, domain.DuelStatusFinished)
	if err != nil {
		if errors.Is(err, domain.ErrDuelFinished) {
			return Result{AlreadyFinished: true}, nil
		}
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

	return Result{
		Correct:      true,
		FinishedDuel: finished,
		Winner:       winner,
	}, nil
}
