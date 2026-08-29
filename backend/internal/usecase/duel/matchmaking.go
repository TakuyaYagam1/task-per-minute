package duel

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
	logkit "github.com/wahrwelt-kit/go-logkit"
)

type MatchmakingUseCase struct {
	tx      TransactionManager
	queue   MatchmakingQueue
	players MatchmakingPlayerRepository
	tasks   MatchmakingTaskRepository
	history MatchmakingHistoryRepository
	duels   MatchmakingDuelRepository
	storage SourceFileURLSigner
	clock   Clock
	log     logkit.Logger
}

// MatchmakingOption configures optional behavior on a MatchmakingUseCase.
type MatchmakingOption func(*MatchmakingUseCase)

// WithMatchmakingLogger attaches a structured logger so each matched pair
// emits a decision record (which branch was taken, which tasks were chosen)
// for production debugging.
func WithMatchmakingLogger(log logkit.Logger) MatchmakingOption {
	return func(u *MatchmakingUseCase) {
		u.log = log
	}
}

func NewMatchmakingUseCase(
	tx TransactionManager,
	queue MatchmakingQueue,
	players MatchmakingPlayerRepository,
	tasks MatchmakingTaskRepository,
	history MatchmakingHistoryRepository,
	duels MatchmakingDuelRepository,
	storage SourceFileURLSigner,
	clk Clock,
) *MatchmakingUseCase {
	return &MatchmakingUseCase{
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

func (u *MatchmakingUseCase) Configure(options ...MatchmakingOption) *MatchmakingUseCase {
	for _, opt := range options {
		if opt != nil {
			opt(u)
		}
	}
	return u
}

func (u *MatchmakingUseCase) JoinQueue(ctx context.Context, playerID uuid.UUID) (*MatchResult, error) {
	reservation, err := u.ensureQueuedForJoin(ctx, playerID)
	if err != nil {
		return nil, err
	}

	if err := u.queue.Enqueue(ctx, playerID); err != nil {
		if releaseErr := u.releaseQueuedReservation(ctx, playerID, reservation); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
		return nil, fmt.Errorf("MatchmakingUsecase - JoinQueue - MatchmakingQueue.Enqueue: %w", err)
	}

	for {
		player1ID, player2ID, ok, err := u.queue.PopPair(ctx)
		if err != nil {
			if rollbackErr := u.rollbackQueuedJoin(ctx, playerID, reservation); rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
			return nil, fmt.Errorf("MatchmakingUsecase - JoinQueue - MatchmakingQueue.PopPair: %w", err)
		}
		if !ok {
			return nil, nil
		}

		result, requeue, err := u.createMatch(ctx, player1ID, player2ID)
		if len(requeue) > 0 {
			if requeueErr := u.requeuePlayers(ctx, requeue...); requeueErr != nil {
				if releaseErr := u.releaseQueuedPlayers(ctx, requeue...); releaseErr != nil {
					requeueErr = errors.Join(requeueErr, releaseErr)
				}
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

func (u *MatchmakingUseCase) LeaveQueue(ctx context.Context, playerID uuid.UUID) error {
	reservation, err := u.players.GetParticipantReservation(ctx, playerID)
	if err != nil {
		return fmt.Errorf("MatchmakingUsecase - LeaveQueue - PlayerRepo.GetParticipantReservation: %w", err)
	}
	if reservation != nil && !isCasualQueueReservation(reservation, playerID) {
		player, getErr := u.players.GetByID(ctx, playerID)
		if getErr != nil {
			return fmt.Errorf("MatchmakingUsecase - LeaveQueue - PlayerRepo.GetByID: %w", getErr)
		}
		if player.Status == domain.PlayerStatusInDuel &&
			reservation.OwnerKind == domain.ParticipantReservationOwnerCasualDuel {
			return nil
		}
		return domain.ErrPlayerReserved
	}
	if err := u.queue.Remove(ctx, playerID); err != nil {
		return fmt.Errorf("MatchmakingUsecase - LeaveQueue - MatchmakingQueue.Remove: %w", err)
	}
	return u.releaseQueuedReservation(ctx, playerID, reservation)
}

func (u *MatchmakingUseCase) rollbackQueuedJoin(
	ctx context.Context,
	playerID uuid.UUID,
	reservation *domain.ParticipantReservation,
) error {
	var errs []error
	if err := u.queue.Remove(ctx, playerID); err != nil {
		errs = append(errs, fmt.Errorf("MatchmakingUsecase - rollbackQueuedJoin - MatchmakingQueue.Remove: %w", err))
	}
	if err := u.releaseQueuedReservation(ctx, playerID, reservation); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (u *MatchmakingUseCase) ensureQueuedForJoin(
	ctx context.Context,
	playerID uuid.UUID,
) (*domain.ParticipantReservation, error) {
	for attempt := 0; attempt < 2; attempt++ {
		var reservation *domain.ParticipantReservation
		err := u.tx.Do(ctx, func(txCtx context.Context) error {
			player, err := u.players.GetByID(txCtx, playerID)
			if err != nil {
				return fmt.Errorf("MatchmakingUsecase - ensureQueuedForJoin - PlayerRepo.GetByID: %w", err)
			}
			if player.Status == domain.PlayerStatusInDuel {
				return domain.ErrPlayerInDuel
			}
			reservation, _, err = u.players.AcquireParticipantReservation(
				txCtx,
				playerID,
				domain.ParticipantReservationOwnerCasualQueue,
				playerID,
				u.clock.Now(),
			)
			if err != nil {
				return fmt.Errorf("MatchmakingUsecase - ensureQueuedForJoin - acquire reservation: %w", err)
			}
			if player.Status == domain.PlayerStatusQueued {
				return nil
			}
			if _, ok, updateErr := u.players.UpdateStatusIfCurrent(
				txCtx,
				playerID,
				player.Status,
				domain.PlayerStatusQueued,
			); updateErr != nil {
				return fmt.Errorf("MatchmakingUsecase - ensureQueuedForJoin - PlayerRepo.UpdateStatusIfCurrent queued: %w", updateErr)
			} else if !ok {
				return domain.ErrConflict
			}
			return nil
		})
		if err == nil {
			return reservation, nil
		}
		if !errors.Is(err, domain.ErrConflict) {
			return nil, err
		}
	}
	return nil, domain.ErrConflict
}

func (u *MatchmakingUseCase) createMatch(ctx context.Context, player1ID, player2ID uuid.UUID) (*MatchResult, []uuid.UUID, error) {
	var result *MatchResult
	var requeue []uuid.UUID
	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		player1, player2, reservation1, reservation2, queuedRequeue, err := u.claimQueuedPair(
			txCtx,
			player1ID,
			player2ID,
		)
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
		if err := u.promotePairReservations(txCtx, reservation1, reservation2, duel.ID); err != nil {
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

func (u *MatchmakingUseCase) claimQueuedPair(
	ctx context.Context,
	player1ID uuid.UUID,
	player2ID uuid.UUID,
) (
	*domain.Player,
	*domain.Player,
	*domain.ParticipantReservation,
	*domain.ParticipantReservation,
	[]uuid.UUID,
	error,
) {
	player1, err := u.players.GetByID(ctx, player1ID)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("MatchmakingUsecase - claimQueuedPair - PlayerRepo.GetByID player1: %w", err)
	}
	player2, err := u.players.GetByID(ctx, player2ID)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("MatchmakingUsecase - claimQueuedPair - PlayerRepo.GetByID player2: %w", err)
	}
	reservation1, err := u.players.GetParticipantReservation(ctx, player1ID)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("MatchmakingUsecase - claimQueuedPair - reservation player1: %w", err)
	}
	reservation2, err := u.players.GetParticipantReservation(ctx, player2ID)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("MatchmakingUsecase - claimQueuedPair - reservation player2: %w", err)
	}
	valid1 := player1.Status == domain.PlayerStatusQueued && isCasualQueueReservation(reservation1, player1ID)
	valid2 := player2.Status == domain.PlayerStatusQueued && isCasualQueueReservation(reservation2, player2ID)
	if !valid1 || !valid2 {
		return nil, nil, nil, nil, queuedReservationPlayerIDs(
			player1,
			reservation1,
			player2,
			reservation2,
		), nil
	}

	if _, ok, err := u.players.UpdateStatusIfCurrent(ctx, player1.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("MatchmakingUsecase - claimQueuedPair - PlayerRepo.UpdateStatusIfCurrent player1 in_duel: %w", err)
	} else if !ok {
		return nil, nil, nil, nil, []uuid.UUID{player2.ID}, nil
	}
	if _, ok, err := u.players.UpdateStatusIfCurrent(ctx, player2.ID, domain.PlayerStatusQueued, domain.PlayerStatusInDuel); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("MatchmakingUsecase - claimQueuedPair - PlayerRepo.UpdateStatusIfCurrent player2 in_duel: %w", err)
	} else if !ok {
		if err := u.rollbackClaimedPlayer(ctx, player1.ID); err != nil {
			return nil, nil, nil, nil, nil, err
		}
		return nil, nil, nil, nil, []uuid.UUID{player1.ID}, nil
	}
	return player1, player2, reservation1, reservation2, nil, nil
}

func (u *MatchmakingUseCase) rollbackClaimedPlayer(ctx context.Context, playerID uuid.UUID) error {
	if _, ok, err := u.players.UpdateStatusIfCurrent(ctx, playerID, domain.PlayerStatusInDuel, domain.PlayerStatusQueued); err != nil {
		return fmt.Errorf("MatchmakingUsecase - rollbackClaimedPlayer - PlayerRepo.UpdateStatusIfCurrent queued: %w", err)
	} else if !ok {
		return fmt.Errorf("MatchmakingUsecase - rollbackClaimedPlayer - stale player status: %w", domain.ErrConflict)
	}
	return nil
}

func (u *MatchmakingUseCase) selectPreparedTasks(
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

func (u *MatchmakingUseCase) createDuelAssignments(
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

func (u *MatchmakingUseCase) requeuePlayers(ctx context.Context, playerIDs ...uuid.UUID) error {
	for _, playerID := range playerIDs {
		if err := u.queue.Enqueue(ctx, playerID); err != nil {
			return fmt.Errorf("MatchmakingUsecase - requeuePlayers - MatchmakingQueue.Enqueue: %w", err)
		}
	}
	return nil
}

func (u *MatchmakingUseCase) releaseQueuedPlayers(ctx context.Context, playerIDs ...uuid.UUID) error {
	return u.tx.Do(ctx, func(txCtx context.Context) error {
		for _, playerID := range playerIDs {
			reservation, err := u.players.GetParticipantReservation(txCtx, playerID)
			if err != nil {
				return fmt.Errorf("MatchmakingUsecase - releaseQueuedPlayers - get reservation: %w", err)
			}
			if reservation != nil && !isCasualQueueReservation(reservation, playerID) {
				return domain.ErrPlayerReserved
			}
			if err := u.releaseQueuedReservationInTx(txCtx, playerID, reservation); err != nil {
				return err
			}
		}
		return nil
	})
}

func (u *MatchmakingUseCase) releaseQueuedReservation(
	ctx context.Context,
	playerID uuid.UUID,
	reservation *domain.ParticipantReservation,
) error {
	return u.tx.Do(ctx, func(txCtx context.Context) error {
		return u.releaseQueuedReservationInTx(txCtx, playerID, reservation)
	})
}

func (u *MatchmakingUseCase) releaseQueuedReservationInTx(
	ctx context.Context,
	playerID uuid.UUID,
	reservation *domain.ParticipantReservation,
) error {
	if reservation != nil {
		if !isCasualQueueReservation(reservation, playerID) {
			return domain.ErrPlayerReserved
		}
		if _, err := u.players.ReleaseParticipantReservation(ctx, *reservation); err != nil {
			return fmt.Errorf("MatchmakingUsecase - release queue reservation: %w", err)
		}
	}
	if _, changed, err := u.players.UpdateStatusIfCurrent(
		ctx,
		playerID,
		domain.PlayerStatusQueued,
		domain.PlayerStatusIdle,
	); err != nil {
		return fmt.Errorf("MatchmakingUsecase - release queue status: %w", err)
	} else if changed {
		return nil
	}
	player, err := u.players.GetByID(ctx, playerID)
	if err != nil {
		return fmt.Errorf("MatchmakingUsecase - release queue status lookup: %w", err)
	}
	if player.Status == domain.PlayerStatusIdle {
		return nil
	}
	return domain.ErrConflict
}

func (u *MatchmakingUseCase) promotePairReservations(
	ctx context.Context,
	first *domain.ParticipantReservation,
	second *domain.ParticipantReservation,
	duelID uuid.UUID,
) error {
	for _, reservation := range []*domain.ParticipantReservation{first, second} {
		if reservation == nil || !isCasualQueueReservation(reservation, reservation.PlayerID) {
			return domain.ErrPlayerReserved
		}
		promoted, _, err := u.players.PromoteParticipantReservation(
			ctx,
			*reservation,
			domain.ParticipantReservationOwnerCasualDuel,
			duelID,
			u.clock.Now(),
		)
		if err != nil {
			return fmt.Errorf("MatchmakingUsecase - promote reservation: %w", err)
		}
		if promoted == nil || promoted.OwnerKind != domain.ParticipantReservationOwnerCasualDuel ||
			promoted.OwnerID != duelID {
			return domain.ErrConflict
		}
	}
	return nil
}

func isCasualQueueReservation(reservation *domain.ParticipantReservation, playerID uuid.UUID) bool {
	return reservation != nil && reservation.IsValid() && reservation.PlayerID == playerID &&
		reservation.OwnerKind == domain.ParticipantReservationOwnerCasualQueue &&
		reservation.OwnerID == playerID
}

func queuedReservationPlayerIDs(
	firstPlayer *domain.Player,
	firstReservation *domain.ParticipantReservation,
	secondPlayer *domain.Player,
	secondReservation *domain.ParticipantReservation,
) []uuid.UUID {
	out := make([]uuid.UUID, 0, 2)
	for _, value := range []struct {
		player      *domain.Player
		reservation *domain.ParticipantReservation
	}{
		{player: firstPlayer, reservation: firstReservation},
		{player: secondPlayer, reservation: secondReservation},
	} {
		if value.player != nil && value.player.Status == domain.PlayerStatusQueued &&
			isCasualQueueReservation(value.reservation, value.player.ID) {
			out = append(out, value.player.ID)
		}
	}
	return out
}

func (u *MatchmakingUseCase) prepareAssignedTask(ctx context.Context, task *domain.Task) (*domain.Task, error) {
	if task == nil || task.SourceFileURL == nil {
		return task, nil
	}
	if u.storage == nil {
		return nil, domain.ErrInternal
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

func (u *MatchmakingUseCase) selectDifficultyForPlayer(ctx context.Context, playerID uuid.UUID) (domain.Difficulty, error) {
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
	return "", domain.ErrTaskNotFound
}

func (u *MatchmakingUseCase) selectTasksForPair(
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
		u.logDecision(matchmakingDecisionFields{
			Branch:            "cross_pool",
			Player1ID:         player1ID,
			Player2ID:         player2ID,
			Player1Difficulty: player1Difficulty,
			Player2Difficulty: player2Difficulty,
			Player1Task:       player1Task,
			Player2Task:       player2Task,
		})
		return player1Task, player2Task, nil
	}

	return u.selectPairTasksInDifficulty(ctx, player1ID, player2ID, player1Difficulty)
}

func (u *MatchmakingUseCase) selectTaskForPlayerInDifficulty(
	ctx context.Context,
	playerID uuid.UUID,
	difficulty domain.Difficulty,
) (*domain.Task, error) {
	tasks, err := u.tasks.ListByDifficulty(ctx, difficulty)
	if err != nil {
		return nil, fmt.Errorf("TaskRepo.ListByDifficulty(%s): %w", difficulty, err)
	}
	if len(tasks) == 0 {
		return nil, domain.ErrTaskNotFound
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

func (u *MatchmakingUseCase) selectPairTasksInDifficulty(
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
		return nil, nil, domain.ErrTaskNotFound
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

	decision := matchmakingDecisionFields{
		Player1ID:           player1ID,
		Player2ID:           player2ID,
		Player1Difficulty:   difficulty,
		Player2Difficulty:   difficulty,
		PoolSize:            len(tasks),
		Player1SolvedCount:  len(player1Solved),
		Player2SolvedCount:  len(player2Solved),
		UnsolvedByBothCount: len(unsolvedByBoth),
	}

	if len(unsolvedByBoth) == 1 {
		decision.Branch = "same_pool_shared_one"
		decision.Player1Task = unsolvedByBoth[0]
		decision.Player2Task = unsolvedByBoth[0]
		u.logDecision(decision)
		return unsolvedByBoth[0], unsolvedByBoth[0], nil
	}
	if len(unsolvedByBoth) >= 2 {
		task := randomTask(unsolvedByBoth)
		decision.Branch = "same_pool_shared_many"
		decision.Player1Task = task
		decision.Player2Task = task
		u.logDecision(decision)
		return task, task, nil
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
		decision.Branch = "same_pool_swap"
		decision.Player1Task = player1Task
		decision.Player2Task = player2Task
		u.logDecision(decision)
		return player1Task, player2Task, nil
	}
	if player1Task, player2Task, ok := selectDistinctTasks(tasks, tasks, len(tasks) == 1); ok {
		decision.Branch = "same_pool_fallback"
		decision.Player1Task = player1Task
		decision.Player2Task = player2Task
		u.logDecision(decision)
		return player1Task, player2Task, nil
	}
	return nil, nil, domain.ErrTaskNotFound
}

// matchmakingDecisionFields captures the inputs and outputs of a single
// matchmaking task assignment. Used to emit one structured log line per
// matched pair so prod issues can be diagnosed without re-running the flow.
type matchmakingDecisionFields struct {
	Branch              string
	Player1ID           uuid.UUID
	Player2ID           uuid.UUID
	Player1Difficulty   domain.Difficulty
	Player2Difficulty   domain.Difficulty
	PoolSize            int
	Player1SolvedCount  int
	Player2SolvedCount  int
	UnsolvedByBothCount int
	Player1Task         *domain.Task
	Player2Task         *domain.Task
}

func (u *MatchmakingUseCase) logDecision(d matchmakingDecisionFields) {
	if u.log == nil {
		return
	}
	fields := logkit.Fields{
		"branch_taken":           d.Branch,
		"player1_id":             d.Player1ID.String(),
		"player2_id":             d.Player2ID.String(),
		"player1_difficulty":     string(d.Player1Difficulty),
		"player2_difficulty":     string(d.Player2Difficulty),
		"pool_size":              d.PoolSize,
		"player1_solved_count":   d.Player1SolvedCount,
		"player2_solved_count":   d.Player2SolvedCount,
		"unsolved_by_both_count": d.UnsolvedByBothCount,
	}
	if d.Player1Task != nil {
		fields["player1_task_id"] = d.Player1Task.ID.String()
		fields["player1_task_title"] = d.Player1Task.Title
		fields["player1_task_difficulty"] = string(d.Player1Task.Difficulty)
	}
	if d.Player2Task != nil {
		fields["player2_task_id"] = d.Player2Task.ID.String()
		fields["player2_task_title"] = d.Player2Task.Title
		fields["player2_task_difficulty"] = string(d.Player2Task.Difficulty)
	}
	u.log.Info("matchmaking task selection", fields)
}

func (u *MatchmakingUseCase) solvedTaskSet(ctx context.Context, playerID uuid.UUID) (map[uuid.UUID]struct{}, error) {
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

func (u *MatchmakingUseCase) unlockedDifficulties(ctx context.Context, playerID uuid.UUID) ([]domain.Difficulty, error) {
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

func (u *MatchmakingUseCase) completedDifficulty(ctx context.Context, playerID uuid.UUID, difficulty domain.Difficulty) (bool, error) {
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
