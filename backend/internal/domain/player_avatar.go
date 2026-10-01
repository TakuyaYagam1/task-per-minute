package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	ErrorCodeAvatarNotFound             ErrorCode = "player.avatar_not_found"
	ErrorCodeAvatarInvalid              ErrorCode = "player.avatar_invalid"
	ErrorCodeAvatarTooLarge             ErrorCode = "player.avatar_too_large"
	ErrorCodeAvatarUnsupportedMediaType ErrorCode = "player.avatar_unsupported_media_type"
	ErrorCodeAvatarBusy                 ErrorCode = "player.avatar_busy"
	ErrorCodeAvatarVideoUnavailable     ErrorCode = "player.avatar_video_unavailable"
)

var (
	ErrAvatarNotFound             = &Error{Code: ErrorCodeAvatarNotFound, Message: "player avatar not found"}
	ErrAvatarInvalid              = &Error{Code: ErrorCodeAvatarInvalid, Message: "player avatar is invalid"}
	ErrAvatarTooLarge             = &Error{Code: ErrorCodeAvatarTooLarge, Message: "player avatar is too large"}
	ErrAvatarUnsupportedMediaType = &Error{Code: ErrorCodeAvatarUnsupportedMediaType, Message: "player avatar media type is not supported"}
	ErrAvatarBusy                 = &Error{Code: ErrorCodeAvatarBusy, Message: "player avatar processing is busy"}
	ErrAvatarVideoUnavailable     = &Error{Code: ErrorCodeAvatarVideoUnavailable, Message: "video avatar processing is unavailable"}
)

// PlayerAvatar contains private storage metadata. ObjectKey and SHA256 must
// never be returned by an HTTP adapter.
type PlayerAvatar struct {
	PlayerID    uuid.UUID
	ObjectKey   string
	ContentType string
	SizeBytes   int64
	SHA256      []byte
	UpdatedAt   time.Time
}
