package avatar

import (
	"context"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

type Clock interface {
	Now() time.Time
}

type Repository interface {
	PrepareUpload(
		ctx context.Context,
		playerID, sessionToken uuid.UUID,
		objectKey string,
		cleanupAfter time.Time,
	) (int64, error)
	GetAvatar(ctx context.Context, playerID, sessionToken uuid.UUID) (*domain.PlayerAvatar, error)
	ActivateUpload(
		ctx context.Context,
		playerID, sessionToken uuid.UUID,
		objectKey, contentType string,
		sizeBytes int64,
		sha256 []byte,
		generation int64,
		updatedAt time.Time,
	) error
	DeleteAvatar(ctx context.Context, playerID, sessionToken uuid.UUID, deletedAt time.Time) error
	ClaimCleanup(
		ctx context.Context,
		now, leaseUntil time.Time,
		claimToken uuid.UUID,
		limit int32,
	) ([]CleanupObject, error)
	CompleteCleanup(ctx context.Context, objectKey string, claimToken uuid.UUID) error
	RetryCleanup(ctx context.Context, objectKey string, claimToken uuid.UUID, retryAt time.Time) error
}

// PublicRepository resolves only an active player's current avatar when its
// canonical digest matches the requested public version.
type PublicRepository interface {
	GetPublicAvatar(ctx context.Context, playerID uuid.UUID, versionSHA256 []byte) (*domain.PlayerAvatar, error)
}

type ObjectStorage interface {
	PutAvatar(ctx context.Context, objectKey string, data []byte, contentType string) error
	GetAvatar(ctx context.Context, objectKey string, maxBytes int64) ([]byte, error)
	DeleteAvatar(ctx context.Context, objectKey string) error
}

// VideoProcessor validates and re-encodes an MP4 avatar into the canonical
// bounded format stored by this use case.
type VideoProcessor interface {
	CanonicalizeMP4(ctx context.Context, input []byte) ([]byte, error)
}

type CleanupObject struct {
	ObjectKey string
	Attempts  int32
}
