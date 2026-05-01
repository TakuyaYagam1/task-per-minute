package apperr

var (
	ErrTaskNotFound   = &Error{Code: CodeTaskNotFound, Message: "task not found"}
	ErrTaskInUse      = &Error{Code: CodeTaskInUse, Message: "task is in use by an active duel"}
	ErrTaskValidation = &Error{Code: CodeTaskValidation, Message: "task validation failed"}
)
