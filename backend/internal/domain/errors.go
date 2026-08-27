package domain

import "errors"

// ErrorCode is a stable machine-readable application error identifier.
type ErrorCode string

const (
	ErrorCodeInternal   ErrorCode = "internal"
	ErrorCodeValidation ErrorCode = "validation"
	ErrorCodeConflict   ErrorCode = "conflict"
	ErrorCodeRateLimit  ErrorCode = "rate_limited"

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

	ErrInvalidCredentials = &Error{Code: ErrorCodeInvalidCredentials, Message: "invalid credentials"}
	ErrTokenExpired       = &Error{Code: ErrorCodeTokenExpired, Message: "token expired"}
	ErrTokenRevoked       = &Error{Code: ErrorCodeTokenRevoked, Message: "token revoked"}
)
