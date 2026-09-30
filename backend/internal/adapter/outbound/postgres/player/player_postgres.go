package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/ctxutil"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

const expiredSessionCleanupTimeout = 2 * time.Second

const playersUsernameUniqueConstraint = "players_username_key"

type PlayerPostgres struct {
	tx *db.TxManager
}

var (
	_ playerusecase.PlayerRepository = (*PlayerPostgres)(nil)
	_ playerusecase.Repository       = (*PlayerPostgres)(nil)
)

func NewPlayerPostgres(tx *db.TxManager) *PlayerPostgres {
	return &PlayerPostgres{tx: tx}
}

func (r *PlayerPostgres) Create(ctx context.Context, username string) (*domain.Player, error) {
	var created *domain.Player
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		q := r.tx.Querier(txCtx)
		normalized := strings.ToLower(username)
		if err := q.LockPlayerUsername(txCtx, normalized); err != nil {
			return fmt.Errorf("PlayerPostgres - Create - LockPlayerUsername: %w", err)
		}
		reserved, err := q.CreateLegacyPlayerUsernameReservation(txCtx, normalized)
		if err != nil {
			return fmt.Errorf("PlayerPostgres - Create - CreateLegacyPlayerUsernameReservation: %w", err)
		}
		if reserved != 1 {
			return domain.ErrUsernameTaken
		}
		row, err := q.CreatePlayer(txCtx, username)
		if err != nil {
			if isUniqueViolation(err, playersUsernameUniqueConstraint) {
				return domain.WrapError(err, domain.ErrUsernameTaken)
			}
			return fmt.Errorf("PlayerPostgres - Create - Querier.CreatePlayer: %w", err)
		}
		created = playerToDomain(row)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (r *PlayerPostgres) CreatePlayer(ctx context.Context, username string) (*playerusecase.PlayerRecord, error) {
	player, err := r.Create(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("PlayerPostgres - CreatePlayer - Create: %w", err)
	}

	created := adminPlayerRecord(
		player.ID,
		player.Username,
		player.CreatedAt,
		nil,
		0,
		0,
		false,
	)
	return &created, nil
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
		_, _ = r.UpdateSessionToken(cleanupCtx, player.ID, token, nil, nil)
		return nil, domain.ErrPlayerNotFound
	}
	return player, nil
}

func (r *PlayerPostgres) UpdateSessionToken(
	ctx context.Context,
	id uuid.UUID,
	expectedToken uuid.UUID,
	token *uuid.UUID,
	sessionExpiresAt *time.Time,
) (*domain.Player, error) {
	row, err := r.tx.Querier(ctx).UpdatePlayerSessionToken(ctx, sqlc.UpdatePlayerSessionTokenParams{
		PlayerID:             id,
		SessionToken:         nullableUUID(token),
		SessionExpiresAt:     nullableTSTZ(sessionExpiresAt),
		ExpectedSessionToken: uuid.NullUUID{UUID: expectedToken, Valid: true},
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
	return r.tx.Do(ctx, func(txCtx context.Context) error {
		q := r.tx.Querier(txCtx)
		current, err := q.GetPlayerForIdentityChange(txCtx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrPlayerNotFound
			}
			return fmt.Errorf("PlayerPostgres - UpdateUsername - GetPlayerForIdentityChange: %w", err)
		}
		if current.DeletedAt.Valid {
			return domain.ErrPlayerNotFound
		}
		if username == current.Username {
			return nil
		}
		return r.renamePlayerUsername(txCtx, q, id, current.Username, username)
	})
}

func (r *PlayerPostgres) renamePlayerUsername(
	ctx context.Context,
	q *sqlc.Queries,
	id uuid.UUID,
	currentUsername string,
	username string,
) error {
	accountLinked, err := q.PlayerHasAccount(ctx, uuid.NullUUID{UUID: id, Valid: true})
	if err != nil {
		return fmt.Errorf("PlayerPostgres - UpdateUsername - PlayerHasAccount: %w", err)
	}
	if accountLinked {
		return domain.ErrConflict
	}
	oldNormalized := strings.ToLower(currentUsername)
	newNormalized := strings.ToLower(username)
	if err := lockUsernames(ctx, q, oldNormalized, newNormalized); err != nil {
		return fmt.Errorf("PlayerPostgres - UpdateUsername - lock username: %w", err)
	}
	if newNormalized != oldNormalized {
		if err := reserveLegacyPlayerUsername(ctx, q, newNormalized); err != nil {
			return fmt.Errorf("PlayerPostgres - UpdateUsername - reserve username: %w", err)
		}
	}
	if _, err := q.UpdatePlayerUsername(ctx, sqlc.UpdatePlayerUsernameParams{ID: id, Username: username}); err != nil {
		return mapPlayerUsernameUpdateError(err)
	}
	if newNormalized != oldNormalized {
		if err := q.DecrementLegacyPlayerUsernameReservation(ctx, oldNormalized); err != nil {
			return fmt.Errorf("PlayerPostgres - UpdateUsername - release old username count: %w", err)
		}
	}
	return nil
}

func reserveLegacyPlayerUsername(ctx context.Context, q *sqlc.Queries, normalizedUsername string) error {
	reserved, err := q.CreateLegacyPlayerUsernameReservation(ctx, normalizedUsername)
	if err != nil {
		return fmt.Errorf("create legacy player username reservation: %w", err)
	}
	if reserved != 1 {
		return domain.ErrUsernameTaken
	}
	return nil
}

func mapPlayerUsernameUpdateError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrPlayerNotFound
	}
	if isUniqueViolation(err, playersUsernameUniqueConstraint) {
		return domain.WrapError(err, domain.ErrUsernameTaken)
	}
	return fmt.Errorf("PlayerPostgres - UpdateUsername - Querier.UpdatePlayerUsername: %w", err)
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
	return r.tx.Do(ctx, func(txCtx context.Context) error {
		q := r.tx.Querier(txCtx)
		current, err := q.GetPlayerForIdentityChange(txCtx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrPlayerNotFound
			}
			return fmt.Errorf("PlayerPostgres - SoftDeletePlayer - GetPlayerForIdentityChange: %w", err)
		}
		if current.DeletedAt.Valid {
			return domain.ErrConflict
		}
		oldNormalized := strings.ToLower(current.Username)
		newNormalized := strings.ToLower(deletedUsername)
		if err := lockUsernames(txCtx, q, oldNormalized, newNormalized); err != nil {
			return fmt.Errorf("PlayerPostgres - SoftDeletePlayer - lock username: %w", err)
		}
		if newNormalized != oldNormalized {
			reserved, err := q.CreateLegacyPlayerUsernameReservation(txCtx, newNormalized)
			if err != nil {
				return fmt.Errorf("PlayerPostgres - SoftDeletePlayer - reserve deleted username: %w", err)
			}
			if reserved != 1 {
				return domain.ErrUsernameTaken
			}
		}
		if _, err := q.SoftDeletePlayer(txCtx, sqlc.SoftDeletePlayerParams{
			ID:        id,
			Username:  deletedUsername,
			DeletedAt: tstz(deletedAt),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrConflict
			}
			if isUniqueViolation(err, playersUsernameUniqueConstraint) {
				return domain.WrapError(err, domain.ErrUsernameTaken)
			}
			return fmt.Errorf("PlayerPostgres - SoftDeletePlayer - Querier.SoftDeletePlayer: %w", err)
		}
		if newNormalized != oldNormalized {
			if err := q.DecrementLegacyPlayerUsernameReservation(txCtx, oldNormalized); err != nil {
				return fmt.Errorf("PlayerPostgres - SoftDeletePlayer - release old username count: %w", err)
			}
		}
		return nil
	})
}

func lockUsernames(ctx context.Context, q *sqlc.Queries, usernames ...string) error {
	unique := make(map[string]struct{}, len(usernames))
	ordered := make([]string, 0, len(usernames))
	for _, username := range usernames {
		if _, exists := unique[username]; exists {
			continue
		}
		unique[username] = struct{}{}
		ordered = append(ordered, username)
	}
	sort.Strings(ordered)
	for _, username := range ordered {
		if err := q.LockPlayerUsername(ctx, username); err != nil {
			return err
		}
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
