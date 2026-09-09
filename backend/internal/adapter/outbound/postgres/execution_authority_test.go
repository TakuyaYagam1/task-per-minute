package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
)

func TestMapExecutionAuthorityLease(t *testing.T) {
	t.Parallel()

	initial := executionAuthorityRow(executionAuthorityLease())
	mapped, err := mapExecutionAuthorityLease(initial)
	require.NoError(t, err)
	require.True(t, executionAuthorityLeasesEqual(*mapped, executionAuthorityLease()))

	t.Run("maps predecessor evidence for a renewal", func(t *testing.T) {
		t.Parallel()

		renewal := executionAuthorityRenewal()
		row := executionAuthorityRow(renewal)
		mapped, err := mapExecutionAuthorityLease(row)
		require.NoError(t, err)
		require.True(t, executionAuthorityLeasesEqual(*mapped, renewal))
		require.NotSame(t, renewal.Previous, mapped.Previous)
	})

	t.Run("rejects incomplete lineage and invalid persistence time", func(t *testing.T) {
		t.Parallel()

		row := executionAuthorityRow(executionAuthorityRenewal())
		row.PreviousEpoch = nil
		_, err := mapExecutionAuthorityLease(row)
		require.ErrorIs(t, err, domain.ErrInternal)

		row = executionAuthorityRow(executionAuthorityLease())
		row.CreatedAt = pgtype.Timestamptz{}
		_, err = mapExecutionAuthorityLease(row)
		require.ErrorIs(t, err, domain.ErrInternal)
	})
}

func TestExecutionAuthorityCommitMatches(t *testing.T) {
	t.Parallel()

	initial := executionAuthorityLease()
	require.True(t, executionAuthorityCommitMatches(
		authorityusecase.CommitCondition{ExpectedState: authorityusecase.ExpectedAbsent},
		nil,
		initial,
		initial.RenewedAt,
	))
	require.False(t, executionAuthorityCommitMatches(
		authorityusecase.CommitCondition{ExpectedState: authorityusecase.ExpectedAbsent},
		nil,
		initial,
		initial.ExpiresAt,
	))

	t.Run("renews only the live matching lease", func(t *testing.T) {
		t.Parallel()

		current := executionAuthorityLease()
		proposed := executionAuthorityRenewal()
		condition := authorityusecase.CommitCondition{
			ExpectedRevision: current.Revision,
			ExpectedStamp:    *current.Stamp(),
			ExpectedState:    authorityusecase.ExpectedLive,
		}
		require.True(t, executionAuthorityCommitMatches(
			condition,
			&current,
			proposed,
			proposed.RenewedAt,
		))

		wrongHolder := proposed
		wrongHolder.HolderID = executionAuthorityID(9)
		require.False(t, executionAuthorityCommitMatches(
			condition,
			&current,
			wrongHolder,
			proposed.RenewedAt,
		))
		require.False(t, executionAuthorityCommitMatches(
			condition,
			&current,
			proposed,
			current.ExpiresAt,
		))
	})

	t.Run("takes over only after expiry with the next epoch", func(t *testing.T) {
		t.Parallel()

		current := executionAuthorityLease()
		takeover := authoritydomain.Lease{
			TournamentID: current.TournamentID,
			HolderID:     executionAuthorityID(5),
			LeaseID:      executionAuthorityID(6),
			Epoch:        2,
			ProcessKind:  authoritydomain.ProcessAuthority,
			Revision:     2,
			CommandID:    executionAuthorityID(7),
			Previous:     current.Stamp(),
			AcquiredAt:   current.ExpiresAt,
			RenewedAt:    current.ExpiresAt,
			ExpiresAt:    current.ExpiresAt.Add(time.Minute),
		}
		condition := authorityusecase.CommitCondition{
			ExpectedRevision: current.Revision,
			ExpectedStamp:    *current.Stamp(),
			ExpectedState:    authorityusecase.ExpectedExpired,
		}
		require.True(t, executionAuthorityCommitMatches(
			condition,
			&current,
			takeover,
			current.ExpiresAt,
		))
		require.False(t, executionAuthorityCommitMatches(
			condition,
			&current,
			takeover,
			current.ExpiresAt.Add(-time.Nanosecond),
		))

		reusedLease := takeover
		reusedLease.LeaseID = current.LeaseID
		require.False(t, executionAuthorityCommitMatches(
			condition,
			&current,
			reusedLease,
			current.ExpiresAt,
		))
	})
}

func TestExecutionAuthorityCreateParams(t *testing.T) {
	t.Parallel()

	lease := executionAuthorityRenewal()
	createdAt := lease.RenewedAt.Add(time.Second)
	params := executionAuthorityCreateParams(lease, createdAt)
	require.Equal(t, lease.Revision-1, *params.PreviousRevision)
	require.Equal(t, lease.Previous.LeaseID, params.PreviousLeaseID.UUID)
	require.True(t, params.PreviousLeaseID.Valid)
	require.Equal(t, lease.Previous.Epoch, *params.PreviousEpoch)
	require.Equal(t, createdAt, params.CreatedAt.Time)

	params = executionAuthorityCreateParams(executionAuthorityLease(), createdAt)
	require.Nil(t, params.PreviousRevision)
	require.False(t, params.PreviousLeaseID.Valid)
	require.Nil(t, params.PreviousEpoch)
}

func TestExecutionAuthorityPostgresRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	lease := executionAuthorityLease()
	condition := authorityusecase.CommitCondition{ExpectedState: authorityusecase.ExpectedAbsent}
	var repository *ExecutionAuthorityPostgres

	_, err := repository.FindAuthorityCommand(context.Background(), lease.TournamentID, lease.CommandID)
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = NewExecutionAuthorityPostgres(nil).LoadAuthority(context.Background(), lease.TournamentID)
	require.ErrorIs(t, err, domain.ErrValidation)
	_, _, err = repository.CommitAuthority(context.Background(), condition, lease)
	require.ErrorIs(t, err, domain.ErrValidation)
}

func executionAuthorityLease() authoritydomain.Lease {
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	return authoritydomain.Lease{
		TournamentID: executionAuthorityID(1),
		HolderID:     executionAuthorityID(2),
		LeaseID:      executionAuthorityID(3),
		Epoch:        1,
		ProcessKind:  authoritydomain.ProcessAuthority,
		Revision:     1,
		CommandID:    executionAuthorityID(4),
		AcquiredAt:   at,
		RenewedAt:    at,
		ExpiresAt:    at.Add(time.Minute),
	}
}

func executionAuthorityRenewal() authoritydomain.Lease {
	current := executionAuthorityLease()
	renewedAt := current.RenewedAt.Add(20 * time.Second)
	return authoritydomain.Lease{
		TournamentID: current.TournamentID,
		HolderID:     current.HolderID,
		LeaseID:      current.LeaseID,
		Epoch:        current.Epoch,
		ProcessKind:  current.ProcessKind,
		Revision:     2,
		CommandID:    executionAuthorityID(8),
		Previous:     current.Stamp(),
		AcquiredAt:   current.AcquiredAt,
		RenewedAt:    renewedAt,
		ExpiresAt:    renewedAt.Add(time.Minute),
	}
}

func executionAuthorityRow(lease authoritydomain.Lease) sqlc.ExecutionAuthorityLease {
	createdAt := lease.RenewedAt.Add(time.Second)
	row := sqlc.ExecutionAuthorityLease{
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
		row.PreviousRevision = &previousRevision
		row.PreviousLeaseID = uuid.NullUUID{UUID: lease.Previous.LeaseID, Valid: true}
		row.PreviousEpoch = &previousEpoch
	}
	return row
}

func executionAuthorityID(value byte) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte{value})
}
