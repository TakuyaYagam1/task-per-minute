package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/repo/persistent/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
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

func (r *PlayerPostgres) ListAdminPlayers(ctx context.Context, includeDeleted bool) ([]usecase.AdminPlayerRecord, error) {
	rows, err := r.tx.Querier(ctx).ListAdminPlayers(ctx, includeDeleted)
	if err != nil {
		return nil, fmt.Errorf("PlayerPostgres - ListAdminPlayers - Querier.ListAdminPlayers: %w", err)
	}
	out := make([]usecase.AdminPlayerRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, adminPlayerToUsecase(
			row.ID,
			row.Username,
			row.Status,
			row.CreatedAt.Time,
			nullableTime(row.DeletedAt),
			int(row.Wins),
			row.AverageSolveTimeMs,
			row.StatsOverridden,
		))
	}
	return out, nil
}

func (r *PlayerPostgres) GetAdminPlayer(ctx context.Context, id uuid.UUID) (*usecase.AdminPlayerRecord, error) {
	row, err := r.tx.Querier(ctx).GetAdminPlayer(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - GetAdminPlayer - Querier.GetAdminPlayer: %w", err)
	}
	out := adminPlayerToUsecase(
		row.ID,
		row.Username,
		row.Status,
		row.CreatedAt.Time,
		nullableTime(row.DeletedAt),
		int(row.Wins),
		row.AverageSolveTimeMs,
		row.StatsOverridden,
	)
	return &out, nil
}

func (r *PlayerPostgres) GetAdminPlayerIncludingDeleted(ctx context.Context, id uuid.UUID) (*usecase.AdminPlayerRecord, error) {
	row, err := r.tx.Querier(ctx).GetAdminPlayerIncludingDeleted(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - GetAdminPlayerIncludingDeleted - Querier.GetAdminPlayerIncludingDeleted: %w", err)
	}
	out := adminPlayerToUsecase(
		row.ID,
		row.Username,
		row.Status,
		row.CreatedAt.Time,
		nullableTime(row.DeletedAt),
		int(row.Wins),
		row.AverageSolveTimeMs,
		row.StatsOverridden,
	)
	return &out, nil
}

func (r *PlayerPostgres) UpdateAdminPlayerUsername(ctx context.Context, id uuid.UUID, username string) error {
	if _, err := r.tx.Querier(ctx).UpdatePlayerUsername(ctx, sqlc.UpdatePlayerUsernameParams{
		ID:       id,
		Username: username,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.ErrPlayerNotFound
		}
		if isUniqueViolation(err, playersUsernameUniqueConstraint) {
			return apperr.Wrap(err, apperr.ErrUsernameTaken)
		}
		return fmt.Errorf("PlayerPostgres - UpdateAdminPlayerUsername - Querier.UpdatePlayerUsername: %w", err)
	}
	return nil
}

func (r *PlayerPostgres) UpsertAdminPlayerStats(
	ctx context.Context,
	id uuid.UUID,
	in usecase.AdminPlayerStatsInput,
	updatedAt time.Time,
) error {
	if in.Wins < 0 || in.Wins > math.MaxInt32 {
		return apperr.ErrValidation
	}

	if _, err := r.tx.Querier(ctx).UpsertPlayerLeaderboardOverride(ctx, sqlc.UpsertPlayerLeaderboardOverrideParams{
		PlayerID:           id,
		Wins:               int32(in.Wins),
		AverageSolveTimeMs: in.AverageSolveTimeMs,
		UpdatedAt:          tstz(updatedAt),
	}); err != nil {
		if isForeignKeyViolation(err) {
			return apperr.ErrPlayerNotFound
		}
		return fmt.Errorf("PlayerPostgres - UpsertAdminPlayerStats - Querier.UpsertPlayerLeaderboardOverride: %w", err)
	}
	return nil
}

func (r *PlayerPostgres) SoftDeleteAdminPlayer(
	ctx context.Context,
	id uuid.UUID,
	deletedUsername string,
	deletedAt time.Time,
) error {
	if _, err := r.tx.Querier(ctx).SoftDeleteIdlePlayer(ctx, sqlc.SoftDeleteIdlePlayerParams{
		ID:        id,
		Username:  deletedUsername,
		DeletedAt: tstz(deletedAt),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if _, lookupErr := r.GetAdminPlayer(ctx, id); lookupErr != nil {
				return lookupErr
			}
			return apperr.ErrConflict
		}
		if isUniqueViolation(err, playersUsernameUniqueConstraint) {
			return apperr.Wrap(err, apperr.ErrUsernameTaken)
		}
		return fmt.Errorf("PlayerPostgres - SoftDeleteAdminPlayer - Querier.SoftDeleteIdlePlayer: %w", err)
	}
	return nil
}

func (r *PlayerPostgres) CreateAdminPlayerAudit(ctx context.Context, in usecase.AdminPlayerAuditInput) error {
	beforeState, err := json.Marshal(in.BeforeState)
	if err != nil {
		return fmt.Errorf("PlayerPostgres - CreateAdminPlayerAudit - json.Marshal before state: %w", err)
	}
	afterState, err := json.Marshal(in.AfterState)
	if err != nil {
		return fmt.Errorf("PlayerPostgres - CreateAdminPlayerAudit - json.Marshal after state: %w", err)
	}

	if err := r.tx.Querier(ctx).CreateAdminPlayerAuditEvent(ctx, sqlc.CreateAdminPlayerAuditEventParams{
		ActorSubject: in.Actor.Subject,
		ActorJti:     in.Actor.JTI,
		Action:       string(in.Action),
		PlayerID:     in.PlayerID,
		BeforeState:  beforeState,
		AfterState:   afterState,
		CreatedAt:    tstz(in.CreatedAt),
	}); err != nil {
		if isForeignKeyViolation(err) {
			return apperr.ErrPlayerNotFound
		}
		return fmt.Errorf("PlayerPostgres - CreateAdminPlayerAudit - Querier.CreateAdminPlayerAuditEvent: %w", err)
	}
	return nil
}

func (r *PlayerPostgres) ListAdminPlayerAudit(
	ctx context.Context,
	playerID uuid.UUID,
	limit int32,
) ([]usecase.AdminPlayerAuditEvent, error) {
	rows, err := r.tx.Querier(ctx).ListAdminPlayerAuditEventsByPlayer(ctx, sqlc.ListAdminPlayerAuditEventsByPlayerParams{
		PlayerID: playerID,
		Limit:    limit,
	})
	if err != nil {
		return nil, fmt.Errorf("PlayerPostgres - ListAdminPlayerAudit - Querier.ListAdminPlayerAuditEventsByPlayer: %w", err)
	}
	out := make([]usecase.AdminPlayerAuditEvent, 0, len(rows))
	for _, row := range rows {
		beforeState, err := adminPlayerAuditStateFromJSON(row.BeforeState)
		if err != nil {
			return nil, fmt.Errorf("PlayerPostgres - ListAdminPlayerAudit - before_state: %w", err)
		}
		afterState, err := adminPlayerAuditStateFromJSON(row.AfterState)
		if err != nil {
			return nil, fmt.Errorf("PlayerPostgres - ListAdminPlayerAudit - after_state: %w", err)
		}
		out = append(out, usecase.AdminPlayerAuditEvent{
			ID: row.ID,
			Actor: usecase.AdminActor{
				Subject: row.ActorSubject,
				JTI:     row.ActorJti,
			},
			Action:      usecase.AdminPlayerAuditAction(row.Action),
			PlayerID:    row.PlayerID,
			BeforeState: beforeState,
			AfterState:  afterState,
			CreatedAt:   row.CreatedAt.Time,
		})
	}
	return out, nil
}

func adminPlayerAuditStateFromJSON(raw []byte) (usecase.AdminPlayerAuditState, error) {
	var out usecase.AdminPlayerAuditState
	if err := json.Unmarshal(raw, &out); err != nil {
		return usecase.AdminPlayerAuditState{}, err
	}
	return out, nil
}

func adminPlayerToUsecase(
	id uuid.UUID,
	username string,
	status string,
	createdAt time.Time,
	deletedAt *time.Time,
	wins int,
	averageSolveTimeMs int64,
	statsOverridden bool,
) usecase.AdminPlayerRecord {
	return usecase.AdminPlayerRecord{
		PlayerID:           id,
		Username:           username,
		Status:             domain.PlayerStatus(status),
		CreatedAt:          createdAt,
		DeletedAt:          deletedAt,
		Wins:               wins,
		AverageSolveTimeMs: averageSolveTimeMs,
		StatsOverridden:    statsOverridden,
	}
}
