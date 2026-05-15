package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

type activeDuelTimerStarter interface {
	StartDuelTimer(duel *domain.Duel)
}

type activeDuelHintStarter interface {
	StartDuel(duel *domain.Duel, assignments map[uuid.UUID]*domain.Task)
}

type StartupRecoverer struct {
	tx          usecase.TxManager
	duels       usecase.ActiveDuelRepo
	duelTasks   usecase.DuelRepo
	players     usecase.PlayerStatusRepo
	queued      usecase.QueuedPlayerResetter
	queue       usecase.MatchmakingQueueCleaner
	broadcaster usecase.DuelBroadcaster
	timers      activeDuelTimerStarter
	hints       activeDuelHintStarter
	clock       clock.Clock
	log         logkit.Logger
}

func NewStartupRecoverer(
	tx usecase.TxManager,
	duels usecase.ActiveDuelRepo,
	duelTasks usecase.DuelRepo,
	players usecase.PlayerStatusRepo,
	queued usecase.QueuedPlayerResetter,
	queue usecase.MatchmakingQueueCleaner,
	broadcaster usecase.DuelBroadcaster,
	timers activeDuelTimerStarter,
	hints activeDuelHintStarter,
	clk clock.Clock,
	log logkit.Logger,
) *StartupRecoverer {
	if clk == nil {
		clk = clock.Real{}
	}
	return &StartupRecoverer{
		tx:          tx,
		duels:       duels,
		duelTasks:   duelTasks,
		players:     players,
		queued:      queued,
		queue:       queue,
		broadcaster: broadcaster,
		timers:      timers,
		hints:       hints,
		clock:       clk,
		log:         log,
	}
}

func (r *StartupRecoverer) Recover(ctx context.Context) error {
	if r == nil {
		return nil
	}

	queuedReset, err := r.recoverQueue(ctx)
	if err != nil {
		return err
	}

	active, err := r.duels.ListActive(ctx)
	if err != nil {
		return fmt.Errorf("StartupRecoverer - Recover - ActiveDuelRepo.ListActive: %w", err)
	}

	now := r.clock.Now()
	finishedCount := 0
	rearmedCount := 0
	for _, duel := range active {
		finished, rearmed, err := r.recoverActiveDuel(ctx, duel, now)
		if err != nil {
			return err
		}
		if finished != nil {
			finishedCount++
			if r.broadcaster != nil {
				r.broadcaster.BroadcastDuelFinished(ctx, finished)
			}
		}
		if rearmed {
			rearmedCount++
		}
	}

	if r.log != nil {
		r.log.Info("startup recovery completed", logkit.Fields{
			"active_duels_finished": finishedCount,
			"active_duels_rearmed":  rearmedCount,
			"queued_players_reset":  queuedReset,
		})
	}
	return nil
}

func (r *StartupRecoverer) recoverActiveDuel(ctx context.Context, duel *domain.Duel, now time.Time) (*domain.Duel, bool, error) {
	if duel == nil || duel.Status != domain.DuelStatusActive {
		return nil, false, nil
	}
	if !duel.Deadline.After(now) {
		finished, err := r.finishExpiredDuel(ctx, duel, now)
		return finished, false, err
	}
	if err := r.rearmDuel(ctx, duel); err != nil {
		if !errors.Is(err, apperr.ErrNotDuelParticipant) {
			return nil, false, err
		}
		if r.log != nil {
			r.log.Warn("startup recovery finished inconsistent active duel", logkit.Fields{
				"duel_id": duel.ID.String(),
				"error":   err.Error(),
			})
		}
		finished, finishErr := r.finishExpiredDuel(ctx, duel, now)
		return finished, false, finishErr
	}
	return nil, true, nil
}

func (r *StartupRecoverer) recoverQueue(ctx context.Context) (int64, error) {
	if r.queue != nil {
		if err := r.queue.Clear(ctx); err != nil {
			return 0, fmt.Errorf("StartupRecoverer - recoverQueue - MatchmakingQueueCleaner.Clear: %w", err)
		}
	}
	if r.queued == nil {
		return 0, nil
	}
	reset, err := r.queued.ResetQueuedToIdle(ctx)
	if err != nil {
		return 0, fmt.Errorf("StartupRecoverer - recoverQueue - QueuedPlayerResetter.ResetQueuedToIdle: %w", err)
	}
	return reset, nil
}

func (r *StartupRecoverer) finishExpiredDuel(ctx context.Context, duel *domain.Duel, now time.Time) (*domain.Duel, error) {
	var finished *domain.Duel
	if err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		finished, err = r.duels.Finish(txCtx, duel.ID, nil, now, domain.DuelStatusFinished)
		if err != nil {
			return fmt.Errorf("StartupRecoverer - recoverDuel - ActiveDuelRepo.Finish: %w", err)
		}
		if _, err := r.players.UpdateStatus(txCtx, duel.Player1ID, domain.PlayerStatusIdle); err != nil {
			return fmt.Errorf("StartupRecoverer - recoverDuel - PlayerStatusRepo.UpdateStatus player1: %w", err)
		}
		if _, err := r.players.UpdateStatus(txCtx, duel.Player2ID, domain.PlayerStatusIdle); err != nil {
			return fmt.Errorf("StartupRecoverer - recoverDuel - PlayerStatusRepo.UpdateStatus player2: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return finished, nil
}

func (r *StartupRecoverer) rearmDuel(ctx context.Context, duel *domain.Duel) error {
	var player1Task, player2Task *domain.Task
	if err := r.tx.Do(ctx, func(txCtx context.Context) error {
		if _, err := r.players.UpdateStatus(txCtx, duel.Player1ID, domain.PlayerStatusInDuel); err != nil {
			return fmt.Errorf("StartupRecoverer - rearmDuel - PlayerStatusRepo.UpdateStatus player1: %w", err)
		}
		if _, err := r.players.UpdateStatus(txCtx, duel.Player2ID, domain.PlayerStatusInDuel); err != nil {
			return fmt.Errorf("StartupRecoverer - rearmDuel - PlayerStatusRepo.UpdateStatus player2: %w", err)
		}
		if r.duelTasks != nil {
			var err error
			player1Task, err = r.duelTasks.GetPlayerTask(txCtx, duel.ID, duel.Player1ID)
			if err != nil {
				return fmt.Errorf("StartupRecoverer - rearmDuel - DuelRepo.GetPlayerTask player1: %w", err)
			}
			player2Task, err = r.duelTasks.GetPlayerTask(txCtx, duel.ID, duel.Player2ID)
			if err != nil {
				return fmt.Errorf("StartupRecoverer - rearmDuel - DuelRepo.GetPlayerTask player2: %w", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if r.hints != nil && player1Task != nil && player2Task != nil {
		r.hints.StartDuel(duel, map[uuid.UUID]*domain.Task{
			duel.Player1ID: player1Task,
			duel.Player2ID: player2Task,
		})
	}
	if r.duelTasks != nil && (player1Task == nil || player2Task == nil) {
		return apperr.ErrNotDuelParticipant
	}
	if r.timers != nil {
		r.timers.StartDuelTimer(duel)
	}
	return nil
}
