package avatar

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	avatarusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/avatar"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Repository struct {
	transactions *db.TxManager
}

var _ avatarusecase.Repository = (*Repository)(nil)
var _ avatarusecase.PublicRepository = (*Repository)(nil)

func NewRepository(transactions *db.TxManager) *Repository {
	return &Repository{transactions: transactions}
}

func (r *Repository) PrepareUpload(
	ctx context.Context,
	playerID, sessionToken uuid.UUID,
	objectKey string,
	cleanupAfter time.Time,
) (int64, error) {
	if r == nil || r.transactions == nil {
		return 0, domain.ErrInternal
	}
	var generation int64
	err := r.transactions.Do(ctx, func(txCtx context.Context) error {
		if err := r.lockSession(txCtx, playerID, sessionToken); err != nil {
			return err
		}
		var err error
		generation, err = r.transactions.Querier(txCtx).EnsurePlayerAvatarState(txCtx, playerID)
		if err != nil {
			return fmt.Errorf("ensure player avatar state: %w", err)
		}
		rows, err := r.transactions.Querier(txCtx).CreatePlayerAvatarUploadIntent(
			txCtx,
			sqlc.CreatePlayerAvatarUploadIntentParams{
				ObjectKey:    objectKey,
				PlayerID:     playerID,
				Generation:   generation,
				CleanupAfter: timestamp(cleanupAfter),
			},
		)
		if err != nil {
			return fmt.Errorf("create player avatar upload intent: %w", err)
		}
		if rows != 1 {
			return domain.ErrConflict
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return generation, nil
}

func (r *Repository) GetAvatar(
	ctx context.Context,
	playerID, sessionToken uuid.UUID,
) (*domain.PlayerAvatar, error) {
	if r == nil || r.transactions == nil {
		return nil, domain.ErrInternal
	}
	var result *domain.PlayerAvatar
	err := r.transactions.Do(ctx, func(txCtx context.Context) error {
		if err := r.lockSession(txCtx, playerID, sessionToken); err != nil {
			return err
		}
		row, err := r.transactions.Querier(txCtx).GetPlayerAvatar(txCtx, playerID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrAvatarNotFound
			}
			return fmt.Errorf("get player avatar metadata: %w", err)
		}
		result = &domain.PlayerAvatar{
			PlayerID:    row.PlayerID,
			ObjectKey:   row.ObjectKey,
			ContentType: row.ContentType,
			SizeBytes:   row.SizeBytes,
			SHA256:      append([]byte(nil), row.Sha256...),
			UpdatedAt:   row.UpdatedAt.Time.UTC(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) GetPublicAvatar(
	ctx context.Context,
	playerID uuid.UUID,
	versionSHA256 []byte,
) (*domain.PlayerAvatar, error) {
	if r == nil || r.transactions == nil {
		return nil, domain.ErrInternal
	}
	if playerID == uuid.Nil || len(versionSHA256) != sha256.Size {
		return nil, domain.ErrValidation
	}
	row, err := r.transactions.Querier(ctx).GetPublicPlayerAvatar(ctx, sqlc.GetPublicPlayerAvatarParams{
		PlayerID:      playerID,
		VersionSha256: append([]byte(nil), versionSHA256...),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrAvatarNotFound
		}
		return nil, fmt.Errorf("get public player avatar metadata: %w", err)
	}
	return &domain.PlayerAvatar{
		PlayerID:    row.PlayerID,
		ObjectKey:   row.ObjectKey,
		ContentType: row.ContentType,
		SizeBytes:   row.SizeBytes,
		SHA256:      append([]byte(nil), row.Sha256...),
		UpdatedAt:   row.UpdatedAt.Time.UTC(),
	}, nil
}

func (r *Repository) ActivateUpload(
	ctx context.Context,
	playerID, sessionToken uuid.UUID,
	objectKey, contentType string,
	sizeBytes int64,
	digest []byte,
	generation int64,
	updatedAt time.Time,
) error {
	if r == nil || r.transactions == nil {
		return domain.ErrInternal
	}
	if sizeBytes <= 0 || sizeBytes > 5<<20 || len(digest) != sha256.Size {
		return domain.ErrValidation
	}
	return r.transactions.Do(ctx, func(txCtx context.Context) error {
		if err := r.lockSession(txCtx, playerID, sessionToken); err != nil {
			return err
		}
		q := r.transactions.Querier(txCtx)
		currentGeneration, err := q.EnsurePlayerAvatarState(txCtx, playerID)
		if err != nil {
			return fmt.Errorf("read player avatar generation: %w", err)
		}
		if currentGeneration != generation {
			return domain.ErrConflict
		}
		if err := queueReplacedAvatar(txCtx, q, playerID, objectKey, updatedAt); err != nil {
			return err
		}
		return activateAvatar(txCtx, q, playerID, objectKey, contentType, sizeBytes, digest, generation, updatedAt)
	})
}

func queueReplacedAvatar(
	ctx context.Context,
	q *sqlc.Queries,
	playerID uuid.UUID,
	newObjectKey string,
	cleanupAt time.Time,
) error {
	old, err := q.GetPlayerAvatar(ctx, playerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load current player avatar: %w", err)
	}
	if old.ObjectKey == newObjectKey {
		return domain.ErrConflict
	}

	deleted, err := q.DeletePlayerAvatar(ctx, playerID)
	if err != nil {
		return fmt.Errorf("remove replaced player avatar metadata: %w", err)
	}
	if deleted != 1 {
		return domain.ErrConflict
	}
	queued, err := q.QueuePlayerAvatarObjectDeletion(ctx, sqlc.QueuePlayerAvatarObjectDeletionParams{
		CleanupAfter: timestamp(cleanupAt),
		ObjectKey:    old.ObjectKey,
	})
	if err != nil {
		return fmt.Errorf("queue replaced player avatar object: %w", err)
	}
	if queued != 1 {
		return domain.ErrConflict
	}
	return nil
}

func activateAvatar(
	ctx context.Context,
	q *sqlc.Queries,
	playerID uuid.UUID,
	objectKey, contentType string,
	sizeBytes int64,
	digest []byte,
	generation int64,
	updatedAt time.Time,
) error {
	activated, err := q.ActivatePlayerAvatarObject(ctx, sqlc.ActivatePlayerAvatarObjectParams{
		ObjectKey:  objectKey,
		PlayerID:   playerID,
		Generation: generation,
	})
	if err != nil {
		return fmt.Errorf("activate player avatar object: %w", err)
	}
	if activated != 1 {
		return domain.ErrConflict
	}
	if err := q.InsertPlayerAvatar(ctx, sqlc.InsertPlayerAvatarParams{
		PlayerID:    playerID,
		ObjectKey:   objectKey,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		Sha256:      append([]byte(nil), digest...),
		UpdatedAt:   timestamp(updatedAt),
	}); err != nil {
		return fmt.Errorf("save player avatar metadata: %w", err)
	}
	return nil
}

func (r *Repository) DeleteAvatar(
	ctx context.Context,
	playerID, sessionToken uuid.UUID,
	deletedAt time.Time,
) error {
	if r == nil || r.transactions == nil {
		return domain.ErrInternal
	}
	return r.transactions.Do(ctx, func(txCtx context.Context) error {
		if err := r.lockSession(txCtx, playerID, sessionToken); err != nil {
			return err
		}
		q := r.transactions.Querier(txCtx)
		if _, err := q.IncrementPlayerAvatarGeneration(txCtx, playerID); err != nil {
			return fmt.Errorf("invalidate player avatar uploads: %w", err)
		}
		current, err := q.GetPlayerAvatar(txCtx, playerID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("load player avatar before delete: %w", err)
		}
		deleted, err := q.DeletePlayerAvatar(txCtx, playerID)
		if err != nil {
			return fmt.Errorf("delete player avatar metadata: %w", err)
		}
		if deleted != 1 {
			return domain.ErrConflict
		}
		queued, err := q.QueuePlayerAvatarObjectDeletion(txCtx, sqlc.QueuePlayerAvatarObjectDeletionParams{
			CleanupAfter: timestamp(deletedAt),
			ObjectKey:    current.ObjectKey,
		})
		if err != nil {
			return fmt.Errorf("queue player avatar object cleanup: %w", err)
		}
		if queued != 1 {
			return domain.ErrConflict
		}
		return nil
	})
}

func (r *Repository) ClaimCleanup(
	ctx context.Context,
	now, leaseUntil time.Time,
	claimToken uuid.UUID,
	limit int32,
) ([]avatarusecase.CleanupObject, error) {
	if r == nil || r.transactions == nil || limit <= 0 || claimToken == uuid.Nil {
		return nil, domain.ErrValidation
	}
	rows, err := r.transactions.Querier(ctx).ClaimPlayerAvatarObjectCleanup(
		ctx,
		sqlc.ClaimPlayerAvatarObjectCleanupParams{
			ClaimToken: uuid.NullUUID{UUID: claimToken, Valid: true},
			LeaseUntil: timestamp(leaseUntil),
			Now:        timestamp(now),
			BatchSize:  limit,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("claim player avatar object cleanup: %w", err)
	}
	items := make([]avatarusecase.CleanupObject, 0, len(rows))
	for _, row := range rows {
		items = append(items, avatarusecase.CleanupObject{ObjectKey: row.ObjectKey, Attempts: row.CleanupAttempts})
	}
	return items, nil
}

func (r *Repository) CompleteCleanup(ctx context.Context, objectKey string, claimToken uuid.UUID) error {
	if r == nil || r.transactions == nil || claimToken == uuid.Nil {
		return domain.ErrValidation
	}
	rows, err := r.transactions.Querier(ctx).CompletePlayerAvatarObjectCleanup(
		ctx,
		sqlc.CompletePlayerAvatarObjectCleanupParams{
			ObjectKey:  objectKey,
			ClaimToken: uuid.NullUUID{UUID: claimToken, Valid: true},
		},
	)
	if err != nil {
		return fmt.Errorf("complete player avatar object cleanup: %w", err)
	}
	if rows != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (r *Repository) RetryCleanup(
	ctx context.Context,
	objectKey string,
	claimToken uuid.UUID,
	retryAt time.Time,
) error {
	if r == nil || r.transactions == nil || claimToken == uuid.Nil {
		return domain.ErrValidation
	}
	rows, err := r.transactions.Querier(ctx).RetryPlayerAvatarObjectCleanup(
		ctx,
		sqlc.RetryPlayerAvatarObjectCleanupParams{
			RetryAt:    timestamp(retryAt),
			ObjectKey:  objectKey,
			ClaimToken: uuid.NullUUID{UUID: claimToken, Valid: true},
		},
	)
	if err != nil {
		return fmt.Errorf("schedule player avatar object cleanup retry: %w", err)
	}
	if rows != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (r *Repository) lockSession(ctx context.Context, playerID, sessionToken uuid.UUID) error {
	if playerID == uuid.Nil || sessionToken == uuid.Nil {
		return domain.ErrInvalidSession
	}
	q := r.transactions.Querier(ctx)
	_, err := q.LockPlayerAvatarSession(ctx, sqlc.LockPlayerAvatarSessionParams{
		PlayerID:     playerID,
		SessionToken: uuid.NullUUID{UUID: sessionToken, Valid: true},
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lock player avatar session: %w", err)
	}
	hash := sha256.Sum256(sessionToken[:])
	deleted, err := q.IsDeletedPlayerAvatarSession(ctx, hash[:])
	if err != nil {
		return fmt.Errorf("check deleted player avatar session: %w", err)
	}
	if deleted {
		return domain.ErrAccountDeleted
	}
	return domain.ErrInvalidSession
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}
