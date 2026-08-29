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
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/admin"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

const expiredSessionCleanupTimeout = 2 * time.Second

const playersUsernameUniqueConstraint = "players_username_key"

type PlayerPostgres struct {
	tx *TxManager
}

var (
	_ admin.PlayerRepository            = (*PlayerPostgres)(nil)
	_ duel.MatchmakingPlayerRepository  = (*PlayerPostgres)(nil)
	_ duel.FinalizationPlayerRepository = (*PlayerPostgres)(nil)
	_ player.Repository                 = (*PlayerPostgres)(nil)
	_ recovery.PlayerStatusRepository   = (*PlayerPostgres)(nil)
	_ recovery.QueuedPlayerResetter     = (*PlayerPostgres)(nil)
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
	row, err := r.tx.Querier(ctx).UpsertPlayerSessionByUsername(ctx, sqlc.UpsertPlayerSessionByUsernameParams{
		Username:         username,
		SessionToken:     uuid.NullUUID{UUID: sessionToken, Valid: true},
		SessionExpiresAt: tstz(sessionExpiresAt),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			existing, getErr := r.GetByUsername(ctx, username)
			if getErr != nil {
				return nil, domain.ErrPlayerInDuel
			}
			if existing.Status == domain.PlayerStatusQueued {
				return nil, domain.ErrPlayerQueued
			}
			if existing.Status == domain.PlayerStatusIdle {
				reservation, reservationErr := r.GetParticipantReservation(ctx, existing.ID)
				if reservationErr != nil {
					return nil, reservationErr
				}
				if reservation != nil {
					return nil, domain.ErrPlayerReserved
				}
			}
			return nil, domain.ErrPlayerInDuel
		}
		return nil, fmt.Errorf("PlayerPostgres - JoinByUsername - Querier.UpsertPlayerSessionByUsername: %w", err)
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

func (r *PlayerPostgres) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.PlayerStatus) (*domain.Player, error) {
	if !status.IsValid() {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).UpdatePlayerStatus(ctx, sqlc.UpdatePlayerStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPlayerNotFound
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
		return nil, false, domain.ErrValidation
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

func (r *PlayerPostgres) GetParticipantReservation(
	ctx context.Context,
	playerID uuid.UUID,
) (*domain.ParticipantReservation, error) {
	if playerID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).GetParticipantReservation(ctx, playerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("PlayerPostgres - GetParticipantReservation: %w", err)
	}
	return participantReservation(
		row.PlayerID,
		row.ReservationID,
		row.OwnerKind,
		row.OwnerID,
		row.Revision,
		row.AcquiredAt.Time,
		row.UpdatedAt.Time,
	), nil
}

func (r *PlayerPostgres) AcquireParticipantReservation(
	ctx context.Context,
	playerID uuid.UUID,
	ownerKind domain.ParticipantReservationOwner,
	ownerID uuid.UUID,
	acquiredAt time.Time,
) (*domain.ParticipantReservation, bool, error) {
	if playerID == uuid.Nil || ownerID == uuid.Nil || !ownerKind.IsValid() || !validServerTime(acquiredAt) {
		return nil, false, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).AcquireParticipantReservation(ctx, sqlc.AcquireParticipantReservationParams{
		PlayerID:   playerID,
		OwnerKind:  string(ownerKind),
		OwnerID:    ownerID,
		AcquiredAt: tstz(acquiredAt),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if _, getErr := r.GetByID(ctx, playerID); getErr != nil {
				return nil, false, getErr
			}
			return nil, false, domain.ErrPlayerReserved
		}
		if isForeignKeyViolation(err) {
			return nil, false, domain.WrapError(err, domain.ErrValidation)
		}
		return nil, false, fmt.Errorf("PlayerPostgres - AcquireParticipantReservation: %w", err)
	}
	return participantReservation(
		row.PlayerID,
		row.ReservationID,
		row.OwnerKind,
		row.OwnerID,
		row.Revision,
		row.AcquiredAt.Time,
		row.UpdatedAt.Time,
	), row.Changed, nil
}

func (r *PlayerPostgres) PromoteParticipantReservation(
	ctx context.Context,
	expected domain.ParticipantReservation,
	nextOwnerKind domain.ParticipantReservationOwner,
	nextOwnerID uuid.UUID,
	updatedAt time.Time,
) (*domain.ParticipantReservation, bool, error) {
	if !expected.IsValid() || expected.OwnerKind != domain.ParticipantReservationOwnerCasualQueue ||
		nextOwnerKind != domain.ParticipantReservationOwnerCasualDuel || nextOwnerID == uuid.Nil ||
		!validServerTime(updatedAt) || updatedAt.Before(expected.UpdatedAt) {
		return nil, false, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).PromoteParticipantReservation(ctx, sqlc.PromoteParticipantReservationParams{
		NextOwnerKind:     string(nextOwnerKind),
		NextOwnerID:       nextOwnerID,
		UpdatedAt:         tstz(updatedAt),
		PlayerID:          expected.PlayerID,
		ReservationID:     expected.ReservationID,
		ExpectedOwnerKind: string(expected.OwnerKind),
		ExpectedOwnerID:   expected.OwnerID,
		ExpectedRevision:  expected.Revision,
	})
	if err == nil {
		return participantReservation(
			row.PlayerID,
			row.ReservationID,
			row.OwnerKind,
			row.OwnerID,
			row.Revision,
			row.AcquiredAt.Time,
			row.UpdatedAt.Time,
		), true, nil
	}
	return r.reconcileParticipantReservationPromotion(ctx, expected, nextOwnerKind, nextOwnerID, err)
}

func (r *PlayerPostgres) reconcileParticipantReservationPromotion(
	ctx context.Context,
	expected domain.ParticipantReservation,
	nextOwnerKind domain.ParticipantReservationOwner,
	nextOwnerID uuid.UUID,
	promoteErr error,
) (*domain.ParticipantReservation, bool, error) {
	if !errors.Is(promoteErr, pgx.ErrNoRows) {
		if isForeignKeyViolation(promoteErr) {
			return nil, false, domain.WrapError(promoteErr, domain.ErrValidation)
		}
		return nil, false, fmt.Errorf("PlayerPostgres - PromoteParticipantReservation: %w", promoteErr)
	}
	current, getErr := r.GetParticipantReservation(ctx, expected.PlayerID)
	if getErr != nil {
		return nil, false, getErr
	}
	if reservationIsPromoted(current, expected, nextOwnerKind, nextOwnerID) {
		return current, false, nil
	}
	if current != nil && current.ReservationID == expected.ReservationID &&
		current.OwnerKind == expected.OwnerKind && current.OwnerID == expected.OwnerID {
		return nil, false, domain.ErrConflict
	}
	return nil, false, domain.ErrPlayerReserved
}

func (r *PlayerPostgres) ReleaseParticipantReservation(
	ctx context.Context,
	expected domain.ParticipantReservation,
) (bool, error) {
	if !expected.IsValid() {
		return false, domain.ErrValidation
	}
	_, err := r.tx.Querier(ctx).ReleaseParticipantReservation(ctx, sqlc.ReleaseParticipantReservationParams{
		PlayerID:          expected.PlayerID,
		ReservationID:     expected.ReservationID,
		ExpectedOwnerKind: string(expected.OwnerKind),
		ExpectedOwnerID:   expected.OwnerID,
		ExpectedRevision:  expected.Revision,
	})
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("PlayerPostgres - ReleaseParticipantReservation: %w", err)
	}
	current, getErr := r.GetParticipantReservation(ctx, expected.PlayerID)
	if getErr != nil {
		return false, getErr
	}
	if current == nil {
		return false, nil
	}
	if current.ReservationID == expected.ReservationID && current.OwnerKind == expected.OwnerKind &&
		current.OwnerID == expected.OwnerID {
		return false, domain.ErrConflict
	}
	return false, domain.ErrPlayerReserved
}

func (r *PlayerPostgres) ResetQueuedToIdle(ctx context.Context) (int64, error) {
	rows, err := r.tx.Querier(ctx).ResetQueuedPlayers(ctx)
	if err != nil {
		return 0, fmt.Errorf("PlayerPostgres - ResetQueuedToIdle - Querier.ResetQueuedPlayers: %w", err)
	}
	return rows, nil
}

func participantReservation(
	playerID uuid.UUID,
	reservationID uuid.UUID,
	ownerKind string,
	ownerID uuid.UUID,
	revision int64,
	acquiredAt time.Time,
	updatedAt time.Time,
) *domain.ParticipantReservation {
	return &domain.ParticipantReservation{
		PlayerID:      playerID,
		ReservationID: reservationID,
		OwnerKind:     domain.ParticipantReservationOwner(ownerKind),
		OwnerID:       ownerID,
		Revision:      revision,
		AcquiredAt:    acquiredAt,
		UpdatedAt:     updatedAt,
	}
}

func reservationIsPromoted(
	current *domain.ParticipantReservation,
	expected domain.ParticipantReservation,
	nextOwnerKind domain.ParticipantReservationOwner,
	nextOwnerID uuid.UUID,
) bool {
	return current != nil && current.ReservationID == expected.ReservationID &&
		current.OwnerKind == nextOwnerKind && current.OwnerID == nextOwnerID &&
		current.Revision == expected.Revision+1
}

func (r *PlayerPostgres) ListAdminPlayers(ctx context.Context, includeDeleted bool) ([]admin.PlayerRecord, error) {
	rows, err := r.tx.Querier(ctx).ListAdminPlayers(ctx, includeDeleted)
	if err != nil {
		return nil, fmt.Errorf("PlayerPostgres - ListAdminPlayers - Querier.ListAdminPlayers: %w", err)
	}
	out := make([]admin.PlayerRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, adminPlayerRecord(
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

func (r *PlayerPostgres) GetAdminPlayer(ctx context.Context, id uuid.UUID) (*admin.PlayerRecord, error) {
	row, err := r.tx.Querier(ctx).GetAdminPlayer(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - GetAdminPlayer - Querier.GetAdminPlayer: %w", err)
	}
	out := adminPlayerRecord(
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

func (r *PlayerPostgres) GetAdminPlayerIncludingDeleted(ctx context.Context, id uuid.UUID) (*admin.PlayerRecord, error) {
	row, err := r.tx.Querier(ctx).GetAdminPlayerIncludingDeleted(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPlayerNotFound
		}
		return nil, fmt.Errorf("PlayerPostgres - GetAdminPlayerIncludingDeleted - Querier.GetAdminPlayerIncludingDeleted: %w", err)
	}
	out := adminPlayerRecord(
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
			return domain.ErrPlayerNotFound
		}
		if isUniqueViolation(err, playersUsernameUniqueConstraint) {
			return domain.WrapError(err, domain.ErrUsernameTaken)
		}
		return fmt.Errorf("PlayerPostgres - UpdateAdminPlayerUsername - Querier.UpdatePlayerUsername: %w", err)
	}
	return nil
}

func (r *PlayerPostgres) UpsertAdminPlayerStats(
	ctx context.Context,
	id uuid.UUID,
	in admin.PlayerStatsInput,
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
			return domain.ErrConflict
		}
		if isUniqueViolation(err, playersUsernameUniqueConstraint) {
			return domain.WrapError(err, domain.ErrUsernameTaken)
		}
		return fmt.Errorf("PlayerPostgres - SoftDeleteAdminPlayer - Querier.SoftDeleteIdlePlayer: %w", err)
	}
	return nil
}

func (r *PlayerPostgres) CreateAdminPlayerAudit(ctx context.Context, in admin.PlayerAuditInput) error {
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
			return domain.ErrPlayerNotFound
		}
		return fmt.Errorf("PlayerPostgres - CreateAdminPlayerAudit - Querier.CreateAdminPlayerAuditEvent: %w", err)
	}
	return nil
}

func (r *PlayerPostgres) ListAdminPlayerAudit(
	ctx context.Context,
	playerID uuid.UUID,
	limit int32,
) ([]admin.PlayerAuditEvent, error) {
	rows, err := r.tx.Querier(ctx).ListAdminPlayerAuditEventsByPlayer(ctx, sqlc.ListAdminPlayerAuditEventsByPlayerParams{
		PlayerID: playerID,
		Limit:    limit,
	})
	if err != nil {
		return nil, fmt.Errorf("PlayerPostgres - ListAdminPlayerAudit - Querier.ListAdminPlayerAuditEventsByPlayer: %w", err)
	}
	out := make([]admin.PlayerAuditEvent, 0, len(rows))
	for _, row := range rows {
		beforeState, err := adminPlayerAuditStateFromJSON(row.BeforeState)
		if err != nil {
			return nil, fmt.Errorf("PlayerPostgres - ListAdminPlayerAudit - before_state: %w", err)
		}
		afterState, err := adminPlayerAuditStateFromJSON(row.AfterState)
		if err != nil {
			return nil, fmt.Errorf("PlayerPostgres - ListAdminPlayerAudit - after_state: %w", err)
		}
		out = append(out, admin.PlayerAuditEvent{
			ID: row.ID,
			Actor: admin.Actor{
				Subject: row.ActorSubject,
				JTI:     row.ActorJti,
			},
			Action:      admin.PlayerAuditAction(row.Action),
			PlayerID:    row.PlayerID,
			BeforeState: beforeState,
			AfterState:  afterState,
			CreatedAt:   row.CreatedAt.Time,
		})
	}
	return out, nil
}

func adminPlayerAuditStateFromJSON(raw []byte) (admin.PlayerAuditState, error) {
	var out admin.PlayerAuditState
	if err := json.Unmarshal(raw, &out); err != nil {
		return admin.PlayerAuditState{}, err
	}
	return out, nil
}

func adminPlayerRecord(
	id uuid.UUID,
	username string,
	status string,
	createdAt time.Time,
	deletedAt *time.Time,
	wins int,
	averageSolveTimeMs int64,
	statsOverridden bool,
) admin.PlayerRecord {
	return admin.PlayerRecord{
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
