package apperr

var (
	ErrDuelNotFound       = &Error{Code: CodeDuelNotFound, Message: "duel not found"}
	ErrDuelFinished       = &Error{Code: CodeDuelFinished, Message: "duel is already finished"}
	ErrDuelDeadlinePassed = &Error{Code: CodeDuelDeadlinePassed, Message: "duel deadline has passed"}
	ErrFlagIncorrect      = &Error{Code: CodeFlagIncorrect, Message: "flag is incorrect"}
	ErrNotDuelParticipant = &Error{Code: CodeNotDuelParticipant, Message: "player is not a participant of this duel"}
)
