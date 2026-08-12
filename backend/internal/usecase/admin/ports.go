package admin

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type Clock interface {
	Now() time.Time
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type RevocationStore interface {
	Revoke(ctx context.Context, jti string, expiresAt time.Time) error
	IsRevoked(ctx context.Context, jti string) (bool, error)
}

type PlayerRepository interface {
	ListAdminPlayers(ctx context.Context, includeDeleted bool) ([]PlayerRecord, error)
	GetAdminPlayer(ctx context.Context, id uuid.UUID) (*PlayerRecord, error)
	GetAdminPlayerIncludingDeleted(ctx context.Context, id uuid.UUID) (*PlayerRecord, error)
	UpdateAdminPlayerUsername(ctx context.Context, id uuid.UUID, username string) error
	UpsertAdminPlayerStats(ctx context.Context, id uuid.UUID, in PlayerStatsInput, updatedAt time.Time) error
	SoftDeleteAdminPlayer(ctx context.Context, id uuid.UUID, deletedUsername string, deletedAt time.Time) error
	CreateAdminPlayerAudit(ctx context.Context, in PlayerAuditInput) error
	ListAdminPlayerAudit(ctx context.Context, playerID uuid.UUID, limit int32) ([]PlayerAuditEvent, error)
}

type LeaderboardInvalidator interface {
	Invalidate()
}

type UploadTaskRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Task, error)
	Update(ctx context.Context, id uuid.UUID, in TaskInput) (*domain.Task, error)
}

type TaskRepository interface {
	UploadTaskRepository
	Create(ctx context.Context, in TaskInput) (*domain.Task, error)
	List(ctx context.Context) ([]*domain.Task, error)
	Delete(ctx context.Context, id uuid.UUID) error
	IsUsedInActiveDuel(ctx context.Context, id uuid.UUID) (bool, error)
}

type SourceFileStorage interface {
	Upload(ctx context.Context, key string, r io.Reader, size int64) (string, error)
	PresignedGetURL(ctx context.Context, key string, ttl time.Duration) (string, error)
	Delete(ctx context.Context, key string) error
}
