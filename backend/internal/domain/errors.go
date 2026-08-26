package domain

import "errors"

// ErrorCode is a stable machine-readable application error identifier.
type ErrorCode string

const (
	ErrorCodeInternal   ErrorCode = "internal"
	ErrorCodeValidation ErrorCode = "validation"
	ErrorCodeConflict   ErrorCode = "conflict"
	ErrorCodeRateLimit  ErrorCode = "rate_limited"

	ErrorCodePlayerNotFound  ErrorCode = "player.not_found"
	ErrorCodeUsernameTaken   ErrorCode = "player.username_taken"
	ErrorCodeUsernameInvalid ErrorCode = "player.username_invalid"
	ErrorCodePlayerInDuel    ErrorCode = "player.in_duel"
	ErrorCodePlayerQueued    ErrorCode = "player.queued"
	ErrorCodeInvalidSession  ErrorCode = "player.invalid_session"

	ErrorCodeTaskNotFound   ErrorCode = "task.not_found"
	ErrorCodeTaskInUse      ErrorCode = "task.in_use"
	ErrorCodeTaskValidation ErrorCode = "task.validation"

	ErrorCodeDuelNotFound       ErrorCode = "duel.not_found"
	ErrorCodeDuelFinished       ErrorCode = "duel.finished"
	ErrorCodeDuelDeadlinePassed ErrorCode = "duel.deadline_passed"
	ErrorCodeFlagIncorrect      ErrorCode = "duel.flag_incorrect"
	ErrorCodeNotDuelParticipant ErrorCode = "duel.not_participant"

	ErrorCodeInvalidCredentials ErrorCode = "admin.invalid_credentials"
	ErrorCodeTokenExpired       ErrorCode = "admin.token_expired" //nolint:gosec // error code identifier, not a credential
	ErrorCodeTokenRevoked       ErrorCode = "admin.token_revoked" //nolint:gosec // error code identifier, not a credential
)

// Error describes a safe application error that can cross adapter boundaries.
type Error struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *Error) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}

	var other *Error
	if !errors.As(target, &other) {
		return false
	}
	return e.Code == other.Code
}

// WrapError preserves a lower-level cause while exposing a safe domain error.
func WrapError(err error, domainErr *Error) *Error {
	if domainErr == nil {
		return nil
	}
	return &Error{Code: domainErr.Code, Message: domainErr.Message, Cause: err}
}

var (
	ErrInternal    = &Error{Code: ErrorCodeInternal, Message: "internal error"}
	ErrValidation  = &Error{Code: ErrorCodeValidation, Message: "validation failed"}
	ErrConflict    = &Error{Code: ErrorCodeConflict, Message: "conflict"}
	ErrRateLimited = &Error{Code: ErrorCodeRateLimit, Message: "too many requests"}

	ErrPlayerNotFound  = &Error{Code: ErrorCodePlayerNotFound, Message: "player not found"}
	ErrUsernameTaken   = &Error{Code: ErrorCodeUsernameTaken, Message: "username already taken"}
	ErrUsernameInvalid = &Error{Code: ErrorCodeUsernameInvalid, Message: "username is invalid"}
	ErrPlayerInDuel    = &Error{Code: ErrorCodePlayerInDuel, Message: "player is already in an active duel"}
	ErrPlayerQueued    = &Error{Code: ErrorCodePlayerQueued, Message: "player is already waiting in queue"}
	ErrInvalidSession  = &Error{Code: ErrorCodeInvalidSession, Message: "invalid session token"}

	ErrTaskNotFound   = &Error{Code: ErrorCodeTaskNotFound, Message: "task not found"}
	ErrTaskInUse      = &Error{Code: ErrorCodeTaskInUse, Message: "task is in use by an active duel"}
	ErrTaskValidation = &Error{Code: ErrorCodeTaskValidation, Message: "task validation failed"}

	ErrDuelNotFound       = &Error{Code: ErrorCodeDuelNotFound, Message: "duel not found"}
	ErrDuelFinished       = &Error{Code: ErrorCodeDuelFinished, Message: "duel is already finished"}
	ErrDuelDeadlinePassed = &Error{Code: ErrorCodeDuelDeadlinePassed, Message: "duel deadline has passed"}
	ErrFlagIncorrect      = &Error{Code: ErrorCodeFlagIncorrect, Message: "flag is incorrect"}
	ErrNotDuelParticipant = &Error{Code: ErrorCodeNotDuelParticipant, Message: "player is not a participant of this duel"}

	ErrInvalidCredentials = &Error{Code: ErrorCodeInvalidCredentials, Message: "invalid credentials"}
	ErrTokenExpired       = &Error{Code: ErrorCodeTokenExpired, Message: "token expired"}
	ErrTokenRevoked       = &Error{Code: ErrorCodeTokenRevoked, Message: "token revoked"}
)
