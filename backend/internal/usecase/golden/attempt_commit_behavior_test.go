package golden_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGoldenAttemptCommit(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC)
	execution := attemptTask049StartedExecution(t, startedAt)
	scope := attemptTask049SubmissionScope(execution)
	submissions, err := goldenusecase.NewGoldenSubmissionLedger(scope, attemptTask049ID(11001))
	require.NoError(t, err)
	submissionRepository := attemptNewTask049SubmissionHarness(t, execution, submissions, startedAt.Add(5*time.Second))
	participantID := execution.Membership.ParticipantIDs[0]
	verification := attemptTask049Verification(scope, execution, participantID, 11100)
	submissionRepository.verifications[verification.ID] = verification
	submissionCommand := goldenusecase.GoldenSubmissionCommand{
		Scope: scope, CommandID: attemptTask049ID(11110), ActorParticipantID: participantID,
		ParticipantID: participantID, VerificationID: verification.ID,
		ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: attemptTask049ID(11111),
	}
	submitted, changed, err := goldenusecase.NewGoldenSubmissionUseCase(submissionRepository).Submit(t.Context(), submissionCommand)
	require.NoError(t, err)
	require.True(t, changed)

	sentinel := attemptTask049SwissPointSentinel(11200)
	positions, err := goldenusecase.NewGoldenPositionLedger(
		execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, attemptTask049ID(11210),
	)
	require.NoError(t, err)
	repository := attemptNewTask049CommitHarness(t, submissionRepository, *submitted, positions, sentinel)
	command := goldenusecase.GoldenAttemptCommitCommand{
		Scope: scope, CommandID: attemptTask049ID(11300), CommitID: attemptTask049ID(11301),
		ExpectedExecution: execution.Expectation(), ExpectedSubmissions: submitted.Expectation(),
		ExpectedPositions: positions.Expectation(), ExpectedSwissPoints: sentinel,
		NextPositionRevisionID: attemptTask049ID(11302), Reason: goldenusecase.GoldenAttemptTerminalDeadline,
	}
	record, changed, err := goldenusecase.NewGoldenAttemptCommitUseCase(
		repository,
		attemptNewGoldenClock(t, execution.Start.Deadline),
	).CommitAttempt(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, domain.GoldenAttemptStateCompleted, record.Attempt.State)
	require.Equal(t, domain.WaveStateCompleted, record.Wave.State)
	require.Equal(t, execution.Expectation(), record.ActiveExecution)
	require.Equal(t, sentinel, record.SwissPoints)
	require.Len(t, record.Positions.Positions, 1)
	require.Equal(t, execution.Group.PositionFrom, record.Positions.Positions[0].Position)
	require.Equal(t, participantID, record.Positions.Positions[0].ParticipantID)
	require.Equal(t, submitted.Submissions[0].ID, record.Positions.Positions[0].SubmissionID)
	require.Len(t, record.Ordering.Order, 1)
	require.Equal(t, record.Positions.Positions[0].SubmissionID, record.Ordering.Order[0].SubmissionID)
	require.Equal(t, submitted.Expectation(), record.Ordering.SubmissionHead)
	require.Equal(t, positions, record.PriorPositions)
	require.Equal(t, []uuid.UUID{positions.RevisionID, command.NextPositionRevisionID}, record.Positions.RevisionIDs)

	t.Run("replays after archive and never duplicates a position", func(t *testing.T) {
		repository.archiveCurrent()
		repository.loadError = errors.New("mutable heads must not be loaded on replay")
		beforeReplayCommits := repository.commitCount()
		beforeReplay := repository.stateSnapshot()
		replayed, replayChanged, replayErr := goldenusecase.NewGoldenAttemptCommitUseCase(
			repository,
			attemptNewGoldenClock(t, execution.Start.Deadline.Add(time.Minute)),
		).CommitAttempt(t.Context(), command)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, record.PayloadDigest, replayed.PayloadDigest)
		require.Len(t, replayed.Positions.Positions, 1)
		require.Equal(t, beforeReplayCommits, repository.commitCount())
		require.Equal(t, beforeReplay, repository.stateSnapshot())

		reused := command
		reused.CommitID = attemptTask049ID(11320)
		beforeReuseCommits := repository.commitCount()
		beforeReuse := repository.stateSnapshot()
		result, reusedChanged, reusedErr := goldenusecase.NewGoldenAttemptCommitUseCase(
			repository,
			attemptNewGoldenClock(t, execution.Start.Deadline),
		).CommitAttempt(t.Context(), reused)
		require.Nil(t, result)
		require.False(t, reusedChanged)
		require.ErrorIs(t, reusedErr, goldenusecase.ErrGoldenAttemptCommitCommandReuse)
		require.Equal(t, beforeReuseCommits, repository.commitCount())
		require.Equal(t, beforeReuse, repository.stateSnapshot())
		repository.restoreCurrent()
		repository.loadError = nil
	})

	t.Run("terminal-first submission is rejected without a write", func(t *testing.T) {
		verification := attemptTask049Verification(scope, execution, execution.Membership.ParticipantIDs[1], 11400)
		submissionRepository.verifications[verification.ID] = verification
		late := goldenusecase.GoldenSubmissionCommand{
			Scope: scope, CommandID: attemptTask049ID(11410), ActorParticipantID: verification.ParticipantID,
			ParticipantID: verification.ParticipantID, VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: attemptTask049ID(11411),
		}
		before := submissionRepository.commitCount()
		result, lateChanged, lateErr := goldenusecase.NewGoldenSubmissionUseCase(submissionRepository).Submit(t.Context(), late)
		require.Nil(t, result)
		require.False(t, lateChanged)
		require.ErrorIs(t, lateErr, goldenusecase.ErrGoldenSubmissionClosed)
		require.Equal(t, before, submissionRepository.commitCount())
	})

	t.Run("terminal rejects retained submissions at the deadline", func(t *testing.T) {
		lateSubmissions := submitted.Snapshot()
		lateSubmissions.Submissions[0].CommittedAt = execution.Start.Deadline
		lateSubmissions.Receipts[0].CommittedAt = execution.Start.Deadline
		attemptTask049SealSubmissionLedger(t, &lateSubmissions)
		require.NoError(t, lateSubmissions.Validate())

		localSubmission := attemptNewTask049SubmissionHarness(t, execution, lateSubmissions, startedAt.Add(5*time.Second))
		local := attemptNewTask049CommitHarness(t, localSubmission, lateSubmissions, positions, sentinel)
		localCommand := command
		localCommand.CommandID = attemptTask049ID(11412)
		localCommand.CommitID = attemptTask049ID(11413)
		localCommand.NextPositionRevisionID = attemptTask049ID(11414)
		localCommand.ExpectedSubmissions = lateSubmissions.Expectation()
		result, localChanged, commitErr := goldenusecase.NewGoldenAttemptCommitUseCase(
			local,
			attemptNewGoldenClock(t, execution.Start.Deadline),
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, goldenusecase.ErrGoldenAttemptCommitAuthorityConflict)
		require.Zero(t, local.commitCount())
	})

	t.Run("submission and terminal settlement share one transaction boundary", func(t *testing.T) {
		t.Run("submit first makes the stale terminal conflict", func(t *testing.T) {
			localSubmission := attemptNewTask049SubmissionHarness(t, execution, *submitted, startedAt.Add(6*time.Second))
			lateParticipant := execution.Membership.ParticipantIDs[1]
			lateVerification := attemptTask049Verification(scope, execution, lateParticipant, 11420)
			localSubmission.verifications[lateVerification.ID] = lateVerification
			lateCommand := goldenusecase.GoldenSubmissionCommand{
				Scope: scope, CommandID: attemptTask049ID(11422), ActorParticipantID: lateParticipant,
				ParticipantID: lateParticipant, VerificationID: lateVerification.ID,
				ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: attemptTask049ID(11423),
			}
			localPositions, buildErr := goldenusecase.NewGoldenPositionLedger(
				execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, attemptTask049ID(11424),
			)
			require.NoError(t, buildErr)
			local := attemptNewTask049CommitHarness(t, localSubmission, *submitted, localPositions, sentinel)
			localCommand := command
			localCommand.CommandID, localCommand.CommitID = attemptTask049ID(11425), attemptTask049ID(11426)
			localCommand.NextPositionRevisionID = attemptTask049ID(11427)
			localCommand.ExpectedPositions = localPositions.Expectation()
			loaded, signalLoaded := attemptTask049Latch(t)
			release, signalRelease := attemptTask049Latch(t)
			local.beforeCommit = func() {
				signalLoaded()
				attemptTask049AwaitSignal(t, release, "submit-first terminal release")
			}

			type terminalResult struct {
				record  *goldenusecase.GoldenAttemptCommitRecord
				changed bool
				err     error
			}
			finished := make(chan terminalResult, 1)
			go func() {
				value, didChange, commitErr := goldenusecase.NewGoldenAttemptCommitUseCase(
					local,
					attemptNewGoldenClock(t, execution.Start.Deadline),
				).CommitAttempt(t.Context(), localCommand)
				finished <- terminalResult{record: value, changed: didChange, err: commitErr}
			}()
			require.True(t, attemptTask049AwaitSignal(t, loaded, "submit-first terminal commit entry"))
			lateLedger, lateChanged, lateErr := goldenusecase.NewGoldenSubmissionUseCase(localSubmission).Submit(
				t.Context(), lateCommand,
			)
			require.NoError(t, lateErr)
			require.True(t, lateChanged)
			require.Len(t, lateLedger.Submissions, 2)
			signalRelease()
			stale, received := attemptTask049AwaitValue(t, finished, "submit-first terminal result")
			require.True(t, received)
			require.Nil(t, stale.record)
			require.False(t, stale.changed)
			require.ErrorIs(t, stale.err, goldenusecase.ErrGoldenAttemptCommitAuthorityConflict)
			require.Zero(t, local.commitCount())
			local.beforeCommit = nil

			fresh := localCommand
			fresh.CommandID, fresh.CommitID = attemptTask049ID(11428), attemptTask049ID(11429)
			fresh.NextPositionRevisionID = attemptTask049ID(11430)
			fresh.ExpectedSubmissions = lateLedger.Expectation()
			settled, settledChanged, settleErr := goldenusecase.NewGoldenAttemptCommitUseCase(
				local,
				attemptNewGoldenClock(t, execution.Start.Deadline),
			).CommitAttempt(t.Context(), fresh)
			require.NoError(t, settleErr)
			require.True(t, settledChanged)
			require.Len(t, settled.Positions.Positions, 2)
			require.Len(t, settled.Ordering.Order, 2)
			require.Equal(t, 1, local.commitCount())
		})

		t.Run("terminal first closes a late submission", func(t *testing.T) {
			localSubmission := attemptNewTask049SubmissionHarness(t, execution, *submitted, startedAt.Add(6*time.Second))
			lateParticipant := execution.Membership.ParticipantIDs[1]
			lateVerification := attemptTask049Verification(scope, execution, lateParticipant, 11440)
			localSubmission.verifications[lateVerification.ID] = lateVerification
			lateCommand := goldenusecase.GoldenSubmissionCommand{
				Scope: scope, CommandID: attemptTask049ID(11442), ActorParticipantID: lateParticipant,
				ParticipantID: lateParticipant, VerificationID: lateVerification.ID,
				ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: attemptTask049ID(11443),
			}
			localPositions, buildErr := goldenusecase.NewGoldenPositionLedger(
				execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, attemptTask049ID(11444),
			)
			require.NoError(t, buildErr)
			local := attemptNewTask049CommitHarness(t, localSubmission, *submitted, localPositions, sentinel)
			localCommand := command
			localCommand.CommandID, localCommand.CommitID = attemptTask049ID(11445), attemptTask049ID(11446)
			localCommand.NextPositionRevisionID = attemptTask049ID(11447)
			localCommand.ExpectedPositions = localPositions.Expectation()
			locked, signalLocked := attemptTask049Latch(t)
			release, signalRelease := attemptTask049Latch(t)
			local.insideCommit = func() {
				signalLocked()
				attemptTask049AwaitSignal(t, release, "terminal-first transaction release")
			}

			terminalFinished := make(chan error, 1)
			go func() {
				_, _, commitErr := goldenusecase.NewGoldenAttemptCommitUseCase(
					local,
					attemptNewGoldenClock(t, execution.Start.Deadline),
				).CommitAttempt(t.Context(), localCommand)
				terminalFinished <- commitErr
			}()
			require.True(t, attemptTask049AwaitSignal(t, locked, "terminal-first transaction entry"))
			submitEntered, signalSubmitEntered := attemptTask049Latch(t)
			localSubmission.beforeCommit = signalSubmitEntered
			type submitResult struct {
				ledger  *goldenusecase.GoldenSubmissionLedger
				changed bool
				err     error
			}
			submitFinished := make(chan submitResult, 1)
			go func() {
				value, didChange, submitErr := goldenusecase.NewGoldenSubmissionUseCase(localSubmission).Submit(
					t.Context(), lateCommand,
				)
				submitFinished <- submitResult{ledger: value, changed: didChange, err: submitErr}
			}()
			require.True(t, attemptTask049AwaitSignal(t, submitEntered, "terminal-first submission entry"))
			signalRelease()
			terminalErr, terminalReceived := attemptTask049AwaitValue(t, terminalFinished, "terminal-first terminal result")
			require.True(t, terminalReceived)
			require.NoError(t, terminalErr)
			late, submitReceived := attemptTask049AwaitValue(t, submitFinished, "terminal-first submission result")
			require.True(t, submitReceived)
			require.Nil(t, late.ledger)
			require.False(t, late.changed)
			require.ErrorIs(t, late.err, goldenusecase.ErrGoldenSubmissionClosed)
			require.Equal(t, 1, local.commitCount())
			require.Len(t, localSubmission.snapshot().Submissions, 1)
		})
	})

	t.Run("nonterminal and stale heads write nothing", func(t *testing.T) {
		localSubmission := attemptNewTask049SubmissionHarness(t, execution, *submitted, startedAt.Add(5*time.Second))
		localPositions, buildErr := goldenusecase.NewGoldenPositionLedger(
			execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, attemptTask049ID(11500),
		)
		require.NoError(t, buildErr)
		local := attemptNewTask049CommitHarness(t, localSubmission, *submitted, localPositions, sentinel)
		localCommand := command
		localCommand.CommandID, localCommand.CommitID = attemptTask049ID(11510), attemptTask049ID(11511)
		localCommand.NextPositionRevisionID = attemptTask049ID(11512)
		localCommand.ExpectedPositions = localPositions.Expectation()

		result, localChanged, commitErr := goldenusecase.NewGoldenAttemptCommitUseCase(
			local,
			attemptNewGoldenClock(t, execution.Start.Deadline.Add(-time.Nanosecond)),
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, goldenusecase.ErrGoldenAttemptNotTerminal)
		require.Zero(t, local.commitCount())

		localCommand.ExpectedSubmissions.Revision++
		result, localChanged, commitErr = goldenusecase.NewGoldenAttemptCommitUseCase(
			local,
			attemptNewGoldenClock(t, execution.Start.Deadline),
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, goldenusecase.ErrGoldenAttemptCommitAuthorityConflict)
		require.Zero(t, local.commitCount())
	})

	t.Run("repository error cannot leave terminal or position state", func(t *testing.T) {
		localSubmission := attemptNewTask049SubmissionHarness(t, execution, *submitted, startedAt.Add(5*time.Second))
		localPositions, buildErr := goldenusecase.NewGoldenPositionLedger(
			execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, attemptTask049ID(11600),
		)
		require.NoError(t, buildErr)
		local := attemptNewTask049CommitHarness(t, localSubmission, *submitted, localPositions, sentinel)
		local.commitError = errors.New("atomic settlement failed")
		localCommand := command
		localCommand.CommandID, localCommand.CommitID = attemptTask049ID(11610), attemptTask049ID(11611)
		localCommand.NextPositionRevisionID = attemptTask049ID(11612)
		localCommand.ExpectedPositions = localPositions.Expectation()
		result, localChanged, commitErr := goldenusecase.NewGoldenAttemptCommitUseCase(
			local,
			attemptNewGoldenClock(t, execution.Start.Deadline),
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorContains(t, commitErr, "atomic settlement failed")
		require.Zero(t, local.commitCount())
		require.Empty(t, local.positions.Positions)
		require.Nil(t, local.current)
	})

	t.Run("valid foreign unchanged result is rejected", func(t *testing.T) {
		localSubmission := attemptNewTask049SubmissionHarness(t, execution, *submitted, startedAt.Add(5*time.Second))
		localPositions, buildErr := goldenusecase.NewGoldenPositionLedger(
			execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, attemptTask049ID(11620),
		)
		require.NoError(t, buildErr)
		local := attemptNewTask049CommitHarness(t, localSubmission, *submitted, localPositions, sentinel)
		foreign := record.Snapshot()
		local.returnRecord = &foreign
		local.returnChanged = false
		localCommand := command
		localCommand.CommandID, localCommand.CommitID = attemptTask049ID(11621), attemptTask049ID(11622)
		localCommand.NextPositionRevisionID = attemptTask049ID(11623)
		localCommand.ExpectedPositions = localPositions.Expectation()

		result, localChanged, commitErr := goldenusecase.NewGoldenAttemptCommitUseCase(
			local,
			attemptNewGoldenClock(t, execution.Start.Deadline),
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, domain.ErrInternal)

		changedLocal := attemptNewTask049CommitHarness(t,
			attemptNewTask049SubmissionHarness(t, execution, *submitted, startedAt.Add(5*time.Second)),
			*submitted,
			positions,
			sentinel)

		changedTimestamp := record.Snapshot()
		replacedAt := changedTimestamp.FinishedAt.Add(time.Nanosecond)
		changedTimestamp.FinishedAt = replacedAt
		changedTimestamp.Attempt.FinishedAt = &replacedAt
		changedTimestamp.Group.Attempts[len(changedTimestamp.Group.Attempts)-1].FinishedAt = &replacedAt
		attemptTask049SealAttemptRecord(t, &changedTimestamp)
		require.NoError(t, changedTimestamp.Validate())
		changedLocal.returnRecord = &changedTimestamp
		changedLocal.returnChanged = true
		result, localChanged, commitErr = goldenusecase.NewGoldenAttemptCommitUseCase(
			changedLocal,
			attemptNewGoldenClock(t, execution.Start.Deadline),
		).CommitAttempt(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, domain.ErrInternal)
	})

	t.Run("same-command unchanged result cannot change a command head", func(t *testing.T) {
		localSubmission := attemptNewTask049SubmissionHarness(t, execution, *submitted, startedAt.Add(5*time.Second))
		local := attemptNewTask049CommitHarness(t, localSubmission, *submitted, positions, sentinel)
		tampered := record.Snapshot()
		tampered.SwissPoints = attemptTask049SwissPointSentinel(11624)
		attemptTask049SealAttemptRecord(t, &tampered)
		require.NoError(t, tampered.Validate())
		local.returnRecord = &tampered
		local.returnChanged = false

		result, localChanged, commitErr := goldenusecase.NewGoldenAttemptCommitUseCase(
			local,
			attemptNewGoldenClock(t, execution.Start.Deadline),
		).CommitAttempt(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, domain.ErrInternal)
	})
}
