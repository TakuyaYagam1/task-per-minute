package domain

import "time"

type RetryAfterError struct {
	cause error
	wait  time.Duration
}

func (e *RetryAfterError) Error() string {
	if e == nil || e.cause == nil {
		return ""
	}
	return e.cause.Error()
}

func (e *RetryAfterError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *RetryAfterError) RetryAfter() time.Duration {
	if e == nil || e.wait < 0 {
		return 0
	}
	return e.wait
}

func WithRetryAfter(err error, wait time.Duration) error {
	if err == nil {
		return nil
	}
	if wait < 0 {
		wait = 0
	}
	return &RetryAfterError{cause: err, wait: wait}
}

const (
	ErrorCodeEmailAlreadyVerified        ErrorCode = "player.email_already_verified"
	ErrorCodeEmailUnverified             ErrorCode = "player.email_unverified"
	ErrorCodeEmailTaken                  ErrorCode = "player_account.email_taken"
	ErrorCodeVerificationTokenInvalid    ErrorCode = "player_account.verification_token_invalid"
	ErrorCodeVerificationUnavailable     ErrorCode = "player_account.verification_unavailable"
	ErrorCodePlayerJoinRetired           ErrorCode = "player_account.join_retired"
	ErrorCodeAccountDeleted              ErrorCode = "player.account_deleted"
	ErrorCodeCurrentPasswordInvalid      ErrorCode = "player.current_password_invalid"
	ErrorCodeEmailChangeCodeInvalid      ErrorCode = "player.email_change_code_invalid"
	ErrorCodeEmailChangeAttemptsExceeded ErrorCode = "player.email_change_attempts_exceeded"
	ErrorCodeEmailChangeRateLimited      ErrorCode = "player.email_change_rate_limited"
	ErrorCodeEmailChangeUnavailable      ErrorCode = "player.email_change_unavailable"
)

var (
	ErrEmailAlreadyVerified        = &Error{Code: ErrorCodeEmailAlreadyVerified, Message: "email address is already verified"}
	ErrEmailUnverified             = &Error{Code: ErrorCodeEmailUnverified, Message: "activate your account by verifying your email before signing in"}
	ErrEmailTaken                  = &Error{Code: ErrorCodeEmailTaken, Message: "email already registered"}
	ErrVerificationTokenInvalid    = &Error{Code: ErrorCodeVerificationTokenInvalid, Message: "verification token is invalid or expired"}
	ErrVerificationUnavailable     = &Error{Code: ErrorCodeVerificationUnavailable, Message: "email verification is temporarily unavailable"}
	ErrPlayerJoinRetired           = &Error{Code: ErrorCodePlayerJoinRetired, Message: "nickname-only player sessions are retired"}
	ErrAccountDeleted              = &Error{Code: ErrorCodeAccountDeleted, Message: "this player account was deleted"}
	ErrCurrentPasswordInvalid      = &Error{Code: ErrorCodeCurrentPasswordInvalid, Message: "current password is incorrect"}
	ErrEmailChangeCodeInvalid      = &Error{Code: ErrorCodeEmailChangeCodeInvalid, Message: "email confirmation code is invalid or expired"}
	ErrEmailChangeAttemptsExceeded = &Error{Code: ErrorCodeEmailChangeAttemptsExceeded, Message: "too many email confirmation attempts"}
	ErrEmailChangeRateLimited      = &Error{Code: ErrorCodeEmailChangeRateLimited, Message: "email change is temporarily rate limited"}
	ErrEmailChangeUnavailable      = &Error{Code: ErrorCodeEmailChangeUnavailable, Message: "email change is temporarily unavailable"}
)

type AccountSettings struct {
	Username               string
	Email                  string
	PendingEmail           *string
	PendingEmailExpiresAt  *time.Time
	EmailResendAvailableAt *time.Time
}

type EmailChangeResult struct {
	Player                *Player
	Email                 string
	PreviousEmailNotified bool
}
