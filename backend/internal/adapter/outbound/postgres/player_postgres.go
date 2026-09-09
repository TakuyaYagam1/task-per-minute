package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/ctxutil"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

const expiredSessionCleanupTimeout = 2 * time.Second

const playersUsernameUniqueConstraint = "players_username_key"

type PlayerPostgres struct {
	tx *TxManager
}

var (
	_ playerusecase.PlayerRepository = (*PlayerPostgres)(nil)
	_ playerusecase.Repository       = (*PlayerPostgres)(nil)
)

func NewPlayerPostgres(tx *TxManager) *PlayerPostgres {
	return &PlayerPostgres{tx: tx}
}

func (r *PlayerPostgres) Create(ctx context.Context, username string) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).CreatePlayer(ctx, username)
	if err != nil {
		if isUniqueViolation(err, playersUsernameUniqueConstraint) {
			return nil, domain.WrapError(err, domain.ErrUsernameTaken)
		}
		return nil, fmt.Errorf("PlayerPostgres - Create - Querier.CreatePlayer: %w", err)
	}
	return playerToDomain(row), nil
}

func (r *PlayerPostgres) JoinByUsername(
	ctx context.Context,
	username string,
	sessionToken uuid.UUID,
	sessionExpiresAt time.Time,
) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).ClaimPlayerSessionByUsername(ctx, sqlc.ClaimPlayerSessionByUsernameParams{
		Username:         username,
		SessionToken:     uuid.NullUUID{UUID: sessionToken, Valid: true},
		SessionExpiresAt: tstz(sessionExpiresAt),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrUsernameTaken
		}
		return nil, fmt.Errorf("PlayerPostgres - JoinByUsername - Querier.ClaimPlayerSessionByUsername: %w", err)
	}
	return playerToDomain(row), nil
}

func (r *PlayerPostgres) GetByID(ctx context.Context, id uuid.UUID) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).GetPlayerByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - GetByID - Querier.GetPlayerByID: %w", err)
	}
	return playerToDomain(row), nil
}

func (r *PlayerPostgres) GetByUsername(ctx context.Context, username string) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).GetPlayerByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - GetByUsername - Querier.GetPlayerByUsername: %w", err)
	}
	return playerToDomain(row), nil
}

func (r *PlayerPostgres) GetBySessionToken(ctx context.Context, token uuid.UUID) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).GetPlayerBySessionToken(ctx, uuid.NullUUID{UUID: token, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - GetBySessionToken - Querier.GetPlayerBySessionToken: %w", err)
	}
	player := playerToDomain(row)
	if sessionExpired(player.SessionExpiresAt, time.Now().UTC()) {
		cleanupCtx, cleanupCancel := ctxutil.DetachedWithTimeout(ctx, expiredSessionCleanupTimeout)
		defer cleanupCancel()
		_, _ = r.UpdateSessionToken(cleanupCtx, player.ID, nil, nil)
		return nil, domain.ErrPlayerNotFound
	}
	return player, nil
}

func (r *PlayerPostgres) UpdateSessionToken(
	ctx context.Context,
	id uuid.UUID,
	token *uuid.UUID,
	sessionExpiresAt *time.Time,
) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).UpdatePlayerSessionToken(ctx, sqlc.UpdatePlayerSessionTokenParams{
		ID:               id,
		SessionToken:     nullableUUID(token),
		SessionExpiresAt: nullableTSTZ(sessionExpiresAt),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - UpdateSessionToken - Querier.UpdatePlayerSessionToken: %w", err)
	}
	return playerToDomain(row), nil
}

func sessionExpired(expiresAt *time.Time, now time.Time) bool {
	return expiresAt == nil || !expiresAt.After(now)
}

func (r *PlayerPostgres) ListPlayers(ctx context.Context, includeDeleted bool) ([]playerusecase.PlayerRecord, error) {
	rows, err := r.tx.Querier(ctx).ListAdminPlayers(ctx, includeDeleted)
	if err != nil {
		return nil, fmt.Errorf("PlayerPostgres - ListPlayers - Querier.ListAdminPlayers: %w", err)
	}
	out := make([]playerusecase.PlayerRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, adminPlayerRecord(
			row.ID,
			row.Username,
			row.CreatedAt.Time,
			nullableTime(row.DeletedAt),
			int(row.Wins),
			row.AverageSolveTimeMs,
			row.StatsOverridden,
		))
	}
	return out, nil
}

func (r *PlayerPostgres) GetPlayer(ctx context.Context, id uuid.UUID) (*playerusecase.PlayerRecord, error) {
	row, err := r.tx.Querier(ctx).GetAdminPlayer(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - GetPlayer - Querier.GetAdminPlayer: %w", err)
	}
	out := adminPlayerRecord(
		row.ID,
		row.Username,
		row.CreatedAt.Time,
		nullableTime(row.DeletedAt),
		int(row.Wins),
		row.AverageSolveTimeMs,
		row.StatsOverridden,
	)
	return &out, nil
}

func (r *PlayerPostgres) GetPlayerIncludingDeleted(ctx context.Context, id uuid.UUID) (*playerusecase.PlayerRecord, error) {
	row, err := r.tx.Querier(ctx).GetAdminPlayerIncludingDeleted(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - GetPlayerIncludingDeleted - Querier.GetAdminPlayerIncludingDeleted: %w", err)
	}
	out := adminPlayerRecord(
		row.ID,
		row.Username,
		row.CreatedAt.Time,
		nullableTime(row.DeletedAt),
		int(row.Wins),
		row.AverageSolveTimeMs,
		row.StatsOverridden,
	)
	return &out, nil
}

func (r *PlayerPostgres) UpdateUsername(ctx context.Context, id uuid.UUID, username string) error {
	if _, err := r.tx.Querier(ctx).UpdatePlayerUsername(ctx, sqlc.UpdatePlayerUsernameParams{
		ID:       id,
		Username: username,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrPlayerNotFound
		}
		if isUniqueViolation(err, playersUsernameUniqueConstraint) {
			return domain.WrapError(err, domain.ErrUsernameTaken)
		}
		return fmt.Errorf("PlayerPostgres - UpdateUsername - Querier.UpdatePlayerUsername: %w", err)
	}
	return nil
}

func (r *PlayerPostgres) UpsertStats(
	ctx context.Context,
	id uuid.UUID,
	in playerusecase.StatsInput,
	updatedAt time.Time,
) error {
	if in.Wins < 0 || in.Wins > math.MaxInt32 {
		return domain.ErrValidation
	}

	if _, err := r.tx.Querier(ctx).UpsertPlayerLeaderboardOverride(ctx, sqlc.UpsertPlayerLeaderboardOverrideParams{
		PlayerID:           id,
		Wins:               int32(in.Wins),
		AverageSolveTimeMs: in.AverageSolveTimeMs,
		UpdatedAt:          tstz(updatedAt),
	}); err != nil {
		if isForeignKeyViolation(err) {
			return domain.ErrPlayerNotFound
		}
		return fmt.Errorf("PlayerPostgres - UpsertStats - Querier.UpsertPlayerLeaderboardOverride: %w", err)
	}
	return nil
}

func (r *PlayerPostgres) SoftDeletePlayer(
	ctx context.Context,
	id uuid.UUID,
	deletedUsername string,
	deletedAt time.Time,
) error {
	if _, err := r.tx.Querier(ctx).SoftDeletePlayer(ctx, sqlc.SoftDeletePlayerParams{
		ID:        id,
		Username:  deletedUsername,
		DeletedAt: tstz(deletedAt),
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if _, lookupErr := r.GetPlayer(ctx, id); lookupErr != nil {
				return lookupErr
			}
			return domain.ErrConflict
		}
		if isUniqueViolation(err, playersUsernameUniqueConstraint) {
			return domain.WrapError(err, domain.ErrUsernameTaken)
		}
		return fmt.Errorf("PlayerPostgres - SoftDeletePlayer - Querier.SoftDeletePlayer: %w", err)
	}
	return nil
}

func (r *PlayerPostgres) CreatePlayerAudit(ctx context.Context, in playerusecase.AuditInput) error {
	beforeState, err := json.Marshal(in.BeforeState)
	if err != nil {
		return fmt.Errorf("PlayerPostgres - CreatePlayerAudit - json.Marshal before state: %w", err)
	}
	afterState, err := json.Marshal(in.AfterState)
	if err != nil {
		return fmt.Errorf("PlayerPostgres - CreatePlayerAudit - json.Marshal after state: %w", err)
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
			return domain.ErrPlayerNotFound
		}
		return fmt.Errorf("PlayerPostgres - CreatePlayerAudit - Querier.CreateAdminPlayerAuditEvent: %w", err)
	}
	return nil
}

func (r *PlayerPostgres) ListPlayerAudit(
	ctx context.Context,
	playerID uuid.UUID,
	limit int32,
) ([]playerusecase.AuditEvent, error) {
	rows, err := r.tx.Querier(ctx).ListAdminPlayerAuditEventsByPlayer(ctx, sqlc.ListAdminPlayerAuditEventsByPlayerParams{
		PlayerID: playerID,
		Limit:    limit,
	})
	if err != nil {
		return nil, fmt.Errorf("PlayerPostgres - ListPlayerAudit - Querier.ListAdminPlayerAuditEventsByPlayer: %w", err)
	}
	out := make([]playerusecase.AuditEvent, 0, len(rows))
	for _, row := range rows {
		beforeState, err := adminPlayerAuditStateFromJSON(row.BeforeState)
		if err != nil {
			return nil, fmt.Errorf("PlayerPostgres - ListPlayerAudit - before_state: %w", err)
		}
		afterState, err := adminPlayerAuditStateFromJSON(row.AfterState)
		if err != nil {
			return nil, fmt.Errorf("PlayerPostgres - ListPlayerAudit - after_state: %w", err)
		}
		out = append(out, playerusecase.AuditEvent{
			ID: row.ID,
			Actor: playerusecase.Actor{
				Subject: row.ActorSubject,
				JTI:     row.ActorJti,
			},
			Action:      playerusecase.AuditAction(row.Action),
			PlayerID:    row.PlayerID,
			BeforeState: beforeState,
			AfterState:  afterState,
			CreatedAt:   row.CreatedAt.Time,
		})
	}
	return out, nil
}

func adminPlayerAuditStateFromJSON(raw []byte) (playerusecase.AuditState, error) {
	var out playerusecase.AuditState
	if err := json.Unmarshal(raw, &out); err != nil {
		return playerusecase.AuditState{}, err
	}
	return out, nil
}

func adminPlayerRecord(
	id uuid.UUID,
	username string,
	createdAt time.Time,
	deletedAt *time.Time,
	wins int,
	averageSolveTimeMs int64,
	statsOverridden bool,
) playerusecase.PlayerRecord {
	return playerusecase.PlayerRecord{
		PlayerID:           id,
		Username:           username,
		CreatedAt:          createdAt,
		DeletedAt:          deletedAt,
		Wins:               wins,
		AverageSolveTimeMs: averageSolveTimeMs,
		StatsOverridden:    statsOverridden,
	}
}
