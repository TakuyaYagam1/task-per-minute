package arena_test

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestExecutionAuthorityEpoch(t *testing.T) {
	t.Parallel()

	t.Run("renews one lease and increments the epoch only after expiry", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
		clock := &task042Clock{now: now}
		repository := &executionAuthorityRepositoryFake{}
		usecase := arena.NewExecutionAuthorityUseCase(repository, clock, 30*time.Second)
		initialCommand := arena.ExecutionAuthorityClaimCommand{
			TournamentID: task042ID(1), HolderID: task042ID(2), LeaseID: task042ID(3),
			CommandID: task042ID(4), ProcessKind: arena.ExecutionProcessKindAuthority,
		}

		initial, changed, err := usecase.Claim(t.Context(), initialCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, initial.Validate())
		require.Equal(t, int64(1), initial.Epoch)
		require.Equal(t, int64(1), initial.Revision)
		require.Nil(t, initial.Previous)
		require.True(t, initial.Proves(initial.Identity(), now))

		clock.now = now.Add(10 * time.Second)
		renewCommand := arena.ExecutionAuthorityClaimCommand{
			TournamentID: initial.TournamentID, HolderID: initial.HolderID, LeaseID: initial.LeaseID,
			CommandID: task042ID(5), ProcessKind: arena.ExecutionProcessKindAuthority,
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

		takeoverCommand := arena.ExecutionAuthorityClaimCommand{
			TournamentID: renewed.TournamentID, HolderID: task042ID(6), LeaseID: task042ID(7),
			CommandID: task042ID(8), ProcessKind: arena.ExecutionProcessKindAuthority,
			Expected: renewed.Stamp(),
		}
		current, changed, err := usecase.Claim(t.Context(), takeoverCommand)
		require.Nil(t, current)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrExecutionAuthorityActive)

		clock.now = renewed.ExpiresAt
		takenOver, changed, err := usecase.Claim(t.Context(), takeoverCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, takenOver.Validate())
		require.Equal(t, int64(2), takenOver.Epoch)
		require.Equal(t, int64(3), takenOver.Revision)
		require.Equal(t, renewed.Stamp(), takenOver.Previous)
		require.False(t, takenOver.Proves(renewed.Identity(), clock.now))
		require.True(t, takenOver.Proves(takenOver.Identity(), clock.now))
		require.Equal(t, 3, repository.writeCount())
	})

	t.Run("rejects projection and read-only transport restarts before repository access", func(t *testing.T) {
		t.Parallel()

		for index, kind := range []arena.ExecutionProcessKind{
			arena.ExecutionProcessKindProjection,
			arena.ExecutionProcessKindReadOnlyTransport,
		} {
			repository := &executionAuthorityRepositoryFake{}
			command := arena.ExecutionAuthorityClaimCommand{
				TournamentID: task042ID(20 + index*4), HolderID: task042ID(21 + index*4),
				LeaseID: task042ID(22 + index*4), CommandID: task042ID(23 + index*4),
				ProcessKind: kind,
			}
			lease, changed, err := arena.NewExecutionAuthorityUseCase(
				repository,
				&task042Clock{now: time.Date(2026, 8, 31, 9, 5, 0, 0, time.UTC)},
				30*time.Second,
			).Claim(t.Context(), command)
			require.Nil(t, lease)
			require.False(t, changed)
			require.ErrorIs(t, err, arena.ErrExecutionAuthorityForbidden)
			require.Equal(t, 0, repository.findCount())
			require.Equal(t, 0, repository.loadCount())
			require.Equal(t, 0, repository.writeCount())
		}
	})

	t.Run("reconciles concurrent duplicate claims with one write", func(t *testing.T) {
		t.Parallel()

		repository := &executionAuthorityRepositoryFake{loadBarrier: task042Barrier(2)}
		command := arena.ExecutionAuthorityClaimCommand{
			TournamentID: task042ID(40), HolderID: task042ID(41), LeaseID: task042ID(42),
			CommandID: task042ID(43), ProcessKind: arena.ExecutionProcessKindAuthority,
		}
		results := make(chan task042ClaimResult, 2)
		var group sync.WaitGroup
		for range 2 {
			group.Add(1)
			go func() {
				defer group.Done()
				lease, changed, err := arena.NewExecutionAuthorityUseCase(
					repository,
					&task042Clock{now: time.Date(2026, 8, 31, 9, 10, 0, 0, time.UTC)},
					30*time.Second,
				).Claim(context.Background(), command)
				results <- task042ClaimResult{lease: lease, changed: changed, err: err}
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
		futureRepository := &executionAuthorityRepositoryFake{}
		futureRepository.replaceCurrent(future)
		lease, changed, err := arena.NewExecutionAuthorityUseCase(
			futureRepository,
			&task042Clock{now: now},
			30*time.Second,
		).Claim(t.Context(), arena.ExecutionAuthorityClaimCommand{
			TournamentID: future.TournamentID, HolderID: future.HolderID, LeaseID: future.LeaseID,
			CommandID: task042ID(45), ProcessKind: arena.ExecutionProcessKindAuthority,
			Expected: future.Stamp(),
		})
		require.Nil(t, lease)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrExecutionAuthorityConflict)
		require.Equal(t, 0, futureRepository.writeCount())

		expired := task042Lease(now, task042ID(46), 2)
		expired.ExpiresAt = now
		expiredRepository := &executionAuthorityRepositoryFake{}
		expiredRepository.replaceCurrent(expired)
		lease, changed, err = arena.NewExecutionAuthorityUseCase(
			expiredRepository,
			&task042Clock{now: now},
			30*time.Second,
		).Claim(t.Context(), arena.ExecutionAuthorityClaimCommand{
			TournamentID: expired.TournamentID, HolderID: task042ID(47),
			LeaseID: expired.LeaseID, CommandID: task042ID(48),
			ProcessKind: arena.ExecutionProcessKindAuthority, Expected: expired.Stamp(),
		})
		require.Nil(t, lease)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrExecutionAuthorityConflict)
		require.Equal(t, 0, expiredRepository.writeCount())

		fresh, changed, err := arena.NewExecutionAuthorityUseCase(
			expiredRepository,
			&task042Clock{now: now},
			30*time.Second,
		).Claim(t.Context(), arena.ExecutionAuthorityClaimCommand{
			TournamentID: expired.TournamentID, HolderID: expired.HolderID,
			LeaseID: task042ID(49), CommandID: task042ID(50),
			ProcessKind: arena.ExecutionProcessKindAuthority, Expected: expired.Stamp(),
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
		for index, current := range []arena.ExecutionAuthorityLease{
			task042Lease(now, task042ID(54), 2),
			task042Lease(now, task042ID(55), math.MaxInt64),
		} {
			current.Revision = math.MaxInt64
			current.ExpiresAt = now
			if current.Epoch == math.MaxInt64 {
				current.Previous = &arena.ExecutionAuthorityStamp{
					LeaseID: task042ID(63), Epoch: math.MaxInt64 - 1,
				}
			}
			require.NoError(t, current.Validate())
			repository := &executionAuthorityRepositoryFake{}
			repository.replaceCurrent(current)
			lease, changed, err := arena.NewExecutionAuthorityUseCase(
				repository,
				&task042Clock{now: now},
				30*time.Second,
			).Claim(t.Context(), arena.ExecutionAuthorityClaimCommand{
				TournamentID: current.TournamentID, HolderID: task042ID(56 + index*3),
				LeaseID: task042ID(57 + index*3), CommandID: task042ID(58 + index*3),
				ProcessKind: arena.ExecutionProcessKindAuthority, Expected: current.Stamp(),
			})
			require.Nil(t, lease)
			require.False(t, changed)
			require.ErrorIs(t, err, arena.ErrExecutionAuthorityConflict)
			require.Equal(t, 0, repository.writeCount())
		}
	})

	t.Run("reconciles durable command history after renewal and takeover", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 9, 16, 0, 0, time.UTC)
		clock := &task042Clock{now: now}
		repository := &executionAuthorityRepositoryFake{}
		usecase := arena.NewExecutionAuthorityUseCase(repository, clock, 30*time.Second)
		initialCommand := arena.ExecutionAuthorityClaimCommand{
			TournamentID: task042ID(80), HolderID: task042ID(81), LeaseID: task042ID(82),
			CommandID: task042ID(83), ProcessKind: arena.ExecutionProcessKindAuthority,
		}
		initial, changed, err := usecase.Claim(t.Context(), initialCommand)
		require.NoError(t, err)
		require.True(t, changed)

		clock.now = now.Add(5 * time.Second)
		renewCommand := arena.ExecutionAuthorityClaimCommand{
			TournamentID: initial.TournamentID, HolderID: initial.HolderID, LeaseID: initial.LeaseID,
			CommandID: task042ID(84), ProcessKind: arena.ExecutionProcessKindAuthority,
			Expected: initial.Stamp(),
		}
		renewed, changed, err := usecase.Claim(t.Context(), renewCommand)
		require.NoError(t, err)
		require.True(t, changed)
		wantRenewalPrevious := *renewed.Previous
		renewed.Previous.LeaseID = task042ID(85)

		clock.now = renewed.ExpiresAt
		takeoverCommand := arena.ExecutionAuthorityClaimCommand{
			TournamentID: renewed.TournamentID, HolderID: task042ID(86), LeaseID: task042ID(87),
			CommandID: task042ID(88), ProcessKind: arena.ExecutionProcessKindAuthority,
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
		require.ErrorIs(t, err, arena.ErrExecutionAuthorityCommandReuse)
	})

	t.Run("fences lease state at authoritative commit time", func(t *testing.T) {
		t.Parallel()

		base := time.Date(2026, 8, 31, 9, 18, 0, 0, time.UTC)
		live := task042Lease(base, task042ID(110), 2)
		renewRepository := &executionAuthorityRepositoryFake{
			transactionNow:      base,
			advanceBeforeCommit: live.ExpiresAt.Sub(base),
		}
		renewRepository.replaceCurrent(live)
		lease, changed, err := arena.NewExecutionAuthorityUseCase(
			renewRepository,
			&task042Clock{now: base},
			30*time.Second,
		).Claim(t.Context(), arena.ExecutionAuthorityClaimCommand{
			TournamentID: live.TournamentID, HolderID: live.HolderID, LeaseID: live.LeaseID,
			CommandID: task042ID(111), ProcessKind: arena.ExecutionProcessKindAuthority,
			Expected: live.Stamp(),
		})
		require.Nil(t, lease)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrExecutionAuthorityConflict)
		require.Equal(t, 2, renewRepository.commitCount())
		require.Equal(t, 0, renewRepository.writeCount())

		takeoverNow := live.ExpiresAt
		takeoverRepository := &executionAuthorityRepositoryFake{
			transactionNow: takeoverNow.Add(-time.Second),
		}
		takeoverRepository.replaceCurrent(live)
		lease, changed, err = arena.NewExecutionAuthorityUseCase(
			takeoverRepository,
			&task042Clock{now: takeoverNow},
			30*time.Second,
		).Claim(t.Context(), arena.ExecutionAuthorityClaimCommand{
			TournamentID: live.TournamentID, HolderID: task042ID(112), LeaseID: task042ID(113),
			CommandID: task042ID(114), ProcessKind: arena.ExecutionProcessKindAuthority,
			Expected: live.Stamp(),
		})
		require.Nil(t, lease)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrExecutionAuthorityConflict)
		require.Equal(t, 2, takeoverRepository.commitCount())
		require.Equal(t, 0, takeoverRepository.writeCount())
	})
}

func TestExecutionAuthorityEpochReplay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 31, 9, 20, 0, 0, time.UTC)
	attemptAuthority, attemptCommand := task040FailedAttemptFixture(t, now)
	attemptCommand.FailureClass = arena.NormalAttemptFailureExecutionEpochBreak
	current := task042Lease(now, attemptCommand.Scope.TournamentID, 2)
	broken := &arena.ExecutionAuthorityStamp{LeaseID: task042ID(70), Epoch: 1}
	command := arena.ExecutionEpochReplayCommand{
		CurrentAuthority: current.Identity(), BrokenAuthority: *broken, Attempt: attemptCommand,
	}
	repository := &executionEpochReplayRepositoryFake{authority: arena.ExecutionEpochReplayAuthority{
		Lease: current, BoundAuthority: *broken, Attempt: attemptAuthority,
	}}
	usecase := arena.NewExecutionEpochReplayUseCase(repository, &task042Clock{now: now})

	record, changed, err := usecase.Replay(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, domain.ArenaGameResultReasonExecutionEpochBreak, record.Attempt.Game.ResultReason)
	require.Equal(t, domain.ArenaSeriesStateReplayRequired, record.Attempt.Series.Series.State)
	require.Equal(t, attemptCommand.Revisions.RouteEvidenceID, record.Attempt.WaveRoute.ID)
	require.Equal(t, broken, &record.BrokenAuthority)
	require.Equal(t, 1, repository.writeCount())
	wantRecord := *record
	record.Attempt.WaveRoute.ID = task042ID(72)

	repeated, changed, err := usecase.Replay(t.Context(), command)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, &wantRecord, repeated)
	require.Equal(t, attemptCommand.Revisions.RouteEvidenceID, repeated.Attempt.WaveRoute.ID)
	require.Equal(t, 1, repository.writeCount())

	later := current
	later.LeaseID = task042ID(73)
	later.Epoch = current.Epoch + 1
	later.Revision = current.Revision + 1
	later.CommandID = task042ID(74)
	later.Previous = current.Stamp()
	later.AcquiredAt = now
	later.RenewedAt = now
	later.ExpiresAt = now.Add(time.Minute)
	require.NoError(t, later.Validate())
	repository.update(func(authority *arena.ExecutionEpochReplayAuthority) {
		authority.Lease = later
	})
	repeated, changed, err = usecase.Replay(t.Context(), command)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, &wantRecord, repeated)
	require.Equal(t, 1, repository.writeCount())

	stale := command
	stale.CurrentAuthority.HolderID = task042ID(71)
	replayed, changed, err := usecase.Replay(t.Context(), stale)
	require.Nil(t, replayed)
	require.False(t, changed)
	require.ErrorIs(t, err, arena.ErrExecutionAuthorityConflict)
	require.Equal(t, 1, repository.writeCount())
}

func TestExecutionAuthorityEpochReplayBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("rejects wrapper and failed-attempt current mismatches", func(t *testing.T) {
		t.Parallel()

		for _, corrupt := range []func(*arena.ExecutionEpochReplayAuthority){
			func(authority *arena.ExecutionEpochReplayAuthority) {
				authority.Attempt.Current = nil
			},
			func(authority *arena.ExecutionEpochReplayAuthority) {
				mismatch := authority.Current.Attempt
				mismatch.CommandID = task042ID(100)
				authority.Attempt.Current = &mismatch
			},
		} {
			now := time.Date(2026, 8, 31, 9, 22, 0, 0, time.UTC)
			authority, command := task042ReplayAuthority(t, now)
			repository := &executionEpochReplayRepositoryFake{authority: authority}
			usecase := arena.NewExecutionEpochReplayUseCase(repository, &task042Clock{now: now})
			_, changed, err := usecase.Replay(t.Context(), command)
			require.NoError(t, err)
			require.True(t, changed)
			repository.update(corrupt)

			replayed, changed, err := usecase.Replay(t.Context(), command)
			require.Nil(t, replayed)
			require.False(t, changed)
			require.ErrorIs(t, err, arena.ErrInvalidExecutionEpochReplay)
			require.Equal(t, 1, repository.writeCount())
		}
	})

	t.Run("rejects attempt ordinal and projection overflow", func(t *testing.T) {
		t.Parallel()

		for _, overflow := range []func(*arena.ExecutionEpochReplayAuthority){
			func(authority *arena.ExecutionEpochReplayAuthority) {
				authority.Attempt.CurrentProjectionRevision = math.MaxInt64
			},
			func(authority *arena.ExecutionEpochReplayAuthority) {
				authority.Attempt.CurrentOrdinal = task042MaxInt() - 1
				revisionID := domain.ArenaSeriesScoreRevisionID(task042ID(101))
				authority.Attempt.Series.Series.CurrentScoreRevisionID = &revisionID
			},
		} {
			now := time.Date(2026, 8, 31, 9, 24, 0, 0, time.UTC)
			authority, command := task042ReplayAuthority(t, now)
			overflow(&authority)
			repository := &executionEpochReplayRepositoryFake{authority: authority}
			replayed, changed, err := arena.NewExecutionEpochReplayUseCase(
				repository,
				&task042Clock{now: now},
			).Replay(t.Context(), command)
			require.Nil(t, replayed)
			require.False(t, changed)
			require.ErrorIs(t, err, arena.ErrInvalidExecutionEpochReplay)
			require.Equal(t, 0, repository.writeCount())
		}
	})

	t.Run("fences live lease at replay commit time", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 9, 26, 0, 0, time.UTC)
		authority, command := task042ReplayAuthority(t, now)
		repository := &executionEpochReplayRepositoryFake{
			authority:           authority,
			transactionNow:      now,
			advanceBeforeCommit: authority.Lease.ExpiresAt.Sub(now),
		}
		replayed, changed, err := arena.NewExecutionEpochReplayUseCase(
			repository,
			&task042Clock{now: now},
		).Replay(t.Context(), command)
		require.Nil(t, replayed)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrExecutionAuthorityConflict)
		require.Equal(t, 2, repository.commitCount())
		require.Equal(t, 0, repository.writeCount())
		wrapperCurrent, attemptCurrent := repository.currentState()
		require.False(t, wrapperCurrent)
		require.False(t, attemptCurrent)
	})
}

type task042Clock struct {
	now time.Time
}

func (c *task042Clock) Now() time.Time {
	return c.now
}

type task042ClaimResult struct {
	lease   *arena.ExecutionAuthorityLease
	changed bool
	err     error
}

type executionAuthorityRepositoryFake struct {
	mu                  sync.Mutex
	current             *arena.ExecutionAuthorityLease
	commands            map[task042AuthorityCommandKey]arena.ExecutionAuthorityLease
	finds               int
	loads               int
	writes              int
	commits             int
	loadBarrier         *sync.WaitGroup
	barrierLoads        int
	transactionNow      time.Time
	advanceBeforeCommit time.Duration
}

type task042AuthorityCommandKey struct {
	tournamentID uuid.UUID
	commandID    uuid.UUID
}

func (r *executionAuthorityRepositoryFake) FindExecutionAuthorityCommand(
	_ context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*arena.ExecutionAuthorityLease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finds++
	recorded, exists := r.commands[task042AuthorityCommandKey{
		tournamentID: tournamentID,
		commandID:    commandID,
	}]
	if !exists {
		return nil, nil
	}
	clone := task042CloneLease(recorded)
	return &clone, nil
}

func (r *executionAuthorityRepositoryFake) LoadExecutionAuthority(
	_ context.Context,
	_ uuid.UUID,
) (*arena.ExecutionAuthorityLease, error) {
	r.mu.Lock()
	r.loads++
	wait := r.loadBarrier != nil && r.barrierLoads < 2
	if wait {
		r.barrierLoads++
	}
	var current *arena.ExecutionAuthorityLease
	if r.current != nil {
		clone := task042CloneLease(*r.current)
		current = &clone
	}
	r.mu.Unlock()
	if wait {
		r.loadBarrier.Done()
		r.loadBarrier.Wait()
	}
	return current, nil
}

func (r *executionAuthorityRepositoryFake) CommitExecutionAuthority(
	_ context.Context,
	condition arena.ExecutionAuthorityCommitCondition,
	lease arena.ExecutionAuthorityLease,
) (*arena.ExecutionAuthorityLease, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits++
	transactionNow := r.transactionNow
	if transactionNow.IsZero() {
		transactionNow = lease.RenewedAt
	}
	if r.advanceBeforeCommit != 0 {
		transactionNow = transactionNow.Add(r.advanceBeforeCommit)
		r.transactionNow = transactionNow
		r.advanceBeforeCommit = 0
	}
	if !task042AuthorityConditionMatches(condition, r.current, transactionNow) ||
		!lease.Proves(lease.Identity(), transactionNow) {
		return nil, false, domain.ErrConflict
	}
	stored := task042CloneLease(lease)
	r.current = &stored
	if r.commands == nil {
		r.commands = make(map[task042AuthorityCommandKey]arena.ExecutionAuthorityLease)
	}
	r.commands[task042AuthorityCommandKey{
		tournamentID: lease.TournamentID,
		commandID:    lease.CommandID,
	}] = task042CloneLease(lease)
	r.writes++
	result := task042CloneLease(stored)
	return &result, true, nil
}

func (r *executionAuthorityRepositoryFake) loadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loads
}

func (r *executionAuthorityRepositoryFake) findCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.finds
}

func (r *executionAuthorityRepositoryFake) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func (r *executionAuthorityRepositoryFake) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *executionAuthorityRepositoryFake) replaceCurrent(lease arena.ExecutionAuthorityLease) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := task042CloneLease(lease)
	r.current = &stored
}

type executionEpochReplayRepositoryFake struct {
	mu                  sync.Mutex
	authority           arena.ExecutionEpochReplayAuthority
	writes              int
	commits             int
	transactionNow      time.Time
	advanceBeforeCommit time.Duration
}

func (r *executionEpochReplayRepositoryFake) LoadExecutionEpochReplayAuthority(
	_ context.Context,
	_ arena.FailedAttemptScope,
) (arena.ExecutionEpochReplayAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authority, nil
}

func (r *executionEpochReplayRepositoryFake) CommitExecutionEpochReplay(
	_ context.Context,
	condition arena.ExecutionEpochReplayCommitCondition,
	record arena.ExecutionEpochReplayRecord,
) (*arena.ExecutionEpochReplayRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits++
	transactionNow := r.transactionNow
	if transactionNow.IsZero() {
		transactionNow = record.Attempt.TerminalizedAt
	}
	if r.advanceBeforeCommit != 0 {
		transactionNow = transactionNow.Add(r.advanceBeforeCommit)
		r.transactionNow = transactionNow
		r.advanceBeforeCommit = 0
	}
	if condition.Validate() != nil || r.authority.Current != nil ||
		condition.CurrentAuthority != record.CurrentAuthority ||
		condition.ExpectedLeaseRevision != record.ExpectedLeaseRevision ||
		condition.BrokenAuthority != record.BrokenAuthority ||
		condition.ExpectedAttemptRevision != record.Attempt.ExpectedAuthorityRevision ||
		condition.ExpectedLeaseRevision != r.authority.Lease.Revision ||
		!r.authority.Lease.Proves(condition.CurrentAuthority, transactionNow) ||
		condition.BrokenAuthority != r.authority.BoundAuthority ||
		condition.ExpectedAttemptRevision != r.authority.Attempt.Revision ||
		r.authority.Attempt.Current != nil {
		return nil, false, domain.ErrConflict
	}
	stored := record
	r.authority.Attempt.Current = &stored.Attempt
	r.authority.Current = &stored
	r.writes++
	return &stored, true, nil
}

func (r *executionEpochReplayRepositoryFake) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func (r *executionEpochReplayRepositoryFake) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *executionEpochReplayRepositoryFake) currentState() (bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authority.Current != nil, r.authority.Attempt.Current != nil
}

func (r *executionEpochReplayRepositoryFake) update(
	update func(*arena.ExecutionEpochReplayAuthority),
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	update(&r.authority)
}

func task042Lease(now time.Time, tournamentID uuid.UUID, epoch int64) arena.ExecutionAuthorityLease {
	return arena.ExecutionAuthorityLease{
		TournamentID: tournamentID, HolderID: task042ID(60), LeaseID: task042ID(61),
		Epoch: epoch, ProcessKind: arena.ExecutionProcessKindAuthority,
		Revision: 4, CommandID: task042ID(62),
		Previous:   &arena.ExecutionAuthorityStamp{LeaseID: task042ID(63), Epoch: epoch - 1},
		AcquiredAt: now.Add(-time.Second), RenewedAt: now.Add(-time.Second),
		ExpiresAt: now.Add(time.Minute),
	}
}

func task042ReplayAuthority(
	t *testing.T,
	now time.Time,
) (arena.ExecutionEpochReplayAuthority, arena.ExecutionEpochReplayCommand) {
	t.Helper()
	attemptAuthority, attemptCommand := task040FailedAttemptFixture(t, now)
	attemptCommand.FailureClass = arena.NormalAttemptFailureExecutionEpochBreak
	lease := task042Lease(now, attemptCommand.Scope.TournamentID, 2)
	broken := arena.ExecutionAuthorityStamp{LeaseID: task042ID(102), Epoch: 1}
	return arena.ExecutionEpochReplayAuthority{
			Lease: lease, BoundAuthority: broken, Attempt: attemptAuthority,
		}, arena.ExecutionEpochReplayCommand{
			CurrentAuthority: lease.Identity(), BrokenAuthority: broken, Attempt: attemptCommand,
		}
}

func task042CloneLease(lease arena.ExecutionAuthorityLease) arena.ExecutionAuthorityLease {
	clone := lease
	if lease.Previous != nil {
		previous := *lease.Previous
		clone.Previous = &previous
	}
	return clone
}

func task042MaxInt() int {
	return int(^uint(0) >> 1)
}

func task042AuthorityConditionMatches(
	condition arena.ExecutionAuthorityCommitCondition,
	current *arena.ExecutionAuthorityLease,
	transactionNow time.Time,
) bool {
	if condition.Validate() != nil {
		return false
	}
	if current == nil {
		return condition.ExpectedState == arena.ExecutionAuthorityExpectedAbsent
	}
	if current.Validate() != nil || condition.ExpectedRevision != current.Revision ||
		condition.ExpectedStamp != *current.Stamp() {
		return false
	}
	switch condition.ExpectedState {
	case arena.ExecutionAuthorityExpectedAbsent:
		return false
	case arena.ExecutionAuthorityExpectedLive:
		return current.Proves(current.Identity(), transactionNow)
	case arena.ExecutionAuthorityExpectedExpired:
		return !transactionNow.IsZero() && transactionNow.Location() == time.UTC &&
			!transactionNow.Before(current.ExpiresAt)
	default:
		return false
	}
}

func task042Barrier(size int) *sync.WaitGroup {
	barrier := &sync.WaitGroup{}
	barrier.Add(size)
	return barrier
}

func task042ID(value int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("task-042-"+time.Unix(int64(value), 0).UTC().Format(time.RFC3339)))
}

var _ arena.ExecutionAuthorityRepository = (*executionAuthorityRepositoryFake)(nil)
var _ arena.ExecutionEpochReplayRepository = (*executionEpochReplayRepositoryFake)(nil)
