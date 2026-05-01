package duel

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

type MatchmakingUsecase struct {
	tx      usecase.TxManager
	queue   usecase.MatchmakingQueue
	players usecase.PlayerRepo
	tasks   usecase.TaskRepo
	history usecase.HistoryRepo
	duels   usecase.DuelRepo
	clock   clock.Clock
}

// MatchResult aliases the canonical declaration in
// internal/usecase/contracts.go.
type MatchResult = usecase.MatchResult

func NewMatchmakingUsecase(
	tx usecase.TxManager,
	queue usecase.MatchmakingQueue,
	players usecase.PlayerRepo,
	tasks usecase.TaskRepo,
	history usecase.HistoryRepo,
	duels usecase.DuelRepo,
	clk clock.Clock,
) *MatchmakingUsecase {
	return &MatchmakingUsecase{
		tx:      tx,
		queue:   queue,
		players: players,
		tasks:   tasks,
		history: history,
		duels:   duels,
		clock:   clk,
	}
}

func (u *MatchmakingUsecase) JoinQueue(ctx context.Context, playerID uuid.UUID) (*MatchResult, error) {
	player, err := u.players.GetByID(ctx, playerID)
	if err != nil {
		return nil, fmt.Errorf("MatchmakingUsecase - JoinQueue - PlayerRepo.GetByID: %w", err)
	}
	if player.Status == domain.PlayerStatusInDuel {
		return nil, apperr.ErrPlayerInDuel
	}

	if err := u.queue.Enqueue(ctx, playerID); err != nil {
		return nil, fmt.Errorf("MatchmakingUsecase - JoinQueue - MatchmakingQueue.Enqueue: %w", err)
	}
	if _, err := u.players.UpdateStatus(ctx, playerID, domain.PlayerStatusQueued); err != nil {
		return nil, fmt.Errorf("MatchmakingUsecase - JoinQueue - PlayerRepo.UpdateStatus queued: %w", err)
	}

	player1ID, player2ID, ok, err := u.queue.PopPair(ctx)
	if err != nil {
		return nil, fmt.Errorf("MatchmakingUsecase - JoinQueue - MatchmakingQueue.PopPair: %w", err)
	}
	if !ok {
		return nil, nil
	}

	result, err := u.createMatch(ctx, player1ID, player2ID)
	if err != nil {
		_, _ = u.players.UpdateStatus(ctx, player1ID, domain.PlayerStatusIdle)
		_, _ = u.players.UpdateStatus(ctx, player2ID, domain.PlayerStatusIdle)
		return nil, err
	}
	return result, nil
}

func (u *MatchmakingUsecase) LeaveQueue(ctx context.Context, playerID uuid.UUID) error {
	if err := u.queue.Remove(ctx, playerID); err != nil {
		return fmt.Errorf("MatchmakingUsecase - LeaveQueue - MatchmakingQueue.Remove: %w", err)
	}

	player, err := u.players.GetByID(ctx, playerID)
	if err != nil {
		return fmt.Errorf("MatchmakingUsecase - LeaveQueue - PlayerRepo.GetByID: %w", err)
	}
	if player.Status == domain.PlayerStatusInDuel {
		return nil
	}
	if _, err := u.players.UpdateStatus(ctx, playerID, domain.PlayerStatusIdle); err != nil {
		return fmt.Errorf("MatchmakingUsecase - LeaveQueue - PlayerRepo.UpdateStatus idle: %w", err)
	}
	return nil
}

func (u *MatchmakingUsecase) createMatch(ctx context.Context, player1ID, player2ID uuid.UUID) (*MatchResult, error) {
	var result *MatchResult
	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		player1, err := u.players.GetByID(txCtx, player1ID)
		if err != nil {
			return fmt.Errorf("MatchmakingUsecase - createMatch - PlayerRepo.GetByID player1: %w", err)
		}
		player2, err := u.players.GetByID(txCtx, player2ID)
		if err != nil {
			return fmt.Errorf("MatchmakingUsecase - createMatch - PlayerRepo.GetByID player2: %w", err)
		}
		if player1.Status == domain.PlayerStatusInDuel || player2.Status == domain.PlayerStatusInDuel {
			return apperr.ErrPlayerInDuel
		}

		player1Task, err := u.selectTaskForPlayer(txCtx, player1.ID)
		if err != nil {
			return fmt.Errorf("MatchmakingUsecase - createMatch - select player1 task: %w", err)
		}
		player2Task, err := u.selectTaskForPlayer(txCtx, player2.ID)
		if err != nil {
			return fmt.Errorf("MatchmakingUsecase - createMatch - select player2 task: %w", err)
		}

		deadline := u.clock.Now().Add(time.Duration(max(player1Task.TimeLimit, player2Task.TimeLimit)) * time.Second)
		duel, err := u.duels.Create(txCtx, player1.ID, player2.ID, deadline)
		if err != nil {
			return fmt.Errorf("MatchmakingUsecase - createMatch - DuelRepo.Create: %w", err)
		}
		if err := u.duels.CreateDuelPlayerTask(txCtx, duel.ID, player1.ID, player1Task.ID); err != nil {
			return fmt.Errorf("MatchmakingUsecase - createMatch - DuelRepo.CreateDuelPlayerTask player1: %w", err)
		}
		if err := u.duels.CreateDuelPlayerTask(txCtx, duel.ID, player2.ID, player2Task.ID); err != nil {
			return fmt.Errorf("MatchmakingUsecase - createMatch - DuelRepo.CreateDuelPlayerTask player2: %w", err)
		}
		if _, err := u.players.UpdateStatus(txCtx, player1.ID, domain.PlayerStatusInDuel); err != nil {
			return fmt.Errorf("MatchmakingUsecase - createMatch - PlayerRepo.UpdateStatus player1: %w", err)
		}
		if _, err := u.players.UpdateStatus(txCtx, player2.ID, domain.PlayerStatusInDuel); err != nil {
			return fmt.Errorf("MatchmakingUsecase - createMatch - PlayerRepo.UpdateStatus player2: %w", err)
		}

		result = &MatchResult{
			Duel:        duel,
			Player1Task: player1Task,
			Player2Task: player2Task,
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func (u *MatchmakingUsecase) selectTaskForPlayer(ctx context.Context, playerID uuid.UUID) (*domain.Task, error) {
	difficulties, err := u.unlockedDifficulties(ctx, playerID)
	if err != nil {
		return nil, err
	}

	for i := len(difficulties) - 1; i >= 0; i-- {
		difficulty := difficulties[i]
		task, err := u.history.SelectUnsolvedTaskByDifficulty(ctx, playerID, difficulty)
		if err == nil {
			return task, nil
		}
		if !errors.Is(err, apperr.ErrTaskNotFound) {
			return nil, fmt.Errorf("HistoryRepo.SelectUnsolvedTaskByDifficulty(%s): %w", difficulty, err)
		}

		task, err = u.history.SelectAnyTaskByDifficulty(ctx, difficulty)
		if err == nil {
			return task, nil
		}
		if !errors.Is(err, apperr.ErrTaskNotFound) {
			return nil, fmt.Errorf("HistoryRepo.SelectAnyTaskByDifficulty(%s): %w", difficulty, err)
		}
	}
	return nil, apperr.ErrTaskNotFound
}

func (u *MatchmakingUsecase) unlockedDifficulties(ctx context.Context, playerID uuid.UUID) ([]domain.Difficulty, error) {
	out := []domain.Difficulty{domain.DifficultyEasy}
	if ok, err := u.completedDifficulty(ctx, playerID, domain.DifficultyEasy); err != nil || !ok {
		return out, err
	}
	out = append(out, domain.DifficultyMedium)
	if ok, err := u.completedDifficulty(ctx, playerID, domain.DifficultyMedium); err != nil || !ok {
		return out, err
	}
	out = append(out, domain.DifficultyHard)
	return out, nil
}

func (u *MatchmakingUsecase) completedDifficulty(ctx context.Context, playerID uuid.UUID, difficulty domain.Difficulty) (bool, error) {
	total, err := u.tasks.CountByDifficulty(ctx, difficulty)
	if err != nil {
		return false, fmt.Errorf("TaskRepo.CountByDifficulty(%s): %w", difficulty, err)
	}
	if total == 0 {
		return false, nil
	}
	solved, err := u.tasks.CountSolvedByDifficulty(ctx, playerID, difficulty)
	if err != nil {
		return false, fmt.Errorf("TaskRepo.CountSolvedByDifficulty(%s): %w", difficulty, err)
	}
	return solved >= total, nil
}
