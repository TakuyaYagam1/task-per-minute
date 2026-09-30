package domain

const (
	ErrorCodeEmailTaken               ErrorCode = "player_account.email_taken"
	ErrorCodeVerificationTokenInvalid ErrorCode = "player_account.verification_token_invalid"
	ErrorCodeVerificationUnavailable  ErrorCode = "player_account.verification_unavailable"
	ErrorCodePlayerJoinRetired        ErrorCode = "player_account.join_retired"
)

var (
	ErrEmailTaken               = &Error{Code: ErrorCodeEmailTaken, Message: "email already registered"}
	ErrVerificationTokenInvalid = &Error{Code: ErrorCodeVerificationTokenInvalid, Message: "verification token is invalid or expired"}
	ErrVerificationUnavailable  = &Error{Code: ErrorCodeVerificationUnavailable, Message: "email verification is temporarily unavailable"}
	ErrPlayerJoinRetired        = &Error{Code: ErrorCodePlayerJoinRetired, Message: "nickname-only player sessions are retired"}
)
