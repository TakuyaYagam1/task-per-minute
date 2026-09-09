package golden_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
)

func TestAtomicGoldenStart(t *testing.T) {
	t.Parallel()

	openedAt := time.Date(2026, 9, 1, 14, 0, 0, 0, time.UTC)
	startedAt := openedAt.Add(20 * time.Second)
	state := waveTask048GoldenState(t, openedAt)
	repository := waveNewGoldenWaveRepositoryHarness(t, state)
	execution := waveTask048OpenGoldenExecution(t, repository, state, openedAt, 3000)
	execution = waveTask048ReadyAll(t, repository, execution, openedAt, 3100)
	authority := waveTask048AuthorityLease(startedAt, state.Scope.TournamentID)
	repository.setAuthority(authority, startedAt)
	command := waveTask048StartCommand(execution, authority, 3300)
	assignmentBefore := execution.Assignment

	started, changed, err := goldenusecase.NewGoldenStartUseCase(
		repository,
		waveNewGoldenClock(t, startedAt),
	).Start(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.WaveStateActive, started.Wave.State)
	require.NotNil(t, started.Wave.StartedAt)
	require.Equal(t, startedAt, *started.Wave.StartedAt)
	require.Equal(t, domain.ReadyWindowStateConsumed, started.Wave.ReadyWindow.State)
	require.Equal(t, goldenusecase.GoldenReadyWindowConsumed, started.Window.State)
	require.Equal(t, domain.GoldenAttemptStateActive, started.Attempt.State)
	require.NotNil(t, started.Attempt.StartedAt)
	require.Equal(t, startedAt, *started.Attempt.StartedAt)
	require.Equal(t, assignmentBefore, started.Assignment, "immutable assignment changed during start")
	require.NotNil(t, started.Start)
	wantDeadline := startedAt.Add(time.Duration(started.Assignment.Snapshot.TimeLimit) * time.Second)
	require.Equal(t, wantDeadline, started.Start.Deadline)
	require.Equal(t, authority.Identity(), started.Start.Authority.Identity)
	require.Equal(t, authority.Epoch, started.Start.Authority.Identity.Epoch)
	require.NotZero(t, started.Start.Authority.LeaseDigest)
	require.Len(t, started.Start.Assignments, len(started.Membership.ParticipantIDs))
	for _, assignment := range started.Start.Assignments {
		require.True(t, assignment.DeliveryEnabled)
		require.Equal(t, startedAt, assignment.StartedAt)
		require.Equal(t, wantDeadline, assignment.Deadline)
		require.Equal(t, authority.Identity(), assignment.Authority)
		require.Equal(t, started.Start.Authority.LeaseDigest, assignment.AuthorityDigest)
		require.Equal(t, started.Assignment.Snapshot.SnapshotID, assignment.SnapshotID)
		require.Equal(t, started.Assignment.ContentDigest, assignment.ContentDigest)
	}
	startedParticipants := make([]uuid.UUID, len(started.Start.Assignments))
	startedAssignmentIDs := make([]uuid.UUID, len(started.Start.Assignments))
	wantAssignmentIDs := make([]uuid.UUID, len(started.Assignment.Private))
	for index, assignment := range started.Start.Assignments {
		startedParticipants[index] = assignment.ParticipantID
		startedAssignmentIDs[index] = assignment.AssignmentID
		wantAssignmentIDs[index] = started.Assignment.Private[index].ID
	}
	require.Equal(t, started.Membership.ParticipantIDs, startedParticipants)
	require.Equal(t, wantAssignmentIDs, startedAssignmentIDs)

	storedBeforeTamper, loadErr := repository.LoadGoldenWaveExecution(t.Context(), state.Scope)
	require.NoError(t, loadErr)
	started.Start.Assignments[0].ParticipantID = task048ID(8990)
	lastReceipt := len(started.Receipts) - 1
	require.NotNil(t, started.Receipts[lastReceipt].Expected)
	started.Receipts[lastReceipt].Expected.RevisionID = task048ID(8991)
	storedAfterTamper, loadErr := repository.LoadGoldenWaveExecution(t.Context(), state.Scope)
	require.NoError(t, loadErr)
	require.Equal(t, storedBeforeTamper.Start.Assignments, storedAfterTamper.Start.Assignments)
	require.Equal(t, storedBeforeTamper.Receipts, storedAfterTamper.Receipts)
	require.NoError(t, storedAfterTamper.Validate())
	storedAfterTamper.Start.Assignments[0].ParticipantID = task048ID(8992)
	storedAfterTamper.Receipts[lastReceipt].Expected.RevisionID = task048ID(8993)
	detachedReload, loadErr := repository.LoadGoldenWaveExecution(t.Context(), state.Scope)
	require.NoError(t, loadErr)
	require.Equal(t, storedBeforeTamper.Start.Assignments, detachedReload.Start.Assignments)
	require.Equal(t, storedBeforeTamper.Receipts, detachedReload.Receipts)
	started = detachedReload
	tamperedGroup := started.Snapshot()
	tamperedGroup.Group.Members[0].Excluded = true
	require.Error(t, tamperedGroup.Validate(), "started Group must retain its immutable opening binding")

	t.Run("replays before source drift and rejects command reuse", func(t *testing.T) {
		repository.setStateLoadError(errors.New("start replay must not read source"))
		replayed, replayChanged, replayErr := goldenusecase.NewGoldenStartUseCase(
			repository,
			waveNewGoldenClock(t, startedAt.Add(time.Minute)),
		).Start(t.Context(), command)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, started.Expectation(), replayed.Expectation())
		repository.setStateLoadError(nil)

		reused := command
		reused.NextExecutionRevisionID = task048ID(3399)
		result, reusedChanged, reusedErr := goldenusecase.NewGoldenStartUseCase(
			repository,
			waveNewGoldenClock(t, startedAt),
		).Start(t.Context(), reused)
		require.Nil(t, result)
		require.False(t, reusedChanged)
		require.ErrorIs(t, reusedErr, goldenusecase.ErrGoldenWaveCommandReuse)
	})

	t.Run("changes nothing unless every active member is ready", func(t *testing.T) {
		localState := waveTask048GoldenState(t, openedAt)
		localRepository := waveNewGoldenWaveRepositoryHarness(t, localState)
		local := waveTask048OpenGoldenExecution(t, localRepository, localState, openedAt, 4000)
		participantID := local.Membership.ParticipantIDs[0]
		ready, readyChanged, readyErr := goldenusecase.NewGoldenReadinessUseCase(
			localRepository,
			waveNewGoldenClock(t, openedAt.Add(time.Second)),
		).MarkReady(t.Context(), waveTask048ReadyCommand(local, participantID, 4100))
		require.NoError(t, readyErr)
		require.True(t, readyChanged)
		localAuthority := waveTask048AuthorityLease(startedAt, localState.Scope.TournamentID)
		localRepository.setAuthority(localAuthority, startedAt)
		before := ready.Expectation()
		beforeCommits := localRepository.commitCount()
		result, localChanged, startErr := goldenusecase.NewGoldenStartUseCase(
			localRepository,
			waveNewGoldenClock(t, startedAt),
		).Start(t.Context(), waveTask048StartCommand(*ready, localAuthority, 4200))
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, startErr, goldenusecase.ErrGoldenWaveAuthorityConflict)
		require.Equal(t, beforeCommits, localRepository.commitCount())
		after, loadErr := localRepository.LoadGoldenWaveExecution(t.Context(), localState.Scope)
		require.NoError(t, loadErr)
		require.Equal(t, before, after.Expectation())
	})

	t.Run("transaction rejects a lease that expires after usecase validation", func(t *testing.T) {
		localState := waveTask048GoldenState(t, openedAt)
		localRepository := waveNewGoldenWaveRepositoryHarness(t, localState)
		local := waveTask048OpenGoldenExecution(t, localRepository, localState, openedAt, 5000)
		local = waveTask048ReadyAll(t, localRepository, local, openedAt, 5100)
		localAuthority := waveTask048AuthorityLease(startedAt, localState.Scope.TournamentID)
		localRepository.setAuthority(localAuthority, localAuthority.ExpiresAt)
		before := local.Expectation()
		beforeCommits := localRepository.commitCount()
		result, localChanged, startErr := goldenusecase.NewGoldenStartUseCase(
			localRepository,
			waveNewGoldenClock(t, startedAt),
		).Start(t.Context(), waveTask048StartCommand(local, localAuthority, 5300))
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, startErr, goldenusecase.ErrGoldenWaveAuthorityNotLive)
		require.Equal(t, beforeCommits, localRepository.commitCount())
		after, loadErr := localRepository.LoadGoldenWaveExecution(t.Context(), localState.Scope)
		require.NoError(t, loadErr)
		require.Equal(t, before, after.Expectation())
	})

	t.Run("repository failure cannot persist a partial start", func(t *testing.T) {
		localState := waveTask048GoldenState(t, openedAt)
		localRepository := waveNewGoldenWaveRepositoryHarness(t, localState)
		local := waveTask048OpenGoldenExecution(t, localRepository, localState, openedAt, 6000)
		local = waveTask048ReadyAll(t, localRepository, local, openedAt, 6100)
		localAuthority := waveTask048AuthorityLease(startedAt, localState.Scope.TournamentID)
		localRepository.setAuthority(localAuthority, startedAt)
		localRepository.setCommitError(errors.New("atomic write failed"))
		before := local.Expectation()
		result, localChanged, startErr := goldenusecase.NewGoldenStartUseCase(
			localRepository,
			waveNewGoldenClock(t, startedAt),
		).Start(t.Context(), waveTask048StartCommand(local, localAuthority, 6300))
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorContains(t, startErr, "atomic write failed")
		localRepository.setCommitError(nil)
		after, loadErr := localRepository.LoadGoldenWaveExecution(t.Context(), localState.Scope)
		require.NoError(t, loadErr)
		require.Equal(t, before, after.Expectation())
	})

	t.Run("serializes concurrent identical starts", func(t *testing.T) {
		localState := waveTask048GoldenState(t, openedAt)
		localRepository := waveNewGoldenWaveRepositoryHarness(t, localState)
		local := waveTask048OpenGoldenExecution(t, localRepository, localState, openedAt, 7000)
		local = waveTask048ReadyAll(t, localRepository, local, openedAt, 7100)
		localAuthority := waveTask048AuthorityLease(startedAt, localState.Scope.TournamentID)
		localRepository.setAuthority(localAuthority, startedAt)
		localRepository.setExecutionBarrier(waveNewGoldenLoadBarrier(len(local.Membership.ParticipantIDs)))
		localCommand := waveTask048StartCommand(local, localAuthority, 7300)
		type result struct {
			execution *goldenusecase.GoldenWaveExecution
			changed   bool
			err       error
		}
		results := [2]result{}
		clock := waveNewGoldenClock(t, startedAt)
		var group sync.WaitGroup
		for index := range results {
			group.Add(1)
			go func(index int) {
				defer group.Done()
				results[index].execution, results[index].changed, results[index].err = goldenusecase.NewGoldenStartUseCase(
					localRepository,
					clock,
				).Start(t.Context(), localCommand)
			}(index)
		}
		barrier := localRepository.executionLoadBarrier()
		for range 2 {
			<-barrier.arrived
		}
		close(barrier.release)
		group.Wait()
		require.NoError(t, results[0].err)
		require.NoError(t, results[1].err)
		require.NotEqual(t, results[0].changed, results[1].changed)
		require.Equal(t, results[0].execution.Expectation(), results[1].execution.Expectation())
	})

	t.Run("accepts same-lease renewal and the inclusive window deadline", func(t *testing.T) {
		localState := waveTask048GoldenState(t, openedAt)
		localRepository := waveNewGoldenWaveRepositoryHarness(t, localState)
		local := waveTask048OpenGoldenExecution(t, localRepository, localState, openedAt, 7400)
		local = waveTask048ReadyAll(t, localRepository, local, openedAt, 7500)
		deadline := openedAt.Add(30 * time.Second)
		localAuthority := waveTask048AuthorityLease(deadline, localState.Scope.TournamentID)
		localAuthority.Previous = &authoritydomain.Stamp{
			LeaseID: localAuthority.LeaseID,
			Epoch:   localAuthority.Epoch,
		}
		localRepository.setAuthority(localAuthority, deadline)
		result, localChanged, startErr := goldenusecase.NewGoldenStartUseCase(
			localRepository,
			waveNewGoldenClock(t, deadline),
		).Start(t.Context(), waveTask048StartCommand(local, localAuthority, 7700))
		require.NoError(t, startErr)
		require.True(t, localChanged)
		require.Equal(t, deadline, result.Start.StartedAt)
	})

	t.Run("closes the window one nanosecond after its deadline", func(t *testing.T) {
		localState := waveTask048GoldenState(t, openedAt)
		localRepository := waveNewGoldenWaveRepositoryHarness(t, localState)
		local := waveTask048OpenGoldenExecution(t, localRepository, localState, openedAt, 7800)
		local = waveTask048ReadyAll(t, localRepository, local, openedAt, 7900)
		afterDeadline := openedAt.Add(30*time.Second + time.Nanosecond)
		localAuthority := waveTask048AuthorityLease(afterDeadline, localState.Scope.TournamentID)
		localRepository.setAuthority(localAuthority, afterDeadline)
		before := local.Expectation()
		result, localChanged, startErr := goldenusecase.NewGoldenStartUseCase(
			localRepository,
			waveNewGoldenClock(t, afterDeadline),
		).Start(t.Context(), waveTask048StartCommand(local, localAuthority, 8100))
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, startErr, goldenusecase.ErrGoldenReadyWindowClosed)
		after, loadErr := localRepository.LoadGoldenWaveExecution(t.Context(), localState.Scope)
		require.NoError(t, loadErr)
		require.Equal(t, before, after.Expectation())
	})

	t.Run("reconciles a start winner archived after load", func(t *testing.T) {
		localState := waveTask048GoldenState(t, openedAt)
		localRepository := waveNewGoldenWaveRepositoryHarness(t, localState)
		local := waveTask048OpenGoldenExecution(t, localRepository, localState, openedAt, 8200)
		local = waveTask048ReadyAll(t, localRepository, local, openedAt, 8300)
		localAuthority := waveTask048AuthorityLease(startedAt, localState.Scope.TournamentID)
		command := waveTask048StartCommand(local, localAuthority, 8500)

		winnerExecution := local.Snapshot()
		winnerRepository := waveNewGoldenWaveRepositoryHarness(t, localState)
		winnerRepository.setExecution(winnerExecution)
		winnerRepository.setAuthority(localAuthority, startedAt)
		winner, winnerChanged, winnerErr := goldenusecase.NewGoldenStartUseCase(
			winnerRepository,
			waveNewGoldenClock(t, startedAt),
		).Start(t.Context(), command)
		require.NoError(t, winnerErr)
		require.True(t, winnerChanged)

		beforeCommits := localRepository.commitCount()
		localRepository.setStateLoadError(errors.New("start replay must stay source-free"))
		localRepository.setPostLoadWinnerArchive(*winner)
		replayed, replayChanged, replayErr := goldenusecase.NewGoldenStartUseCase(
			localRepository,
			waveNewGoldenClock(t, startedAt.Add(time.Second)),
		).Start(t.Context(), command)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, winner.Expectation(), replayed.Expectation())
		require.Equal(t, beforeCommits, localRepository.commitCount())
	})

	t.Run("rejects transaction-time authority mismatches atomically", func(t *testing.T) {
		testCases := []struct {
			name      string
			configure func(*waveGoldenWaveRepositoryHarness, authoritydomain.Lease)
		}{
			{
				name: "identity",
				configure: func(repository *waveGoldenWaveRepositoryHarness, lease authoritydomain.Lease) {
					lease.HolderID = task048ID(8801)
					repository.setAuthority(lease, startedAt)
				},
			},
			{
				name: "lease revision",
				configure: func(repository *waveGoldenWaveRepositoryHarness, lease authoritydomain.Lease) {
					repository.setAuthority(lease, startedAt)
					repository.setAuthorityRevisionOverride(lease.Revision + 1)
				},
			},
			{
				name: "lease digest",
				configure: func(repository *waveGoldenWaveRepositoryHarness, lease authoritydomain.Lease) {
					repository.setAuthority(lease, startedAt)
					digest := goldenusecase.GoldenExecutionAuthorityDigest(lease)
					digest[0] ^= 0xff
					repository.setAuthorityDigestOverride(digest)
				},
			},
		}
		for index, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				localState := waveTask048GoldenState(t, openedAt)
				localRepository := waveNewGoldenWaveRepositoryHarness(t, localState)
				base := 9000 + index*300
				local := waveTask048OpenGoldenExecution(t, localRepository, localState, openedAt, base)
				local = waveTask048ReadyAll(t, localRepository, local, openedAt, base+100)
				localAuthority := waveTask048AuthorityLease(startedAt, localState.Scope.TournamentID)
				testCase.configure(localRepository, localAuthority)
				before := local.Expectation()
				beforeCommits := localRepository.commitCount()
				result, localChanged, startErr := goldenusecase.NewGoldenStartUseCase(
					localRepository,
					waveNewGoldenClock(t, startedAt),
				).Start(t.Context(), waveTask048StartCommand(local, localAuthority, base+200))
				require.Nil(t, result)
				require.False(t, localChanged)
				require.ErrorIs(t, startErr, goldenusecase.ErrGoldenWaveAuthorityNotLive)
				require.Equal(t, beforeCommits, localRepository.commitCount())
				after, afterErr := localRepository.LoadGoldenWaveExecution(t.Context(), localState.Scope)
				require.NoError(t, afterErr)
				require.Equal(t, before, after.Expectation())
			})
		}
	})
}

func waveTask048ReadyAll(
	t *testing.T,
	repository *waveGoldenWaveRepositoryHarness,
	execution goldenusecase.GoldenWaveExecution,
	openedAt time.Time,
	base int,
) goldenusecase.GoldenWaveExecution {
	t.Helper()
	current := execution
	for index, participantID := range execution.Membership.ParticipantIDs {
		ready, changed, err := goldenusecase.NewGoldenReadinessUseCase(
			repository,
			waveNewGoldenClock(t, openedAt.Add(time.Duration(index+1)*time.Second)),
		).MarkReady(t.Context(), waveTask048ReadyCommand(current, participantID, base+index*10))
		require.NoError(t, err)
		require.True(t, changed)
		current = *ready
	}
	return current
}

func waveTask048AuthorityLease(
	now time.Time,
	tournamentID uuid.UUID,
) authoritydomain.Lease {
	return authoritydomain.Lease{
		TournamentID: tournamentID, HolderID: task048ID(8001), LeaseID: task048ID(8002),
		Epoch: 2, ProcessKind: authoritydomain.ProcessAuthority, Revision: 2,
		CommandID:  task048ID(8003),
		Previous:   &authoritydomain.Stamp{LeaseID: task048ID(8004), Epoch: 1},
		AcquiredAt: now.Add(-time.Minute), RenewedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute),
	}
}

func waveTask048StartCommand(
	execution goldenusecase.GoldenWaveExecution,
	authority authoritydomain.Lease,
	base int,
) goldenusecase.GoldenStartCommand {
	return goldenusecase.GoldenStartCommand{
		Scope: execution.Scope, CommandID: task048ID(base),
		AttemptID: execution.Attempt.ID, WaveID: execution.Wave.ID, WindowID: execution.Window.ID,
		ExpectedState: execution.Source, ExpectedExecution: execution.Expectation(),
		NextExecutionRevisionID: task048ID(base + 1), NextWindowRevisionID: task048ID(base + 2),
		Authority: authority,
	}
}
