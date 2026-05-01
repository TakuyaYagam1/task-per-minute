package apperr

var (
	ErrInvalidCredentials = &Error{Code: CodeInvalidCredentials, Message: "invalid credentials"}
	ErrTokenExpired       = &Error{Code: CodeTokenExpired, Message: "token expired"}
	ErrTokenRevoked       = &Error{Code: CodeTokenRevoked, Message: "token revoked"}
)
