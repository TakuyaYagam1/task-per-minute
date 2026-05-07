package duel

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
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
	storage usecase.SourceFileStorage
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
	storage usecase.SourceFileStorage,
	clk clock.Clock,
) *MatchmakingUsecase {
	return &MatchmakingUsecase{
		tx:      tx,
		queue:   queue,
		players: players,
		tasks:   tasks,
		history: history,
		duels:   duels,
		storage: storage,
		clock:   clk,
	}
}

func (u *MatchmakingUsecase) JoinQueue(ctx context.Context, playerID uuid.UUID) (*MatchResult, error) {
	if err := u.ensureQueuedForJoin(ctx, playerID); err != nil {
		return nil, err
	}

	if err := u.queue.Enqueue(ctx, playerID); err != nil {
		if releaseErr := u.releaseQueuedPlayers(ctx, playerID); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
		return nil, fmt.Errorf("MatchmakingUsecase - JoinQueue - MatchmakingQueue.Enqueue: %w", err)
	}

	for {
		player1ID, player2ID, ok, err := u.queue.PopPair(ctx)
		if err != nil {
			return nil, fmt.Errorf("MatchmakingUsecase - JoinQueue - MatchmakingQueue.PopPair: %w", err)
		}
		if !ok {
			return nil, nil
		}

		result, requeue, err := u.createMatch(ctx, player1ID, player2ID)
		if len(requeue) > 0 {
			if requeueErr := u.requeuePlayers(ctx, requeue...); requeueErr != nil {
				return nil, requeueErr
			}
		}
		if err != nil {
			if releaseErr := u.releaseQueuedPlayers(ctx, player1ID, player2ID); releaseErr != nil {
				err = errors.Join(err, releaseErr)
			}
			return nil, err
		}
		if result == nil {
			continue
		}
		return result, nil
	}
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
	if player.Status != domain.PlayerStatusQueued {
		return nil
	}
	if _, _, err := u.players.UpdateStatusIfCurrent(ctx, playerID, domain.PlayerStatusQueued, domain.PlayerStatusIdle); err != nil {
		return fmt.Errorf("MatchmakingUsecase - LeaveQueue - PlayerRepo.UpdateStatusIfCurrent idle: %w", err)
	}
	return nil
}

func (u *MatchmakingUsecase) ensureQueuedForJoin(ctx context.Context, playerID uuid.UUID) error {
	for attempt := 0; attempt < 2; attempt++ {
		player, err := u.players.GetByID(ctx, playerID)
		if err != nil {
			return fmt.Errorf("MatchmakingUsecase - ensureQueuedForJoin - PlayerRepo.GetByID: %w", err)
		}
		switch player.Status {
		case domain.PlayerStatusInDuel:
			return apperr.ErrPlayerInDuel
		case domain.PlayerStatusQueued:
			return nil
		}

		if _, ok, err := u.players.UpdateStatusIfCurrent(ctx, playerID, player.Status, domain.PlayerStatusQueued); err != nil {
			return fmt.Errorf("MatchmakingUsecase - ensureQueuedForJoin - PlayerRepo.UpdateStatusIfCurrent queued: %w", err)
		} else if ok {
			return nil
		}
	}
	return apperr.ErrConflict
}

func (u *MatchmakingUsecase) createMatch(ctx context.Context, player1ID, player2ID uuid.UUID) (*MatchResult, []uuid.UUID, error) {
	var result *MatchResult
	var requeue []uuid.UUID
	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		player1, player2, queuedRequeue, err := u.claimQueuedPair(txCtx, player1ID, player2ID)
		if err != nil {
			return err
		}
		if len(queuedRequeue) > 0 {
			requeue = queuedRequeue
			return nil
		}

		player1Task, player2Task, err := u.selectPreparedTasks(txCtx, player1.ID, player2.ID)
		if err != nil {
			return err
		}
		duel, err := u.createDuelAssignments(txCtx, player1, player2, player1Task, player2Task)
		if err != nil {
			return err
		}

		result = &MatchResult{
			Duel:        duel,
			Player1Task: player1Task,
			Player2Task: player2Task,
		}
		return nil
	}); err != nil {
		return nil, nil, err
	}
	return result, requeue, nil
}

func (u *MatchmakingUsecase) claimQueuedPair(
	ctx context.Context,
	player1ID uuid.UUID,
	player2ID uuid.UUID,
) (*domain.Player, *domain.Player, []uuid.UUID, error) {
	player1, err := u.players.GetByID(ctx, player1ID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("MatchmakingUsecase - claimQueuedPair - PlayerRepo.GetByID player1: %w", err)
	}
	player2, err := u.players.GetByID(ctx, player2ID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("MatchmakingUsecase - claimQueuedPair - PlayerRepo.GetByID player2: %w", err)
	}
	if player1.Status != domain.PlayerStatusQueued || player2.Status != domain.PlayerStatusQueued {
		return nil, nil, queuedPlayerIDs(player1, player2), nil
	}

	if _, ok, err := u.players.UpdateStatusIfCurrent(ctx, player1.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel); err != nil {
		return nil, nil, nil, fmt.Errorf("MatchmakingUsecase - claimQueuedPair - PlayerRepo.UpdateStatusIfCurrent player1 in_duel: %w", err)
	} else if !ok {
		return nil, nil, []uuid.UUID{player2.ID}, nil
	}
	if _, ok, err := u.players.UpdateStatusIfCurrent(ctx, player2.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel); err != nil {
		return nil, nil, nil, fmt.Errorf("MatchmakingUsecase - claimQueuedPair - PlayerRepo.UpdateStatusIfCurrent player2 in_duel: %w", err)
	} else if !ok {
		if err := u.rollbackClaimedPlayer(ctx, player1.ID); err != nil {
			return nil, nil, nil, err
		}
		return nil, nil, []uuid.UUID{player1.ID}, nil
	}
	return player1, player2, nil, nil
}

func (u *MatchmakingUsecase) rollbackClaimedPlayer(ctx context.Context, playerID uuid.UUID) error {
	if _, ok, err := u.players.UpdateStatusIfCurrent(ctx, playerID, domain.PlayerStatusInDuel, domain.PlayerStatusQueued); err != nil {
		return fmt.Errorf("MatchmakingUsecase - rollbackClaimedPlayer - PlayerRepo.UpdateStatusIfCurrent queued: %w", err)
	} else if !ok {
		return fmt.Errorf("MatchmakingUsecase - rollbackClaimedPlayer - stale player status: %w", apperr.ErrConflict)
	}
	return nil
}

func (u *MatchmakingUsecase) selectPreparedTasks(
	ctx context.Context,
	player1ID uuid.UUID,
	player2ID uuid.UUID,
) (*domain.Task, *domain.Task, error) {
	player1Task, player2Task, err := u.selectTasksForPair(ctx, player1ID, player2ID)
	if err != nil {
		return nil, nil, fmt.Errorf("MatchmakingUsecase - selectPreparedTasks - select pair tasks: %w", err)
	}
	player1Task, err = u.prepareAssignedTask(ctx, player1Task)
	if err != nil {
		return nil, nil, fmt.Errorf("MatchmakingUsecase - selectPreparedTasks - prepare player1 task: %w", err)
	}
	player2Task, err = u.prepareAssignedTask(ctx, player2Task)
	if err != nil {
		return nil, nil, fmt.Errorf("MatchmakingUsecase - selectPreparedTasks - prepare player2 task: %w", err)
	}
	return player1Task, player2Task, nil
}

func (u *MatchmakingUsecase) createDuelAssignments(
	ctx context.Context,
	player1 *domain.Player,
	player2 *domain.Player,
	player1Task *domain.Task,
	player2Task *domain.Task,
) (*domain.Duel, error) {
	deadline := u.clock.Now().Add(time.Duration(max(player1Task.TimeLimit, player2Task.TimeLimit)) * time.Second)
	duel, err := u.duels.Create(ctx, player1.ID, player2.ID, deadline)
	if err != nil {
		return nil, fmt.Errorf("MatchmakingUsecase - createDuelAssignments - DuelRepo.Create: %w", err)
	}
	if err := u.duels.CreateDuelPlayerTask(ctx, duel.ID, player1.ID, player1Task.ID); err != nil {
		return nil, fmt.Errorf("MatchmakingUsecase - createDuelAssignments - DuelRepo.CreateDuelPlayerTask player1: %w", err)
	}
	if err := u.duels.CreateDuelPlayerTask(ctx, duel.ID, player2.ID, player2Task.ID); err != nil {
		return nil, fmt.Errorf("MatchmakingUsecase - createDuelAssignments - DuelRepo.CreateDuelPlayerTask player2: %w", err)
	}
	return duel, nil
}

func queuedPlayerIDs(players ...*domain.Player) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(players))
	for _, player := range players {
		if player != nil && player.Status == domain.PlayerStatusQueued {
			out = append(out, player.ID)
		}
	}
	return out
}

func (u *MatchmakingUsecase) requeuePlayers(ctx context.Context, playerIDs ...uuid.UUID) error {
	for _, playerID := range playerIDs {
		if err := u.queue.Enqueue(ctx, playerID); err != nil {
			return fmt.Errorf("MatchmakingUsecase - requeuePlayers - MatchmakingQueue.Enqueue: %w", err)
		}
	}
	return nil
}

func (u *MatchmakingUsecase) releaseQueuedPlayers(ctx context.Context, playerIDs ...uuid.UUID) error {
	for _, playerID := range playerIDs {
		if _, _, err := u.players.UpdateStatusIfCurrent(ctx, playerID, domain.PlayerStatusQueued, domain.PlayerStatusIdle); err != nil {
			return fmt.Errorf("MatchmakingUsecase - releaseQueuedPlayers - PlayerRepo.UpdateStatusIfCurrent idle: %w", err)
		}
	}
	return nil
}

func (u *MatchmakingUsecase) prepareAssignedTask(ctx context.Context, task *domain.Task) (*domain.Task, error) {
	if task == nil || task.SourceFileURL == nil {
		return task, nil
	}
	if u.storage == nil {
		return nil, apperr.ErrInternal
	}

	url, err := u.storage.PresignedGetURL(
		ctx,
		domain.TaskSourceFileKeyFromURL(task.ID, *task.SourceFileURL),
		time.Duration(task.TimeLimit)*time.Second,
	)
	if err != nil {
		return nil, fmt.Errorf("SourceFileStorage.PresignedGetURL: %w", err)
	}

	out := *task
	out.Hints = append([]string(nil), task.Hints...)
	out.SourceFileURL = &url
	return &out, nil
}

func (u *MatchmakingUsecase) selectDifficultyForPlayer(ctx context.Context, playerID uuid.UUID) (domain.Difficulty, error) {
	difficulties, err := u.unlockedDifficulties(ctx, playerID)
	if err != nil {
		return "", err
	}
	for i := len(difficulties) - 1; i >= 0; i-- {
		difficulty := difficulties[i]
		tasks, err := u.tasks.ListByDifficulty(ctx, difficulty)
		if err != nil {
			return "", fmt.Errorf("TaskRepo.ListByDifficulty(%s): %w", difficulty, err)
		}
		if len(tasks) > 0 {
			return difficulty, nil
		}
	}
	return "", apperr.ErrTaskNotFound
}

func (u *MatchmakingUsecase) selectTasksForPair(
	ctx context.Context,
	player1ID uuid.UUID,
	player2ID uuid.UUID,
) (*domain.Task, *domain.Task, error) {
	player1Difficulty, err := u.selectDifficultyForPlayer(ctx, player1ID)
	if err != nil {
		return nil, nil, fmt.Errorf("select player1 difficulty: %w", err)
	}
	player2Difficulty, err := u.selectDifficultyForPlayer(ctx, player2ID)
	if err != nil {
		return nil, nil, fmt.Errorf("select player2 difficulty: %w", err)
	}

	if player1Difficulty != player2Difficulty {
		player1Task, err := u.selectTaskForPlayerInDifficulty(ctx, player1ID, player1Difficulty)
		if err != nil {
			return nil, nil, fmt.Errorf("select player1 task: %w", err)
		}
		player2Task, err := u.selectTaskForPlayerInDifficulty(ctx, player2ID, player2Difficulty)
		if err != nil {
			return nil, nil, fmt.Errorf("select player2 task: %w", err)
		}
		return player1Task, player2Task, nil
	}

	return u.selectPairTasksInDifficulty(ctx, player1ID, player2ID, player1Difficulty)
}

func (u *MatchmakingUsecase) selectTaskForPlayerInDifficulty(
	ctx context.Context,
	playerID uuid.UUID,
	difficulty domain.Difficulty,
) (*domain.Task, error) {
	tasks, err := u.tasks.ListByDifficulty(ctx, difficulty)
	if err != nil {
		return nil, fmt.Errorf("TaskRepo.ListByDifficulty(%s): %w", difficulty, err)
	}
	if len(tasks) == 0 {
		return nil, apperr.ErrTaskNotFound
	}

	solved, err := u.solvedTaskSet(ctx, playerID)
	if err != nil {
		return nil, err
	}
	unsolved := filterTasks(tasks, func(task *domain.Task) bool {
		_, ok := solved[task.ID]
		return !ok
	})
	if len(unsolved) > 0 {
		return randomTask(unsolved), nil
	}
	return randomTask(tasks), nil
}

func (u *MatchmakingUsecase) selectPairTasksInDifficulty(
	ctx context.Context,
	player1ID uuid.UUID,
	player2ID uuid.UUID,
	difficulty domain.Difficulty,
) (*domain.Task, *domain.Task, error) {
	tasks, err := u.tasks.ListByDifficulty(ctx, difficulty)
	if err != nil {
		return nil, nil, fmt.Errorf("TaskRepo.ListByDifficulty(%s): %w", difficulty, err)
	}
	if len(tasks) == 0 {
		return nil, nil, apperr.ErrTaskNotFound
	}

	player1Solved, err := u.solvedTaskSet(ctx, player1ID)
	if err != nil {
		return nil, nil, fmt.Errorf("player1 solved tasks: %w", err)
	}
	player2Solved, err := u.solvedTaskSet(ctx, player2ID)
	if err != nil {
		return nil, nil, fmt.Errorf("player2 solved tasks: %w", err)
	}

	unsolvedByBoth := filterTasks(tasks, func(task *domain.Task) bool {
		_, solvedByPlayer1 := player1Solved[task.ID]
		_, solvedByPlayer2 := player2Solved[task.ID]
		return !solvedByPlayer1 && !solvedByPlayer2
	})
	if len(unsolvedByBoth) >= 2 {
		shuffled := shuffledTasks(unsolvedByBoth)
		return shuffled[0], shuffled[1], nil
	}

	player1Unsolved := filterTasks(tasks, func(task *domain.Task) bool {
		_, ok := player1Solved[task.ID]
		return !ok
	})
	player2Unsolved := filterTasks(tasks, func(task *domain.Task) bool {
		_, ok := player2Solved[task.ID]
		return !ok
	})
	if player1Task, player2Task, ok := selectDistinctTasks(player1Unsolved, player2Unsolved, false); ok {
		return player1Task, player2Task, nil
	}
	if player1Task, player2Task, ok := selectDistinctTasks(tasks, tasks, len(tasks) == 1); ok {
		return player1Task, player2Task, nil
	}
	return nil, nil, apperr.ErrTaskNotFound
}

func (u *MatchmakingUsecase) solvedTaskSet(ctx context.Context, playerID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	ids, err := u.history.ListSolvedTaskIDs(ctx, playerID)
	if err != nil {
		return nil, fmt.Errorf("HistoryRepo.ListSolvedTaskIDs: %w", err)
	}
	out := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out, nil
}

func filterTasks(tasks []*domain.Task, keep func(*domain.Task) bool) []*domain.Task {
	out := make([]*domain.Task, 0, len(tasks))
	for _, task := range tasks {
		if task != nil && keep(task) {
			out = append(out, task)
		}
	}
	return out
}

func randomTask(tasks []*domain.Task) *domain.Task {
	if len(tasks) == 0 {
		return nil
	}
	shuffled := shuffledTasks(tasks)
	return shuffled[0]
}

func selectDistinctTasks(
	first []*domain.Task,
	second []*domain.Task,
	allowDuplicate bool,
) (*domain.Task, *domain.Task, bool) {
	firstShuffled := shuffledTasks(first)
	secondShuffled := shuffledTasks(second)
	for _, firstTask := range firstShuffled {
		for _, secondTask := range secondShuffled {
			if firstTask.ID != secondTask.ID {
				return firstTask, secondTask, true
			}
		}
	}
	if allowDuplicate && len(firstShuffled) > 0 && len(secondShuffled) > 0 {
		return firstShuffled[0], secondShuffled[0], true
	}
	return nil, nil, false
}

func shuffledTasks(tasks []*domain.Task) []*domain.Task {
	out := append([]*domain.Task(nil), tasks...)

	rand.Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out
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
