package authority

import (
	"math"
	"time"

	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

func (usecase *UseCase) buildLease(
	command ClaimCommand,
	current *authoritydomain.Lease,
	now time.Time,
) (authoritydomain.Lease, CommitCondition, error) {
	lease := authoritydomain.Lease{
		TournamentID: command.TournamentID,
		HolderID:     command.HolderID,
		LeaseID:      command.LeaseID,
		Epoch:        1,
		ProcessKind:  command.ProcessKind,
		Revision:     1,
		CommandID:    command.CommandID,
		AcquiredAt:   now,
		RenewedAt:    now,
		ExpiresAt:    now.Add(usecase.leaseDuration),
	}
	if current == nil {
		if command.Expected != nil {
			return authoritydomain.Lease{}, CommitCondition{}, authoritydomain.ErrConflict
		}
		if err := lease.Validate(); err != nil {
			return authoritydomain.Lease{}, CommitCondition{}, err
		}
		return lease, CommitCondition{ExpectedState: ExpectedAbsent}, nil
	}
	if !authoritydomain.StampsEqual(command.Expected, current.Stamp()) {
		return authoritydomain.Lease{}, CommitCondition{}, authoritydomain.ErrConflict
	}
	if now.Before(current.RenewedAt) || current.Revision == math.MaxInt64 {
		return authoritydomain.Lease{}, CommitCondition{}, authoritydomain.ErrConflict
	}
	condition := CommitCondition{
		ExpectedRevision: current.Revision,
		ExpectedStamp:    *current.Stamp(),
		ExpectedState:    ExpectedLive,
	}
	lease.Revision = current.Revision + 1
	lease.Previous = current.Stamp()
	if now.Before(current.ExpiresAt) {
		if current.HolderID != command.HolderID || current.LeaseID != command.LeaseID {
			return authoritydomain.Lease{}, CommitCondition{}, authoritydomain.ErrActive
		}
		lease.Epoch = current.Epoch
		lease.AcquiredAt = current.AcquiredAt
	} else {
		if current.Epoch == math.MaxInt64 || command.LeaseID == current.LeaseID {
			return authoritydomain.Lease{}, CommitCondition{}, authoritydomain.ErrConflict
		}
		condition.ExpectedState = ExpectedExpired
		lease.Epoch = current.Epoch + 1
	}
	if err := lease.Validate(); err != nil {
		return authoritydomain.Lease{}, CommitCondition{}, err
	}
	if err := condition.Validate(); err != nil {
		return authoritydomain.Lease{}, CommitCondition{}, err
	}
	return lease, condition, nil
}
