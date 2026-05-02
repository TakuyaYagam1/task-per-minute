package apperr

import "errors"

type Code string

const (
	CodeInternal    Code = "internal"
	CodeValidation  Code = "validation"
	CodeConflict    Code = "conflict"
	CodeRateLimited Code = "rate_limited"

	CodePlayerNotFound  Code = "player.not_found"
	CodeUsernameTaken   Code = "player.username_taken"
	CodeUsernameInvalid Code = "player.username_invalid"
	CodePlayerInDuel    Code = "player.in_duel"
	CodeInvalidSession  Code = "player.invalid_session"

	CodeTaskNotFound   Code = "task.not_found"
	CodeTaskInUse      Code = "task.in_use"
	CodeTaskValidation Code = "task.validation"

	CodeDuelNotFound       Code = "duel.not_found"
	CodeDuelFinished       Code = "duel.finished"
	CodeDuelDeadlinePassed Code = "duel.deadline_passed"
	CodeFlagIncorrect      Code = "duel.flag_incorrect"
	CodeNotDuelParticipant Code = "duel.not_participant"

	CodeInvalidCredentials Code = "admin.invalid_credentials"
	CodeTokenExpired       Code = "admin.token_expired" //nolint:gosec // error code identifier, not a credential
	CodeTokenRevoked       Code = "admin.token_revoked" //nolint:gosec // error code identifier, not a credential
)

type Error struct {
	Code    Code
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
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return e.Code == t.Code
}

func Wrap(err error, app *Error) *Error {
	if app == nil {
		return nil
	}
	return &Error{Code: app.Code, Message: app.Message, Cause: err}
}

var (
	ErrInternal    = &Error{Code: CodeInternal, Message: "internal error"}
	ErrValidation  = &Error{Code: CodeValidation, Message: "validation failed"}
	ErrConflict    = &Error{Code: CodeConflict, Message: "conflict"}
	ErrRateLimited = &Error{Code: CodeRateLimited, Message: "too many requests"}
)
