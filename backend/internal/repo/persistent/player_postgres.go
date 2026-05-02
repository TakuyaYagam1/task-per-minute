package persistent

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent/sqlc"
)

const playersUsernameUniqueConstraint = "players_username_key"

type PlayerPostgres struct {
	tx *TxManager
}

func NewPlayerPostgres(tx *TxManager) *PlayerPostgres {
	return &PlayerPostgres{tx: tx}
}

func (r *PlayerPostgres) Create(ctx context.Context, username string) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).CreatePlayer(ctx, username)
	if err != nil {
		if isUniqueViolation(err, playersUsernameUniqueConstraint) {
			return nil, apperr.Wrap(err, apperr.ErrUsernameTaken)
		}
		return nil, fmt.Errorf("PlayerPostgres - Create - Querier.CreatePlayer: %w", err)
	}
	return playerToDomain(row), nil
}

func (r *PlayerPostgres) JoinByUsername(ctx context.Context, username string, sessionToken uuid.UUID) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).UpsertPlayerSessionByUsername(ctx, sqlc.UpsertPlayerSessionByUsernameParams{
		Username:     username,
		SessionToken: uuid.NullUUID{UUID: sessionToken, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrPlayerInDuel
		}
		return nil, fmt.Errorf("PlayerPostgres - JoinByUsername - Querier.UpsertPlayerSessionByUsername: %w", err)
	}
	return playerToDomain(row), nil
}

func (r *PlayerPostgres) GetByID(ctx context.Context, id uuid.UUID) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).GetPlayerByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - GetByID - Querier.GetPlayerByID: %w", err)
	}
	return playerToDomain(row), nil
}

func (r *PlayerPostgres) GetByUsername(ctx context.Context, username string) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).GetPlayerByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - GetByUsername - Querier.GetPlayerByUsername: %w", err)
	}
	return playerToDomain(row), nil
}

func (r *PlayerPostgres) GetBySessionToken(ctx context.Context, token uuid.UUID) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).GetPlayerBySessionToken(ctx, uuid.NullUUID{UUID: token, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - GetBySessionToken - Querier.GetPlayerBySessionToken: %w", err)
	}
	return playerToDomain(row), nil
}

func (r *PlayerPostgres) UpdateSessionToken(ctx context.Context, id uuid.UUID, token *uuid.UUID) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).UpdatePlayerSessionToken(ctx, sqlc.UpdatePlayerSessionTokenParams{
		ID:           id,
		SessionToken: nullableUUID(token),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - UpdateSessionToken - Querier.UpdatePlayerSessionToken: %w", err)
	}
	return playerToDomain(row), nil
}

func (r *PlayerPostgres) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.PlayerStatus) (*domain.Player, error) {
	if !status.IsValid() {
		return nil, apperr.ErrValidation
	}
	row, err := r.tx.Querier(ctx).UpdatePlayerStatus(ctx, sqlc.UpdatePlayerStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - UpdateStatus - Querier.UpdatePlayerStatus: %w", err)
	}
	return playerToDomain(row), nil
}

func (r *PlayerPostgres) UpdateStatusIfCurrent(
	ctx context.Context,
	id uuid.UUID,
	from domain.PlayerStatus,
	to domain.PlayerStatus,
) (*domain.Player, bool, error) {
	if !from.IsValid() || !to.IsValid() {
		return nil, false, apperr.ErrValidation
	}
	row, err := r.tx.Querier(ctx).UpdatePlayerStatusIfCurrent(ctx, sqlc.UpdatePlayerStatusIfCurrentParams{
		ID:       id,
		Status:   string(from),
		Status_2: string(to),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("PlayerPostgres - UpdateStatusIfCurrent - Querier.UpdatePlayerStatusIfCurrent: %w", err)
	}
	return playerToDomain(row), true, nil
}

func (r *PlayerPostgres) ResetQueuedToIdle(ctx context.Context) (int64, error) {
	rows, err := r.tx.Querier(ctx).ResetQueuedPlayers(ctx)
	if err != nil {
		return 0, fmt.Errorf("PlayerPostgres - ResetQueuedToIdle - Querier.ResetQueuedPlayers: %w", err)
	}
	return rows, nil
}
