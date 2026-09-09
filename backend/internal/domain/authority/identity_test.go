package authority_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	authority "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

func TestIdentityValidation(t *testing.T) {
	t.Parallel()

	identity := authority.Identity{
		TournamentID: uuid.New(),
		HolderID:     uuid.New(),
		LeaseID:      uuid.New(),
		Epoch:        2,
		ProcessKind:  authority.ProcessAuthority,
	}
	require.NoError(t, identity.Validate())
	require.Equal(t, authority.Stamp{LeaseID: identity.LeaseID, Epoch: 2}, identity.Stamp())

	for _, kind := range []authority.ProcessKind{
		authority.ProcessProjection,
		authority.ProcessReadOnlyTransport,
	} {
		invalid := identity
		invalid.ProcessKind = kind
		require.ErrorIs(t, invalid.Validate(), authority.ErrInvalid)
	}
}

func TestStampCopyAndEquality(t *testing.T) {
	t.Parallel()

	stamp := &authority.Stamp{LeaseID: uuid.New(), Epoch: 3}
	clone := authority.CloneStamp(stamp)
	require.Equal(t, stamp, clone)
	require.NotSame(t, stamp, clone)
	require.True(t, authority.StampsEqual(stamp, clone))
	require.True(t, authority.StampsEqual(nil, nil))
	require.False(t, authority.StampsEqual(stamp, nil))
}
