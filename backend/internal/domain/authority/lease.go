package authority

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type Lease struct {
	TournamentID uuid.UUID
	HolderID     uuid.UUID
	LeaseID      uuid.UUID
	Epoch        int64
	ProcessKind  ProcessKind
	Revision     int64
	CommandID    uuid.UUID
	Previous     *Stamp
	AcquiredAt   time.Time
	RenewedAt    time.Time
	ExpiresAt    time.Time
}

func (lease Lease) Validate() error {
	if err := validateLeaseIdentity(lease); err != nil {
		return err
	}
	if err := validateLeaseInterval(lease); err != nil {
		return err
	}
	return validateLeaseContinuity(lease)
}

func validateLeaseIdentity(lease Lease) error {
	if lease.TournamentID == uuid.Nil || lease.HolderID == uuid.Nil ||
		lease.LeaseID == uuid.Nil || lease.CommandID == uuid.Nil ||
		lease.Epoch < 1 || lease.Revision < lease.Epoch ||
		lease.ProcessKind != ProcessAuthority {
		return invalid("invalid lease identity")
	}
	return nil
}

func validateLeaseInterval(lease Lease) error {
	if !domain.IsValidServerTime(lease.AcquiredAt) ||
		!domain.IsValidServerTime(lease.RenewedAt) ||
		!domain.IsValidServerTime(lease.ExpiresAt) ||
		lease.RenewedAt.Before(lease.AcquiredAt) ||
		!lease.ExpiresAt.After(lease.RenewedAt) {
		return invalid("invalid lease interval")
	}
	return nil
}

func validateLeaseContinuity(lease Lease) error {
	if lease.Revision == 1 {
		if lease.Epoch != 1 || lease.Previous != nil {
			return invalid("initial lease has predecessor evidence")
		}
		return nil
	}
	if lease.Previous == nil || lease.Previous.Validate() != nil {
		return invalid("invalid predecessor lease stamp")
	}
	sameLeaseRenewal := lease.Previous.Epoch == lease.Epoch &&
		lease.Previous.LeaseID == lease.LeaseID
	freshLeaseAcquisition := lease.Previous.Epoch == lease.Epoch-1 &&
		lease.Previous.LeaseID != lease.LeaseID
	if !sameLeaseRenewal && !freshLeaseAcquisition {
		return invalid("predecessor does not prove lease continuity")
	}
	return nil
}

func (lease Lease) Identity() Identity {
	return Identity{
		TournamentID: lease.TournamentID,
		HolderID:     lease.HolderID,
		LeaseID:      lease.LeaseID,
		Epoch:        lease.Epoch,
		ProcessKind:  lease.ProcessKind,
	}
}

func (lease Lease) Stamp() *Stamp {
	return &Stamp{LeaseID: lease.LeaseID, Epoch: lease.Epoch}
}

func (lease Lease) Proves(identity Identity, now time.Time) bool {
	return lease.Validate() == nil && identity.Validate() == nil &&
		lease.Identity() == identity && domain.IsValidServerTime(now) &&
		!now.Before(lease.RenewedAt) && now.Before(lease.ExpiresAt)
}

func (lease Lease) Clone() Lease {
	clone := lease
	clone.Previous = CloneStamp(lease.Previous)
	return clone
}

func CloneLease(lease *Lease) *Lease {
	if lease == nil {
		return nil
	}
	clone := lease.Clone()
	return &clone
}
