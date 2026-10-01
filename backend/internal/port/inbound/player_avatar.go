package usecase

import (
	"context"

	"github.com/google/uuid"
)

// PlayerAvatarContent is the canonical avatar body served by private and
// public avatar reads. It deliberately contains no storage key or URL.
type PlayerAvatarContent struct {
	ContentType string
	Data        []byte
}

// PlayerAvatarService manages the avatar belonging to the authenticated
// player. The adapter supplies the player ID from session context and the
// actual session token from the signed cookie; neither is request data.
type PlayerAvatarService interface {
	GetAvatar(ctx context.Context, playerID, sessionToken uuid.UUID) (PlayerAvatarContent, error)
	ReplaceAvatar(
		ctx context.Context,
		playerID, sessionToken uuid.UUID,
		declaredContentType string,
		data []byte,
	) error
	DeleteAvatar(ctx context.Context, playerID, sessionToken uuid.UUID) error
}

// PublicPlayerAvatarService reads only the exact current avatar version for
// an active player. The public contract returns content, never storage metadata.
type PublicPlayerAvatarService interface {
	GetPublicAvatar(ctx context.Context, playerID uuid.UUID, version string) (PlayerAvatarContent, error)
}
