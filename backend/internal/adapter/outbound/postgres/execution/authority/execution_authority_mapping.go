package authority

import (
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
)

func mapExecutionAuthorityLease(
	row sqlc.ExecutionAuthorityLease,
) (*authoritydomain.Lease, error) {
	if !row.AcquiredAt.Valid || !row.RenewedAt.Valid ||
		!row.ExpiresAt.Valid || !row.CreatedAt.Valid {
		return nil, domain.ErrInternal
	}
	lease := authoritydomain.Lease{
		TournamentID: row.TournamentID,
		HolderID:     row.HolderID,
		LeaseID:      row.LeaseID,
		Epoch:        row.Epoch,
		ProcessKind:  authoritydomain.ProcessKind(row.ProcessKind),
		Revision:     row.Revision,
		CommandID:    row.CommandID,
		AcquiredAt:   row.AcquiredAt.Time.UTC(),
		RenewedAt:    row.RenewedAt.Time.UTC(),
		ExpiresAt:    row.ExpiresAt.Time.UTC(),
	}
	previous, err := mapExecutionAuthorityPredecessor(row)
	if err != nil {
		return nil, err
	}
	lease.Previous = previous
	createdAt := row.CreatedAt.Time.UTC()
	if lease.Validate() != nil || !domain.IsValidServerTime(createdAt) ||
		createdAt.Before(lease.RenewedAt) {
		return nil, domain.ErrInternal
	}
	return &lease, nil
}

func mapExecutionAuthorityPredecessor(
	row sqlc.ExecutionAuthorityLease,
) (*authoritydomain.Stamp, error) {
	if row.Revision == 1 {
		if row.PreviousRevision != nil || row.PreviousLeaseID.Valid || row.PreviousEpoch != nil {
			return nil, domain.ErrInternal
		}
		return nil, nil
	}
	if row.PreviousRevision == nil || *row.PreviousRevision != row.Revision-1 ||
		!row.PreviousLeaseID.Valid || row.PreviousEpoch == nil {
		return nil, domain.ErrInternal
	}
	return &authoritydomain.Stamp{
		LeaseID: row.PreviousLeaseID.UUID,
		Epoch:   *row.PreviousEpoch,
	}, nil
}

func executionAuthorityTime(value pgtype.Timestamptz) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, domain.ErrInternal
	}
	authoritativeAt := value.Time.UTC()
	if !domain.IsValidServerTime(authoritativeAt) {
		return time.Time{}, domain.ErrInternal
	}
	return authoritativeAt, nil
}

func executionAuthorityCreateParams(
	lease authoritydomain.Lease,
	createdAt time.Time,
) sqlc.CreateExecutionAuthorityLeaseParams {
	params := sqlc.CreateExecutionAuthorityLeaseParams{
		TournamentID: lease.TournamentID,
		CommandID:    lease.CommandID,
		HolderID:     lease.HolderID,
		LeaseID:      lease.LeaseID,
		Epoch:        lease.Epoch,
		ProcessKind:  string(lease.ProcessKind),
		Revision:     lease.Revision,
		AcquiredAt:   tstz(lease.AcquiredAt),
		RenewedAt:    tstz(lease.RenewedAt),
		ExpiresAt:    tstz(lease.ExpiresAt),
		CreatedAt:    tstz(createdAt),
	}
	if lease.Previous != nil {
		previousRevision := lease.Revision - 1
		previousEpoch := lease.Previous.Epoch
		params.PreviousRevision = &previousRevision
		params.PreviousLeaseID = uuid.NullUUID{
			UUID:  lease.Previous.LeaseID,
			Valid: true,
		}
		params.PreviousEpoch = &previousEpoch
	}
	return params
}

func executionAuthorityCommitMatches(
	condition authorityusecase.CommitCondition,
	current *authoritydomain.Lease,
	proposed authoritydomain.Lease,
	authoritativeAt time.Time,
) bool {
	if condition.Validate() != nil || proposed.Validate() != nil ||
		!proposed.Proves(proposed.Identity(), authoritativeAt) {
		return false
	}
	if current == nil {
		return executionAuthorityInitialCommitMatches(condition, proposed)
	}
	if !executionAuthoritySuccessorMatches(condition, *current, proposed) {
		return false
	}
	switch condition.ExpectedState {
	case authorityusecase.ExpectedAbsent:
		return false
	case authorityusecase.ExpectedLive:
		return executionAuthorityRenewalMatches(*current, proposed, authoritativeAt)
	case authorityusecase.ExpectedExpired:
		return executionAuthorityTakeoverMatches(*current, proposed, authoritativeAt)
	default:
		return false
	}
}

func executionAuthorityInitialCommitMatches(
	condition authorityusecase.CommitCondition,
	proposed authoritydomain.Lease,
) bool {
	return condition.ExpectedState == authorityusecase.ExpectedAbsent &&
		proposed.Revision == 1 && proposed.Previous == nil &&
		proposed.AcquiredAt.Equal(proposed.RenewedAt)
}

func executionAuthoritySuccessorMatches(
	condition authorityusecase.CommitCondition,
	current authoritydomain.Lease,
	proposed authoritydomain.Lease,
) bool {
	return current.Validate() == nil && current.TournamentID == proposed.TournamentID &&
		current.Revision != math.MaxInt64 && proposed.Revision == current.Revision+1 &&
		condition.ExpectedRevision == current.Revision &&
		condition.ExpectedStamp == *current.Stamp() &&
		authoritydomain.StampsEqual(proposed.Previous, current.Stamp())
}

func executionAuthorityRenewalMatches(
	current authoritydomain.Lease,
	proposed authoritydomain.Lease,
	authoritativeAt time.Time,
) bool {
	return current.Proves(current.Identity(), authoritativeAt) &&
		proposed.HolderID == current.HolderID && proposed.LeaseID == current.LeaseID &&
		proposed.Epoch == current.Epoch && proposed.AcquiredAt.Equal(current.AcquiredAt) &&
		!proposed.RenewedAt.Before(current.RenewedAt)
}

func executionAuthorityTakeoverMatches(
	current authoritydomain.Lease,
	proposed authoritydomain.Lease,
	authoritativeAt time.Time,
) bool {
	return !authoritativeAt.Before(current.ExpiresAt) && proposed.LeaseID != current.LeaseID &&
		proposed.Epoch == current.Epoch+1 && proposed.AcquiredAt.Equal(proposed.RenewedAt) &&
		!proposed.RenewedAt.Before(current.ExpiresAt)
}

func executionAuthorityLeasesEqual(first, second authoritydomain.Lease) bool {
	return first.TournamentID == second.TournamentID &&
		first.HolderID == second.HolderID && first.LeaseID == second.LeaseID &&
		first.Epoch == second.Epoch && first.ProcessKind == second.ProcessKind &&
		first.Revision == second.Revision && first.CommandID == second.CommandID &&
		authoritydomain.StampsEqual(first.Previous, second.Previous) &&
		first.AcquiredAt.Equal(second.AcquiredAt) &&
		first.RenewedAt.Equal(second.RenewedAt) &&
		first.ExpiresAt.Equal(second.ExpiresAt)
}
