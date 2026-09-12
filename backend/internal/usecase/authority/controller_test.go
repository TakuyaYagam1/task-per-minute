package authority_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
	authoritymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority/mocks"
)

func TestControllerProvesNewLeaseAtFreshAuthoritativeTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	repository := newAuthorityRepositoryHarness(t, authorityRepositoryOptions{})
	source := authoritymocks.NewMockTimeSource(t)
	call := 0
	source.EXPECT().AuthorityTime(mock.Anything).RunAndReturn(func(context.Context) (time.Time, error) {
		call++
		return now.Add(time.Duration(call) * time.Microsecond), nil
	}).Times(3)
	controller, err := authorityusecase.NewController(
		repository,
		source,
		authorityusecase.ControllerConfig{HolderID: task042ID(139)},
	)
	require.NoError(t, err)

	identity, err := controller.AuthorityFor(t.Context(), task042ID(138))
	require.NoError(t, err)
	require.Equal(t, task042ID(138), identity.TournamentID)
	require.Equal(t, 1, repository.writeCount())
}

func TestControllerRenewsAndFencesExpiredLocalAuthority(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC)
	repository := newAuthorityRepositoryHarness(t, authorityRepositoryOptions{})
	timeSource := newMutableAuthorityTimeSource(t, now)
	controller, err := authorityusecase.NewController(
		repository,
		timeSource.mock,
		authorityusecase.ControllerConfig{
			HolderID:      task042ID(140),
			LeaseDuration: 30 * time.Second,
			RenewBefore:   10 * time.Second,
		},
	)
	require.NoError(t, err)

	first, err := controller.AuthorityFor(t.Context(), task042ID(141))
	require.NoError(t, err)
	require.EqualValues(t, 1, first.Epoch)
	require.Equal(t, 1, repository.writeCount())

	timeSource.set(now.Add(time.Second))
	cached, err := controller.AuthorityFor(t.Context(), task042ID(141))
	require.NoError(t, err)
	require.Equal(t, first, cached)
	require.Equal(t, 1, repository.writeCount())

	// After expiry, even the same process must create a fresh lease and epoch.
	// Reusing the local LeaseID here would be an unfenced self-takeover.
	timeSource.set(now.Add(31 * time.Second))
	takenOver, err := controller.AuthorityFor(t.Context(), task042ID(141))
	require.NoError(t, err)
	require.NotEqual(t, first.LeaseID, takenOver.LeaseID)
	require.Equal(t, first.Epoch+1, takenOver.Epoch)
	require.Equal(t, 2, repository.writeCount())
}

func TestControllerNeverTakesLiveForeignAuthority(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 7, 8, 2, 0, 0, time.UTC)
	tournamentID := task042ID(145)
	repository := newAuthorityRepositoryHarness(t, authorityRepositoryOptions{})
	foreign := task042Lease(now, tournamentID, 2)
	repository.replaceCurrent(foreign)
	controller, err := authorityusecase.NewController(
		repository,
		newMutableAuthorityTimeSource(t, now).mock,
		authorityusecase.ControllerConfig{HolderID: task042ID(146)},
	)
	require.NoError(t, err)

	identity, err := controller.AuthorityFor(t.Context(), tournamentID)
	require.EqualError(t, err, authorityusecase.ErrNotOwner.Error())
	require.Empty(t, identity)
	require.Equal(t, 0, repository.writeCount())
}
