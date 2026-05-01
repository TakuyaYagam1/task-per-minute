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
)

// finalizeDuel atomically finishes the duel with the given winner (nil = draw)
// and resets both participants to PlayerStatusIdle. Idempotent: when the duel
// is already finished it returns (nil, nil) so callers can safely retry.
//
// Used by both TimerRegistry (deadline expiry) and ReconnectManager
// (disconnect-overflow / opponent forfeit) — single owner of the duel-finish
// transaction.
func finalizeDuel(
	ctx context.Context,
	tx usecase.TxManager,
	duels usecase.DuelRepo,
	players usecase.PlayerRepo,
	now time.Time,
	duelID uuid.UUID,
	winnerID *uuid.UUID,
) (*domain.Duel, error) {
	var finished *domain.Duel
	if err := tx.Do(ctx, func(txCtx context.Context) error {
		duel, err := duels.GetByID(txCtx, duelID)
		if err != nil {
			if errors.Is(err, apperr.ErrDuelNotFound) {
				return nil
			}
			return fmt.Errorf("finalizeDuel - DuelRepo.GetByID: %w", err)
		}
		if duel.Status != domain.DuelStatusActive {
			return nil
		}

		finishedDuel, err := duels.Finish(txCtx, duelID, winnerID, now, domain.DuelStatusFinished)
		if err != nil {
			if errors.Is(err, apperr.ErrDuelFinished) {
				return nil
			}
			return fmt.Errorf("finalizeDuel - DuelRepo.Finish: %w", err)
		}
		if _, err := players.UpdateStatus(txCtx, duel.Player1ID, domain.PlayerStatusIdle); err != nil {
			return fmt.Errorf("finalizeDuel - PlayerRepo.UpdateStatus player1: %w", err)
		}
		if _, err := players.UpdateStatus(txCtx, duel.Player2ID, domain.PlayerStatusIdle); err != nil {
			return fmt.Errorf("finalizeDuel - PlayerRepo.UpdateStatus player2: %w", err)
		}
		finished = finishedDuel
		return nil
	}); err != nil {
		return nil, err
	}

	return finished, nil
}
