package apperr

var (
	ErrPlayerNotFound  = &Error{Code: CodePlayerNotFound, Message: "player not found"}
	ErrUsernameTaken   = &Error{Code: CodeUsernameTaken, Message: "username already taken"}
	ErrUsernameInvalid = &Error{Code: CodeUsernameInvalid, Message: "username is invalid"}
	ErrPlayerInDuel    = &Error{Code: CodePlayerInDuel, Message: "player is already in an active duel"}
	ErrInvalidSession  = &Error{Code: CodeInvalidSession, Message: "invalid session token"}
)
