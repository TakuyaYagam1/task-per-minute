package authority

import authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"

func reconcileLease(
	lease authoritydomain.Lease,
	command ClaimCommand,
) (*authoritydomain.Lease, error) {
	if lease.Validate() != nil || lease.TournamentID != command.TournamentID ||
		lease.HolderID != command.HolderID || lease.LeaseID != command.LeaseID ||
		lease.CommandID != command.CommandID || lease.ProcessKind != command.ProcessKind ||
		!authoritydomain.StampsEqual(lease.Previous, command.Expected) {
		return nil, authoritydomain.ErrCommandReuse
	}
	clone := lease.Clone()
	return &clone, nil
}

func validCommittedLease(
	committed *authoritydomain.Lease,
	proposed authoritydomain.Lease,
	command ClaimCommand,
	changed bool,
) bool {
	if committed == nil || committed.Validate() != nil {
		return false
	}
	if changed {
		return leasesEqual(*committed, proposed)
	}
	_, err := reconcileLease(*committed, command)
	return err == nil
}

func leasesEqual(first, second authoritydomain.Lease) bool {
	return first.TournamentID == second.TournamentID &&
		first.HolderID == second.HolderID && first.LeaseID == second.LeaseID &&
		first.Epoch == second.Epoch && first.ProcessKind == second.ProcessKind &&
		first.Revision == second.Revision && first.CommandID == second.CommandID &&
		authoritydomain.StampsEqual(first.Previous, second.Previous) &&
		first.AcquiredAt.Equal(second.AcquiredAt) &&
		first.RenewedAt.Equal(second.RenewedAt) &&
		first.ExpiresAt.Equal(second.ExpiresAt)
}
