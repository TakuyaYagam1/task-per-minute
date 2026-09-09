package authority

import (
	"github.com/google/uuid"

	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

type ClaimCommand struct {
	TournamentID uuid.UUID
	HolderID     uuid.UUID
	LeaseID      uuid.UUID
	CommandID    uuid.UUID
	ProcessKind  authoritydomain.ProcessKind
	Expected     *authoritydomain.Stamp
}

type ExpectedState string

const (
	ExpectedAbsent  ExpectedState = "absent"
	ExpectedLive    ExpectedState = "live"
	ExpectedExpired ExpectedState = "expired"
)

type CommitCondition struct {
	ExpectedRevision int64
	ExpectedStamp    authoritydomain.Stamp
	ExpectedState    ExpectedState
}

func (condition CommitCondition) Validate() error {
	switch condition.ExpectedState {
	case ExpectedAbsent:
		if condition.ExpectedRevision != 0 ||
			condition.ExpectedStamp != (authoritydomain.Stamp{}) {
			return invalidClaim("invalid absent commit condition")
		}
	case ExpectedLive, ExpectedExpired:
		if condition.ExpectedRevision < 1 || condition.ExpectedStamp.Validate() != nil {
			return invalidClaim("invalid existing lease commit condition")
		}
	default:
		return invalidClaim("unknown commit condition state")
	}
	return nil
}

func validateClaimCommand(command ClaimCommand) error {
	if command.TournamentID == uuid.Nil || command.HolderID == uuid.Nil ||
		command.LeaseID == uuid.Nil || command.CommandID == uuid.Nil ||
		command.ProcessKind != authoritydomain.ProcessAuthority {
		return invalidClaim("invalid claim command")
	}
	if command.Expected != nil && command.Expected.Validate() != nil {
		return invalidClaim("invalid expected lease stamp")
	}
	return nil
}

func cloneClaimCommand(command ClaimCommand) ClaimCommand {
	clone := command
	clone.Expected = authoritydomain.CloneStamp(command.Expected)
	return clone
}
