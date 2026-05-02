package duel

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
)

// finalizeDuel atomically finishes the duel with the given winner (nil = draw)
// and resets both participants to PlayerStatusIdle. Idempotent: when the duel
// is already finished it returns (nil, nil) so callers can safely retry.
//
// Used by both TimerRegistry (deadline expiry - always a draw) and
// ReconnectManager (disconnect-overflow / opponent forfeit - winner is set)
// as the single owner of the duel-finish transaction.
//
// The optional `boards` variadic lets callers wire a leaderboard store. When
// supplied AND winnerID is non-nil, the winner's username is captured inside
// the tx (so the read is consistent with the duel finish) and IncrementWin
// fires AFTER the tx commits - Redis is not transactional with Postgres, so
// running it inside would risk a leaderboard bump for a duel whose commit
// later failed. The error is intentionally swallowed: the duel is already
// finalized in PG and can be reconciled from duels.winner_id offline.
func finalizeDuel(
	ctx context.Context,
	tx usecase.TxManager,
	duels usecase.DuelRepo,
	players usecase.PlayerRepo,
	now time.Time,
	duelID uuid.UUID,
	winnerID *uuid.UUID,
	board usecase.LeaderboardBumper,
	log logkit.Logger,
) (*domain.Duel, error) {
	finished, winnerUsername, err := finalizeDuelInTx(ctx, tx, duels, players, now, duelID, winnerID, board != nil)
	if err != nil {
		return nil, err
	}

	if finished != nil && winnerUsername != "" {
		bumpLeaderboard(ctx, board, log, duelID, winnerUsername)
	}

	return finished, nil
}

// finalizeDuelInTx runs the duel-finish state machine in a single transaction.
// captureWinner is true when the caller intends to bump the leaderboard - only
// then do we issue the extra players.GetByID read to capture the winner's
// username while the row is still consistent with the duel finish.
func finalizeDuelInTx(
	ctx context.Context,
	tx usecase.TxManager,
	duels usecase.DuelRepo,
	players usecase.PlayerRepo,
	now time.Time,
	duelID uuid.UUID,
	winnerID *uuid.UUID,
	captureWinner bool,
) (*domain.Duel, string, error) {
	var (
		finished       *domain.Duel
		winnerUsername string
	)
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

		if winnerID != nil && captureWinner {
			winner, lookupErr := players.GetByID(txCtx, *winnerID)
			if lookupErr != nil {
				return fmt.Errorf("finalizeDuel - PlayerRepo.GetByID winner: %w", lookupErr)
			}
			winnerUsername = winner.Username
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
		return nil, "", err
	}
	return finished, winnerUsername, nil
}
