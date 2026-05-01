package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent/sqlc"
)

type HistoryPostgres struct {
	tx *TxManager
}

func NewHistoryPostgres(tx *TxManager) *HistoryPostgres {
	return &HistoryPostgres{tx: tx}
}

func (r *HistoryPostgres) AddSolved(ctx context.Context, playerID, taskID uuid.UUID, solvedAt time.Time) error {
	if err := r.tx.Querier(ctx).AddSolvedTask(ctx, sqlc.AddSolvedTaskParams{
		PlayerID: playerID,
		TaskID:   taskID,
		SolvedAt: tstz(solvedAt),
	}); err != nil {
		return fmt.Errorf("HistoryPostgres - AddSolved - Querier.AddSolvedTask: %w", err)
	}
	return nil
}

func (r *HistoryPostgres) ListSolvedTaskIDs(ctx context.Context, playerID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.tx.Querier(ctx).ListSolvedTaskIDs(ctx, playerID)
	if err != nil {
		return nil, fmt.Errorf("HistoryPostgres - ListSolvedTaskIDs - Querier.ListSolvedTaskIDs: %w", err)
	}
	return rows, nil
}

// SelectUnsolvedTaskByDifficulty returns a random task of the given difficulty
// the player has NOT yet solved. Returns apperr.ErrTaskNotFound when every
// task in that bucket is already in the player's history; the caller is
// responsible for falling back to SelectAnyTaskByDifficulty.
func (r *HistoryPostgres) SelectUnsolvedTaskByDifficulty(ctx context.Context, playerID uuid.UUID, difficulty domain.Difficulty) (*domain.Task, error) {
	if !difficulty.IsValid() {
		return nil, apperr.ErrValidation
	}
	row, err := r.tx.Querier(ctx).SelectUnsolvedTaskByDifficulty(ctx, sqlc.SelectUnsolvedTaskByDifficultyParams{
		PlayerID:   playerID,
		Difficulty: string(difficulty),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrTaskNotFound
		}
		return nil, fmt.Errorf("HistoryPostgres - SelectUnsolvedTaskByDifficulty - Querier.SelectUnsolvedTaskByDifficulty: %w", err)
	}
	return taskToDomain(row), nil
}

// SelectAnyTaskByDifficulty is the matchmaking fallback: when the player has
// solved every task in the bucket we still need to start the duel.
func (r *HistoryPostgres) SelectAnyTaskByDifficulty(ctx context.Context, difficulty domain.Difficulty) (*domain.Task, error) {
	if !difficulty.IsValid() {
		return nil, apperr.ErrValidation
	}
	row, err := r.tx.Querier(ctx).SelectAnyTaskByDifficulty(ctx, string(difficulty))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrTaskNotFound
		}
		return nil, fmt.Errorf("HistoryPostgres - SelectAnyTaskByDifficulty - Querier.SelectAnyTaskByDifficulty: %w", err)
	}
	return taskToDomain(row), nil
}
