package authority_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	authority "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

func TestLeaseContinuityAndProof(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	initial := authority.Lease{
		TournamentID: uuid.New(),
		HolderID:     uuid.New(),
		LeaseID:      uuid.New(),
		Epoch:        1,
		ProcessKind:  authority.ProcessAuthority,
		Revision:     1,
		CommandID:    uuid.New(),
		AcquiredAt:   now,
		RenewedAt:    now,
		ExpiresAt:    now.Add(time.Minute),
	}
	require.NoError(t, initial.Validate())
	require.True(t, initial.Proves(initial.Identity(), now))
	require.False(t, initial.Proves(initial.Identity(), initial.ExpiresAt))

	renewed := initial
	renewed.Revision = 2
	renewed.CommandID = uuid.New()
	renewed.Previous = initial.Stamp()
	renewed.RenewedAt = now.Add(10 * time.Second)
	renewed.ExpiresAt = now.Add(70 * time.Second)
	require.NoError(t, renewed.Validate())

	takenOver := renewed
	takenOver.HolderID = uuid.New()
	takenOver.LeaseID = uuid.New()
	takenOver.Epoch = 2
	takenOver.Revision = 3
	takenOver.CommandID = uuid.New()
	takenOver.Previous = renewed.Stamp()
	takenOver.AcquiredAt = renewed.ExpiresAt
	takenOver.RenewedAt = renewed.ExpiresAt
	takenOver.ExpiresAt = renewed.ExpiresAt.Add(time.Minute)
	require.NoError(t, takenOver.Validate())
}

func TestLeaseCloneOwnsPredecessor(t *testing.T) {
	t.Parallel()

	lease := authority.Lease{Previous: &authority.Stamp{LeaseID: uuid.New(), Epoch: 1}}
	clone := lease.Clone()
	require.Equal(t, lease.Previous, clone.Previous)
	require.NotSame(t, lease.Previous, clone.Previous)

	clonePointer := authority.CloneLease(&lease)
	require.NotSame(t, &lease, clonePointer)
	require.NotSame(t, lease.Previous, clonePointer.Previous)
	require.Nil(t, authority.CloneLease(nil))
}
