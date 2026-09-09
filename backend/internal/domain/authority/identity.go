package authority

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var (
	ErrInvalid      = errors.New("invalid execution authority")
	ErrActive       = errors.New("execution authority lease is active")
	ErrConflict     = errors.New("execution authority conflict")
	ErrCommandReuse = errors.New("execution authority command was reused")
)

type Stamp struct {
	LeaseID uuid.UUID
	Epoch   int64
}

func (stamp Stamp) Validate() error {
	if stamp.LeaseID == uuid.Nil || stamp.Epoch < 1 {
		return invalid("invalid lease stamp")
	}
	return nil
}

type Identity struct {
	TournamentID uuid.UUID
	HolderID     uuid.UUID
	LeaseID      uuid.UUID
	Epoch        int64
	ProcessKind  ProcessKind
}

func (identity Identity) Validate() error {
	if identity.TournamentID == uuid.Nil || identity.HolderID == uuid.Nil ||
		identity.LeaseID == uuid.Nil || identity.Epoch < 1 ||
		identity.ProcessKind != ProcessAuthority {
		return invalid("invalid authority identity")
	}
	return nil
}

func (identity Identity) Stamp() Stamp {
	return Stamp{LeaseID: identity.LeaseID, Epoch: identity.Epoch}
}

func StampsEqual(first, second *Stamp) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func CloneStamp(stamp *Stamp) *Stamp {
	if stamp == nil {
		return nil
	}
	clone := *stamp
	return &clone
}

func invalid(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalid, message)
}
