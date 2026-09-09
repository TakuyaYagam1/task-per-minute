package authority_test

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	authorityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/authority"
)

func TestClaimAuthorityLease(t *testing.T) {
	t.Parallel()

	t.Run("renews one lease and increments the epoch only after expiry", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
		clock := newMutableAuthorityClock(t, now)
		repository := newAuthorityRepositoryHarness(t, authorityRepositoryOptions{})
		usecase := authorityusecase.New(repository, clock.mock, 30*time.Second)
		initialCommand := authorityusecase.ClaimCommand{
			TournamentID: task042ID(1), HolderID: task042ID(2), LeaseID: task042ID(3),
			CommandID: task042ID(4), ProcessKind: authoritydomain.ProcessAuthority,
		}

		initial, changed, err := usecase.Claim(t.Context(), initialCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, initial.Validate())
		require.Equal(t, int64(1), initial.Epoch)
		require.Equal(t, int64(1), initial.Revision)
		require.Nil(t, initial.Previous)
		require.True(t, initial.Proves(initial.Identity(), now))

		clock.set(now.Add(10 * time.Second))
		renewCommand := authorityusecase.ClaimCommand{
			TournamentID: initial.TournamentID, HolderID: initial.HolderID, LeaseID: initial.LeaseID,
			CommandID: task042ID(5), ProcessKind: authoritydomain.ProcessAuthority,
			Expected: initial.Stamp(),
		}
		renewed, changed, err := usecase.Claim(t.Context(), renewCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, renewed.Validate())
		require.Equal(t, initial.LeaseID, renewed.LeaseID)
		require.Equal(t, initial.Epoch, renewed.Epoch)
		require.Equal(t, int64(2), renewed.Revision)
		require.Equal(t, initial.AcquiredAt, renewed.AcquiredAt)
		require.Equal(t, initial.Stamp(), renewed.Previous)

		takeoverCommand := authorityusecase.ClaimCommand{
			TournamentID: renewed.TournamentID, HolderID: task042ID(6), LeaseID: task042ID(7),
			CommandID: task042ID(8), ProcessKind: authoritydomain.ProcessAuthority,
			Expected: renewed.Stamp(),
		}
		current, changed, err := usecase.Claim(t.Context(), takeoverCommand)
		require.Nil(t, current)
		require.False(t, changed)
		require.ErrorIs(t, err, authoritydomain.ErrActive)

		clock.set(renewed.ExpiresAt)
		takenOver, changed, err := usecase.Claim(t.Context(), takeoverCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, takenOver.Validate())
		require.Equal(t, int64(2), takenOver.Epoch)
		require.Equal(t, int64(3), takenOver.Revision)
		require.Equal(t, renewed.Stamp(), takenOver.Previous)
		require.False(t, takenOver.Proves(renewed.Identity(), clock.current()))
		require.True(t, takenOver.Proves(takenOver.Identity(), clock.current()))
		require.Equal(t, 3, repository.writeCount())
	})

	t.Run("rejects projection and read-only transport restarts before repository access", func(t *testing.T) {
		t.Parallel()

		for index, kind := range []authoritydomain.ProcessKind{
			authoritydomain.ProcessProjection,
			authoritydomain.ProcessReadOnlyTransport,
		} {
			repository := newAuthorityRepositoryHarness(t, authorityRepositoryOptions{})
			command := authorityusecase.ClaimCommand{
				TournamentID: task042ID(20 + index*4), HolderID: task042ID(21 + index*4),
				LeaseID: task042ID(22 + index*4), CommandID: task042ID(23 + index*4),
				ProcessKind: kind,
			}
			lease, changed, err := authorityusecase.New(
				repository,
				newMutableAuthorityClock(t, time.Date(2026, 8, 31, 9, 5, 0, 0, time.UTC)).mock,
				30*time.Second,
			).Claim(t.Context(), command)
			require.Nil(t, lease)
			require.False(t, changed)
			require.ErrorIs(t, err, authorityusecase.ErrForbidden)
			require.Equal(t, 0, repository.findCount())
			require.Equal(t, 0, repository.loadCount())
			require.Equal(t, 0, repository.writeCount())
		}
	})

	t.Run("reconciles concurrent duplicate claims with one write", func(t *testing.T) {
		t.Parallel()

		repository := newAuthorityRepositoryHarness(t, authorityRepositoryOptions{loadBarrier: task042Barrier(2)})
		clock := newMutableAuthorityClock(t, time.Date(2026, 8, 31, 9, 10, 0, 0, time.UTC))
		command := authorityusecase.ClaimCommand{
			TournamentID: task042ID(40), HolderID: task042ID(41), LeaseID: task042ID(42),
			CommandID: task042ID(43), ProcessKind: authoritydomain.ProcessAuthority,
		}
		results := make(chan authorityClaimResult, 2)
		var group sync.WaitGroup
		for range 2 {
			group.Add(1)
			go func() {
				defer group.Done()
				lease, changed, err := authorityusecase.New(
					repository,
					clock.mock,
					30*time.Second,
				).Claim(context.Background(), command)
				results <- authorityClaimResult{lease: lease, changed: changed, err: err}
			}()
		}
		group.Wait()
		close(results)

		changedCount := 0
		for result := range results {
			require.NoError(t, result.err)
			require.NotNil(t, result.lease)
			if result.changed {
				changedCount++
			}
		}
		require.Equal(t, 1, changedCount)
		require.Equal(t, 1, repository.writeCount())
	})

	t.Run("rejects future renewal time and expired lease identity reuse", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 9, 12, 0, 0, time.UTC)
		future := task042Lease(now, task042ID(44), 2)
		future.RenewedAt = now.Add(time.Second)
		require.NoError(t, future.Validate())
		require.False(t, future.Proves(future.Identity(), now))
		futureRepository := newAuthorityRepositoryHarness(t, authorityRepositoryOptions{})
		futureRepository.replaceCurrent(future)
		lease, changed, err := authorityusecase.New(
			futureRepository,
			newMutableAuthorityClock(t, now).mock,
			30*time.Second,
		).Claim(t.Context(), authorityusecase.ClaimCommand{
			TournamentID: future.TournamentID, HolderID: future.HolderID, LeaseID: future.LeaseID,
			CommandID: task042ID(45), ProcessKind: authoritydomain.ProcessAuthority,
			Expected: future.Stamp(),
		})
		require.Nil(t, lease)
		require.False(t, changed)
		require.ErrorIs(t, err, authoritydomain.ErrConflict)
		require.Equal(t, 0, futureRepository.writeCount())

		expired := task042Lease(now, task042ID(46), 2)
		expired.ExpiresAt = now
		expiredRepository := newAuthorityRepositoryHarness(t, authorityRepositoryOptions{})
		expiredRepository.replaceCurrent(expired)
		lease, changed, err = authorityusecase.New(
			expiredRepository,
			newMutableAuthorityClock(t, now).mock,
			30*time.Second,
		).Claim(t.Context(), authorityusecase.ClaimCommand{
			TournamentID: expired.TournamentID, HolderID: task042ID(47),
			LeaseID: expired.LeaseID, CommandID: task042ID(48),
			ProcessKind: authoritydomain.ProcessAuthority, Expected: expired.Stamp(),
		})
		require.Nil(t, lease)
		require.False(t, changed)
		require.ErrorIs(t, err, authoritydomain.ErrConflict)
		require.Equal(t, 0, expiredRepository.writeCount())

		fresh, changed, err := authorityusecase.New(
			expiredRepository,
			newMutableAuthorityClock(t, now).mock,
			30*time.Second,
		).Claim(t.Context(), authorityusecase.ClaimCommand{
			TournamentID: expired.TournamentID, HolderID: expired.HolderID,
			LeaseID: task042ID(49), CommandID: task042ID(50),
			ProcessKind: authoritydomain.ProcessAuthority, Expected: expired.Stamp(),
		})
		require.NoError(t, err)
		require.True(t, changed)
		require.NotEqual(t, expired.LeaseID, fresh.LeaseID)
		require.Equal(t, expired.Epoch+1, fresh.Epoch)
		require.Equal(t, 1, expiredRepository.writeCount())
	})

	t.Run("rejects lease epoch and revision overflow", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 9, 14, 0, 0, time.UTC)
		for index, current := range []authoritydomain.Lease{
			task042Lease(now, task042ID(54), 2),
			task042Lease(now, task042ID(55), math.MaxInt64),
		} {
			current.Revision = math.MaxInt64
			current.ExpiresAt = now
			if current.Epoch == math.MaxInt64 {
				current.Previous = &authoritydomain.Stamp{
					LeaseID: task042ID(63), Epoch: math.MaxInt64 - 1,
				}
			}
			require.NoError(t, current.Validate())
			repository := newAuthorityRepositoryHarness(t, authorityRepositoryOptions{})
			repository.replaceCurrent(current)
			lease, changed, err := authorityusecase.New(
				repository,
				newMutableAuthorityClock(t, now).mock,
				30*time.Second,
			).Claim(t.Context(), authorityusecase.ClaimCommand{
				TournamentID: current.TournamentID, HolderID: task042ID(56 + index*3),
				LeaseID: task042ID(57 + index*3), CommandID: task042ID(58 + index*3),
				ProcessKind: authoritydomain.ProcessAuthority, Expected: current.Stamp(),
			})
			require.Nil(t, lease)
			require.False(t, changed)
			require.ErrorIs(t, err, authoritydomain.ErrConflict)
			require.Equal(t, 0, repository.writeCount())
		}
	})

	t.Run("reconciles durable command history after renewal and takeover", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 9, 16, 0, 0, time.UTC)
		clock := newMutableAuthorityClock(t, now)
		repository := newAuthorityRepositoryHarness(t, authorityRepositoryOptions{})
		usecase := authorityusecase.New(repository, clock.mock, 30*time.Second)
		initialCommand := authorityusecase.ClaimCommand{
			TournamentID: task042ID(80), HolderID: task042ID(81), LeaseID: task042ID(82),
			CommandID: task042ID(83), ProcessKind: authoritydomain.ProcessAuthority,
		}
		initial, changed, err := usecase.Claim(t.Context(), initialCommand)
		require.NoError(t, err)
		require.True(t, changed)

		clock.set(now.Add(5 * time.Second))
		renewCommand := authorityusecase.ClaimCommand{
			TournamentID: initial.TournamentID, HolderID: initial.HolderID, LeaseID: initial.LeaseID,
			CommandID: task042ID(84), ProcessKind: authoritydomain.ProcessAuthority,
			Expected: initial.Stamp(),
		}
		renewed, changed, err := usecase.Claim(t.Context(), renewCommand)
		require.NoError(t, err)
		require.True(t, changed)
		wantRenewalPrevious := *renewed.Previous
		renewed.Previous.LeaseID = task042ID(85)

		clock.set(renewed.ExpiresAt)
		takeoverCommand := authorityusecase.ClaimCommand{
			TournamentID: renewed.TournamentID, HolderID: task042ID(86), LeaseID: task042ID(87),
			CommandID: task042ID(88), ProcessKind: authoritydomain.ProcessAuthority,
			Expected: renewed.Stamp(),
		}
		_, changed, err = usecase.Claim(t.Context(), takeoverCommand)
		require.NoError(t, err)
		require.True(t, changed)

		retriedInitial, changed, err := usecase.Claim(t.Context(), initialCommand)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, initial, retriedInitial)
		retriedRenewal, changed, err := usecase.Claim(t.Context(), renewCommand)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, wantRenewalPrevious, *retriedRenewal.Previous)
		require.NoError(t, retriedRenewal.Validate())
		require.Equal(t, 3, repository.writeCount())

		reused := initialCommand
		reused.HolderID = task042ID(89)
		lease, changed, err := usecase.Claim(t.Context(), reused)
		require.Nil(t, lease)
		require.False(t, changed)
		require.ErrorIs(t, err, authoritydomain.ErrCommandReuse)
	})

	t.Run("fences lease state at authoritative commit time", func(t *testing.T) {
		t.Parallel()

		base := time.Date(2026, 8, 31, 9, 18, 0, 0, time.UTC)
		live := task042Lease(base, task042ID(110), 2)
		renewRepository := newAuthorityRepositoryHarness(t, authorityRepositoryOptions{
			transactionNow:      base,
			advanceBeforeCommit: live.ExpiresAt.Sub(base),
		})
		renewRepository.replaceCurrent(live)
		lease, changed, err := authorityusecase.New(
			renewRepository,
			newMutableAuthorityClock(t, base).mock,
			30*time.Second,
		).Claim(t.Context(), authorityusecase.ClaimCommand{
			TournamentID: live.TournamentID, HolderID: live.HolderID, LeaseID: live.LeaseID,
			CommandID: task042ID(111), ProcessKind: authoritydomain.ProcessAuthority,
			Expected: live.Stamp(),
		})
		require.Nil(t, lease)
		require.False(t, changed)
		require.ErrorIs(t, err, authoritydomain.ErrConflict)
		require.Equal(t, 2, renewRepository.commitCount())
		require.Equal(t, 0, renewRepository.writeCount())

		takeoverNow := live.ExpiresAt
		takeoverRepository := newAuthorityRepositoryHarness(t, authorityRepositoryOptions{
			transactionNow: takeoverNow.Add(-time.Second),
		})
		takeoverRepository.replaceCurrent(live)
		lease, changed, err = authorityusecase.New(
			takeoverRepository,
			newMutableAuthorityClock(t, takeoverNow).mock,
			30*time.Second,
		).Claim(t.Context(), authorityusecase.ClaimCommand{
			TournamentID: live.TournamentID, HolderID: task042ID(112), LeaseID: task042ID(113),
			CommandID: task042ID(114), ProcessKind: authoritydomain.ProcessAuthority,
			Expected: live.Stamp(),
		})
		require.Nil(t, lease)
		require.False(t, changed)
		require.ErrorIs(t, err, authoritydomain.ErrConflict)
		require.Equal(t, 2, takeoverRepository.commitCount())
		require.Equal(t, 0, takeoverRepository.writeCount())
	})
}
