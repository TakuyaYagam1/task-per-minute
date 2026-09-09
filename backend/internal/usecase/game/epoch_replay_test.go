package game_test

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func TestExecutionAuthorityEpochReplay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 31, 9, 20, 0, 0, time.UTC)
	attemptAuthority, attemptCommand := task042FailedAttemptFixture(t, now)
	attemptCommand.FailureClass = gamedomain.FailureExecutionEpochBreak
	current := task042Lease(now, attemptCommand.Scope.TournamentID, 2)
	broken := &authoritydomain.Stamp{LeaseID: task042ID(70), Epoch: 1}
	command := gameusecase.EpochReplayCommand{
		CurrentAuthority: current.Identity(), BrokenAuthority: *broken, RosterID: task042ID(120), Attempt: attemptCommand,
	}
	repository := newExecutionEpochReplayRepositoryHarness(t, gameusecase.EpochReplayAuthority{
		Lease: current, BoundAuthority: *broken, RosterID: task042ID(120), Attempt: attemptAuthority,
	}, executionEpochReplayRepositoryOptions{})
	usecase := gameusecase.NewEpochReplayUseCase(repository, newMutableEpochReplayTimeSource(t, now).mock)

	record, changed, err := usecase.Replay(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, domain.GameResultReasonExecutionEpochBreak, record.Attempt.Game.ResultReason)
	require.Equal(t, domain.SeriesStateReplayRequired, record.Attempt.Series.Series.State)
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
	repository.update(func(authority *gameusecase.EpochReplayAuthority) {
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
	require.Equal(t, &wantRecord, replayed)
	require.False(t, changed)
	require.NoError(t, err)
	require.Equal(t, 1, repository.writeCount())

	changedPayload := command
	changedPayload.Attempt.Revisions.AuditEventID = task042ID(75)
	replayed, changed, err = usecase.Replay(t.Context(), changedPayload)
	require.Nil(t, replayed)
	require.False(t, changed)
	require.ErrorIs(t, err, gameusecase.ErrEpochReplayCommandReuse)
	require.Equal(t, 1, repository.writeCount())
}

func TestExecutionAuthorityEpochReplayBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("uses database time rather than a skewed process clock", func(t *testing.T) {
		t.Parallel()

		databaseNow := time.Date(2026, 8, 31, 9, 21, 0, 0, time.UTC)
		authority, command := task042ReplayAuthority(t, databaseNow)
		hostNow := authority.Lease.ExpiresAt.Add(time.Hour)
		require.False(t, authority.Lease.Proves(command.CurrentAuthority, hostNow))
		repository := newExecutionEpochReplayRepositoryHarness(
			t,
			authority,
			executionEpochReplayRepositoryOptions{},
		)

		record, changed, err := gameusecase.NewEpochReplayUseCase(
			repository,
			newMutableEpochReplayTimeSource(t, databaseNow).mock,
		).Replay(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, databaseNow, record.Attempt.TerminalizedAt)
	})

	t.Run("rejects wrapper and failed-attempt current mismatches", func(t *testing.T) {
		t.Parallel()

		for _, corrupt := range []func(*gameusecase.EpochReplayAuthority){
			func(authority *gameusecase.EpochReplayAuthority) {
				authority.Attempt.Current = nil
			},
			func(authority *gameusecase.EpochReplayAuthority) {
				mismatch := authority.Current.Attempt
				mismatch.CommandID = task042ID(100)
				authority.Attempt.Current = &mismatch
			},
		} {
			now := time.Date(2026, 8, 31, 9, 22, 0, 0, time.UTC)
			authority, command := task042ReplayAuthority(t, now)
			repository := newExecutionEpochReplayRepositoryHarness(t, authority, executionEpochReplayRepositoryOptions{})
			usecase := gameusecase.NewEpochReplayUseCase(repository, newMutableEpochReplayTimeSource(t, now).mock)
			_, changed, err := usecase.Replay(t.Context(), command)
			require.NoError(t, err)
			require.True(t, changed)
			repository.hideCurrentReplay()
			repository.update(corrupt)

			replayed, changed, err := usecase.Replay(t.Context(), command)
			require.Nil(t, replayed)
			require.False(t, changed)
			require.ErrorIs(t, err, gameusecase.ErrInvalidEpochReplay)
			require.Equal(t, 1, repository.writeCount())
		}
	})

	t.Run("rejects attempt ordinal and projection overflow", func(t *testing.T) {
		t.Parallel()

		for _, overflow := range []func(*gameusecase.EpochReplayAuthority){
			func(authority *gameusecase.EpochReplayAuthority) {
				authority.Attempt.CurrentProjectionRevision = math.MaxInt64
			},
			func(authority *gameusecase.EpochReplayAuthority) {
				authority.Attempt.CurrentOrdinal = task042MaxInt() - 1
				revisionID := domain.SeriesScoreRevisionID(task042ID(101))
				authority.Attempt.Series.Series.CurrentScoreRevisionID = &revisionID
			},
		} {
			now := time.Date(2026, 8, 31, 9, 24, 0, 0, time.UTC)
			authority, command := task042ReplayAuthority(t, now)
			overflow(&authority)
			repository := newExecutionEpochReplayRepositoryHarness(t, authority, executionEpochReplayRepositoryOptions{})
			replayed, changed, err := gameusecase.NewEpochReplayUseCase(
				repository,
				newMutableEpochReplayTimeSource(t, now).mock,
			).Replay(t.Context(), command)
			require.Nil(t, replayed)
			require.False(t, changed)
			require.ErrorIs(t, err, gameusecase.ErrInvalidEpochReplay)
			require.Equal(t, 0, repository.writeCount())
		}
	})

	t.Run("fences live lease at replay commit time", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 31, 9, 26, 0, 0, time.UTC)
		authority, command := task042ReplayAuthority(t, now)
		repository := newExecutionEpochReplayRepositoryHarness(t, authority, executionEpochReplayRepositoryOptions{
			transactionNow:      now,
			advanceBeforeCommit: authority.Lease.ExpiresAt.Sub(now),
		})
		replayed, changed, err := gameusecase.NewEpochReplayUseCase(
			repository,
			newMutableEpochReplayTimeSource(t, now).mock,
		).Replay(t.Context(), command)
		require.Nil(t, replayed)
		require.False(t, changed)
		require.ErrorIs(t, err, gameusecase.ErrEpochReplayConflict)
		require.Equal(t, 2, repository.commitCount())
		require.Equal(t, 0, repository.writeCount())
		wrapperCurrent, attemptCurrent := repository.currentState()
		require.False(t, wrapperCurrent)
		require.False(t, attemptCurrent)
	})
}
