package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	ErrorCodePlayerNotFound  ErrorCode = "player.not_found"
	ErrorCodeUsernameTaken   ErrorCode = "player.username_taken"
	ErrorCodeUsernameInvalid ErrorCode = "player.username_invalid"
	ErrorCodePlayerInDuel    ErrorCode = "player.in_duel"
	ErrorCodePlayerQueued    ErrorCode = "player.queued"
	ErrorCodeInvalidSession  ErrorCode = "player.invalid_session"
)

var (
	ErrPlayerNotFound  = &Error{Code: ErrorCodePlayerNotFound, Message: "player not found"}
	ErrUsernameTaken   = &Error{Code: ErrorCodeUsernameTaken, Message: "username already taken"}
	ErrUsernameInvalid = &Error{Code: ErrorCodeUsernameInvalid, Message: "username is invalid"}
	ErrPlayerInDuel    = &Error{Code: ErrorCodePlayerInDuel, Message: "player is already in an active duel"}
	ErrPlayerQueued    = &Error{Code: ErrorCodePlayerQueued, Message: "player is already waiting in queue"}
	ErrInvalidSession  = &Error{Code: ErrorCodeInvalidSession, Message: "invalid session token"}
)

type PlayerStatus string

const (
	PlayerStatusIdle   PlayerStatus = "idle"
	PlayerStatusQueued PlayerStatus = "queued"
	PlayerStatusInDuel PlayerStatus = "in_duel"
)

func (s PlayerStatus) IsValid() bool {
	switch s {
	case PlayerStatusIdle, PlayerStatusQueued, PlayerStatusInDuel:
		return true
	}
	return false
}

func (s PlayerStatus) String() string {
	return string(s)
}

type Player struct {
	ID               uuid.UUID
	Username         string
	SessionToken     *uuid.UUID
	SessionExpiresAt *time.Time
	Status           PlayerStatus
	CreatedAt        time.Time
}
