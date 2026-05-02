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

type DuelPostgres struct {
	tx *TxManager
}

func NewDuelPostgres(tx *TxManager) *DuelPostgres {
	return &DuelPostgres{tx: tx}
}

func (r *DuelPostgres) Create(ctx context.Context, player1ID, player2ID uuid.UUID, deadline time.Time) (*domain.Duel, error) {
	if player1ID == player2ID {
		return nil, apperr.ErrValidation
	}
	row, err := r.tx.Querier(ctx).CreateDuel(ctx, sqlc.CreateDuelParams{
		Player1ID: player1ID,
		Player2ID: player2ID,
		Deadline:  tstz(deadline),
	})
	if err != nil {
		return nil, fmt.Errorf("DuelPostgres - Create - Querier.CreateDuel: %w", err)
	}
	return duelToDomain(row), nil
}

func (r *DuelPostgres) GetByID(ctx context.Context, id uuid.UUID) (*domain.Duel, error) {
	row, err := r.tx.Querier(ctx).GetDuelByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrDuelNotFound
		}
		return nil, fmt.Errorf("DuelPostgres - GetByID - Querier.GetDuelByID: %w", err)
	}
	return duelToDomain(row), nil
}

// GetActiveByPlayerID returns the player's currently-active duel (if any).
// A nil duel with nil error means the player has no active duel.
func (r *DuelPostgres) GetActiveByPlayerID(ctx context.Context, playerID uuid.UUID) (*domain.Duel, error) {
	row, err := r.tx.Querier(ctx).GetActiveDuelByPlayerID(ctx, playerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("DuelPostgres - GetActiveByPlayerID - Querier.GetActiveDuelByPlayerID: %w", err)
	}
	return duelToDomain(row), nil
}

func (r *DuelPostgres) UpdateDeadline(ctx context.Context, id uuid.UUID, deadline time.Time) (*domain.Duel, error) {
	row, err := r.tx.Querier(ctx).UpdateDuelDeadline(ctx, sqlc.UpdateDuelDeadlineParams{
		ID:       id,
		Deadline: tstz(deadline),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			existing, getErr := r.GetByID(ctx, id)
			if getErr == nil && existing.Status == domain.DuelStatusFinished {
				return nil, apperr.ErrDuelFinished
			}
			return nil, apperr.ErrDuelNotFound
		}
		return nil, fmt.Errorf("DuelPostgres - UpdateDeadline - Querier.UpdateDuelDeadline: %w", err)
	}
	return duelToDomain(row), nil
}

// Finish closes the duel with the supplied winner (nil for a draw) and finished_at.
// status MUST be domain.DuelStatusFinished - the schema CHECK enforces
// (status='finished') = (finished_at IS NOT NULL).
func (r *DuelPostgres) Finish(ctx context.Context, id uuid.UUID, winnerID *uuid.UUID, finishedAt time.Time, status domain.DuelStatus) (*domain.Duel, error) {
	if status != domain.DuelStatusFinished {
		return nil, apperr.ErrValidation
	}
	row, err := r.tx.Querier(ctx).FinishDuel(ctx, sqlc.FinishDuelParams{
		ID:         id,
		WinnerID:   nullableUUID(winnerID),
		FinishedAt: tstz(finishedAt),
		Status:     string(status),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			existing, getErr := r.GetByID(ctx, id)
			if getErr == nil && existing.Status == domain.DuelStatusFinished {
				return nil, apperr.ErrDuelFinished
			}
			return nil, apperr.ErrDuelNotFound
		}
		return nil, fmt.Errorf("DuelPostgres - Finish - Querier.FinishDuel: %w", err)
	}
	return duelToDomain(row), nil
}

func (r *DuelPostgres) ListActive(ctx context.Context) ([]*domain.Duel, error) {
	rows, err := r.tx.Querier(ctx).ListActiveDuels(ctx)
	if err != nil {
		return nil, fmt.Errorf("DuelPostgres - ListActive - Querier.ListActiveDuels: %w", err)
	}
	out := make([]*domain.Duel, 0, len(rows))
	for _, row := range rows {
		out = append(out, duelToDomain(row))
	}
	return out, nil
}

func (r *DuelPostgres) CreateDuelPlayerTask(ctx context.Context, duelID, playerID, taskID uuid.UUID) error {
	if err := r.tx.Querier(ctx).CreateDuelPlayerTask(ctx, sqlc.CreateDuelPlayerTaskParams{
		DuelID:   duelID,
		PlayerID: playerID,
		TaskID:   taskID,
	}); err != nil {
		return fmt.Errorf("DuelPostgres - CreateDuelPlayerTask - Querier.CreateDuelPlayerTask: %w", err)
	}
	return nil
}

func (r *DuelPostgres) GetDuelPlayerTask(ctx context.Context, duelID, playerID uuid.UUID) (*domain.DuelPlayerTask, error) {
	row, err := r.tx.Querier(ctx).GetDuelPlayerTask(ctx, sqlc.GetDuelPlayerTaskParams{
		DuelID:   duelID,
		PlayerID: playerID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrNotDuelParticipant
		}
		return nil, fmt.Errorf("DuelPostgres - GetDuelPlayerTask - Querier.GetDuelPlayerTask: %w", err)
	}
	return duelPlayerTaskToDomain(row), nil
}

func (r *DuelPostgres) GetPlayerTask(ctx context.Context, duelID, playerID uuid.UUID) (*domain.Task, error) {
	row, err := r.tx.Querier(ctx).GetPlayerTask(ctx, sqlc.GetPlayerTaskParams{
		DuelID:   duelID,
		PlayerID: playerID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrNotDuelParticipant
		}
		return nil, fmt.Errorf("DuelPostgres - GetPlayerTask - Querier.GetPlayerTask: %w", err)
	}
	return taskToDomain(row), nil
}

// MarkSolved sets solved=true and solved_at on the (duel_id, player_id) row.
// Schema CHECK enforces solved = (solved_at IS NOT NULL); both are written together.
func (r *DuelPostgres) MarkSolved(ctx context.Context, duelID, playerID uuid.UUID, solvedAt time.Time) error {
	if err := r.tx.Querier(ctx).MarkDuelPlayerTaskSolved(ctx, sqlc.MarkDuelPlayerTaskSolvedParams{
		DuelID:   duelID,
		PlayerID: playerID,
		SolvedAt: tstz(solvedAt),
	}); err != nil {
		return fmt.Errorf("DuelPostgres - MarkSolved - Querier.MarkDuelPlayerTaskSolved: %w", err)
	}
	return nil
}
