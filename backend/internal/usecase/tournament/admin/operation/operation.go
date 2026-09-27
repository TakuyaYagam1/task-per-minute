package operation

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// OperatorIdentity identifies the authenticated operator issuing a command.
// The inbound adapter derives it from the authenticated session.
type OperatorIdentity struct {
	ActorID uuid.UUID
}

// RevisionConflictError reports a stale projection revision while preserving
// the domain conflict identity for callers that use errors.Is.
type RevisionConflictError struct {
	ExpectedRevision int64
	CurrentRevision  int64
	CurrentState     domain.TournamentState
	Detail           string
}

func (e *RevisionConflictError) Error() string { return "tournament projection revision conflict" }
func (e *RevisionConflictError) Unwrap() error { return domain.ErrConflict }

type CommandScope struct {
	Operator     OperatorIdentity
	TournamentID uuid.UUID
	CommandID    uuid.UUID
}
