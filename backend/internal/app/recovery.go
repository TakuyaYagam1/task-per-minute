package app

import (
	"context"
	"fmt"

	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
	"github.com/TakuyaYagam1/task-per-minute/pkg/clock"
)

type StartupRecoverer struct {
	tx          usecase.TxManager
	duels       usecase.ActiveDuelRepo
	players     usecase.PlayerStatusRepo
	queued      usecase.QueuedPlayerResetter
	queue       usecase.MatchmakingQueueCleaner
	broadcaster usecase.DuelBroadcaster
	clock       clock.Clock
	log         logkit.Logger
}

func NewStartupRecoverer(
	tx usecase.TxManager,
	duels usecase.ActiveDuelRepo,
	players usecase.PlayerStatusRepo,
	queued usecase.QueuedPlayerResetter,
	queue usecase.MatchmakingQueueCleaner,
	broadcaster usecase.DuelBroadcaster,
	clk clock.Clock,
	log logkit.Logger,
) *StartupRecoverer {
	if clk == nil {
		clk = clock.Real{}
	}
	return &StartupRecoverer{
		tx:          tx,
		duels:       duels,
		players:     players,
		queued:      queued,
		queue:       queue,
		broadcaster: broadcaster,
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

	recovered := 0
	for _, duel := range active {
		finished, err := r.recoverDuel(ctx, duel)
		if err != nil {
			return err
		}
		if finished == nil {
			continue
		}
		recovered++
		if r.broadcaster != nil {
			r.broadcaster.BroadcastDuelFinished(ctx, finished)
		}
	}

	if r.log != nil {
		r.log.Info("startup recovery completed", logkit.Fields{
			"active_duels_recovered": recovered,
			"queued_players_reset":   queuedReset,
		})
	}
	return nil
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

func (r *StartupRecoverer) recoverDuel(ctx context.Context, duel *domain.Duel) (*domain.Duel, error) {
	if duel == nil || duel.Status != domain.DuelStatusActive {
		return nil, nil
	}

	var finished *domain.Duel
	now := r.clock.Now()
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
