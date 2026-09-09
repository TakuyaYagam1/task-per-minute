package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	ErrorCodePlayerNotFound  ErrorCode = "player.not_found"
	ErrorCodeUsernameTaken   ErrorCode = "player.username_taken"
	ErrorCodeUsernameInvalid ErrorCode = "player.username_invalid"
	ErrorCodeInvalidSession  ErrorCode = "player.invalid_session"
)

var (
	ErrPlayerNotFound  = &Error{Code: ErrorCodePlayerNotFound, Message: "player not found"}
	ErrUsernameTaken   = &Error{Code: ErrorCodeUsernameTaken, Message: "username already taken"}
	ErrUsernameInvalid = &Error{Code: ErrorCodeUsernameInvalid, Message: "username is invalid"}
	ErrInvalidSession  = &Error{Code: ErrorCodeInvalidSession, Message: "invalid session token"}
)

type Player struct {
	ID               uuid.UUID
	Username         string
	SessionToken     *uuid.UUID
	SessionExpiresAt *time.Time
	CreatedAt        time.Time
}
