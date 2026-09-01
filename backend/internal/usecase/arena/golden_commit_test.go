package arena_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenAttemptCommit(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC)
	execution := task049StartedExecution(t, startedAt)
	scope := task049SubmissionScope(execution)
	submissions, err := arena.NewGoldenSubmissionLedger(scope, task049ID(11001))
	require.NoError(t, err)
	submissionRepository := newTask049SubmissionRepository(execution, submissions, startedAt.Add(5*time.Second))
	participantID := execution.Membership.ParticipantIDs[0]
	verification := task049Verification(scope, execution, participantID, 11100)
	submissionRepository.verifications[verification.ID] = verification
	submissionCommand := arena.GoldenSubmissionCommand{
		Scope: scope, CommandID: task049ID(11110), ActorParticipantID: participantID,
		ParticipantID: participantID, VerificationID: verification.ID,
		ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(11111),
	}
	submitted, changed, err := arena.NewGoldenSubmissionUseCase(submissionRepository).Submit(t.Context(), submissionCommand)
	require.NoError(t, err)
	require.True(t, changed)

	sentinel := task049SwissPointSentinel(11200)
	positions, err := arena.NewGoldenPositionLedger(
		execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, task049ID(11210),
	)
	require.NoError(t, err)
	repository := newTask049CommitRepository(submissionRepository, *submitted, positions, sentinel)
	command := arena.GoldenAttemptCommitCommand{
		Scope: scope, CommandID: task049ID(11300), CommitID: task049ID(11301),
		ExpectedExecution: execution.Expectation(), ExpectedSubmissions: submitted.Expectation(),
		ExpectedPositions: positions.Expectation(), ExpectedSwissPoints: sentinel,
		NextPositionRevisionID: task049ID(11302), Reason: arena.GoldenAttemptTerminalDeadline,
	}
	record, changed, err := arena.NewGoldenAttemptCommitUseCase(
		repository,
		fixedArenaClock{now: execution.Start.Deadline},
	).CommitAttempt(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, domain.ArenaGoldenAttemptStateCompleted, record.Attempt.State)
	require.Equal(t, domain.ArenaWaveStateCompleted, record.Wave.State)
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
		replayed, replayChanged, replayErr := arena.NewGoldenAttemptCommitUseCase(
			repository,
			fixedArenaClock{now: execution.Start.Deadline.Add(time.Minute)},
		).CommitAttempt(t.Context(), command)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, record.PayloadDigest, replayed.PayloadDigest)
		require.Len(t, replayed.Positions.Positions, 1)
		require.Equal(t, beforeReplayCommits, repository.commitCount())
		require.Equal(t, beforeReplay, repository.stateSnapshot())

		reused := command
		reused.CommitID = task049ID(11320)
		beforeReuseCommits := repository.commitCount()
		beforeReuse := repository.stateSnapshot()
		result, reusedChanged, reusedErr := arena.NewGoldenAttemptCommitUseCase(
			repository,
			fixedArenaClock{now: execution.Start.Deadline},
		).CommitAttempt(t.Context(), reused)
		require.Nil(t, result)
		require.False(t, reusedChanged)
		require.ErrorIs(t, reusedErr, arena.ErrGoldenAttemptCommitCommandReuse)
		require.Equal(t, beforeReuseCommits, repository.commitCount())
		require.Equal(t, beforeReuse, repository.stateSnapshot())
		repository.restoreCurrent()
		repository.loadError = nil
	})

	t.Run("terminal-first submission is rejected without a write", func(t *testing.T) {
		verification := task049Verification(scope, execution, execution.Membership.ParticipantIDs[1], 11400)
		submissionRepository.verifications[verification.ID] = verification
		late := arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(11410), ActorParticipantID: verification.ParticipantID,
			ParticipantID: verification.ParticipantID, VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(11411),
		}
		before := submissionRepository.commitCount()
		result, lateChanged, lateErr := arena.NewGoldenSubmissionUseCase(submissionRepository).Submit(t.Context(), late)
		require.Nil(t, result)
		require.False(t, lateChanged)
		require.ErrorIs(t, lateErr, arena.ErrGoldenSubmissionClosed)
		require.Equal(t, before, submissionRepository.commitCount())
	})

	t.Run("terminal rejects retained submissions at the deadline", func(t *testing.T) {
		lateSubmissions := submitted.Snapshot()
		lateSubmissions.Submissions[0].CommittedAt = execution.Start.Deadline
		lateSubmissions.Receipts[0].CommittedAt = execution.Start.Deadline
		task049SealSubmissionLedger(t, &lateSubmissions)
		require.NoError(t, lateSubmissions.Validate())

		localSubmission := newTask049SubmissionRepository(execution, lateSubmissions, startedAt.Add(5*time.Second))
		local := newTask049CommitRepository(localSubmission, lateSubmissions, positions, sentinel)
		localCommand := command
		localCommand.CommandID = task049ID(11412)
		localCommand.CommitID = task049ID(11413)
		localCommand.NextPositionRevisionID = task049ID(11414)
		localCommand.ExpectedSubmissions = lateSubmissions.Expectation()
		result, localChanged, commitErr := arena.NewGoldenAttemptCommitUseCase(
			local,
			fixedArenaClock{now: execution.Start.Deadline},
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, arena.ErrGoldenAttemptCommitAuthorityConflict)
		require.Zero(t, local.commitCount())
	})

	t.Run("submission and terminal settlement share one transaction boundary", func(t *testing.T) {
		t.Run("submit first makes the stale terminal conflict", func(t *testing.T) {
			localSubmission := newTask049SubmissionRepository(execution, *submitted, startedAt.Add(6*time.Second))
			lateParticipant := execution.Membership.ParticipantIDs[1]
			lateVerification := task049Verification(scope, execution, lateParticipant, 11420)
			localSubmission.verifications[lateVerification.ID] = lateVerification
			lateCommand := arena.GoldenSubmissionCommand{
				Scope: scope, CommandID: task049ID(11422), ActorParticipantID: lateParticipant,
				ParticipantID: lateParticipant, VerificationID: lateVerification.ID,
				ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(11423),
			}
			localPositions, buildErr := arena.NewGoldenPositionLedger(
				execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, task049ID(11424),
			)
			require.NoError(t, buildErr)
			local := newTask049CommitRepository(localSubmission, *submitted, localPositions, sentinel)
			localCommand := command
			localCommand.CommandID, localCommand.CommitID = task049ID(11425), task049ID(11426)
			localCommand.NextPositionRevisionID = task049ID(11427)
			localCommand.ExpectedPositions = localPositions.Expectation()
			loaded, signalLoaded := task049Latch(t)
			release, signalRelease := task049Latch(t)
			local.beforeCommit = func() {
				signalLoaded()
				task049AwaitSignal(t, release, "submit-first terminal release")
			}

			type terminalResult struct {
				record  *arena.GoldenAttemptCommitRecord
				changed bool
				err     error
			}
			finished := make(chan terminalResult, 1)
			go func() {
				value, didChange, commitErr := arena.NewGoldenAttemptCommitUseCase(
					local,
					fixedArenaClock{now: execution.Start.Deadline},
				).CommitAttempt(t.Context(), localCommand)
				finished <- terminalResult{record: value, changed: didChange, err: commitErr}
			}()
			require.True(t, task049AwaitSignal(t, loaded, "submit-first terminal commit entry"))
			lateLedger, lateChanged, lateErr := arena.NewGoldenSubmissionUseCase(localSubmission).Submit(
				t.Context(), lateCommand,
			)
			require.NoError(t, lateErr)
			require.True(t, lateChanged)
			require.Len(t, lateLedger.Submissions, 2)
			signalRelease()
			stale, received := task049AwaitValue(t, finished, "submit-first terminal result")
			require.True(t, received)
			require.Nil(t, stale.record)
			require.False(t, stale.changed)
			require.ErrorIs(t, stale.err, arena.ErrGoldenAttemptCommitAuthorityConflict)
			require.Zero(t, local.commitCount())
			local.beforeCommit = nil

			fresh := localCommand
			fresh.CommandID, fresh.CommitID = task049ID(11428), task049ID(11429)
			fresh.NextPositionRevisionID = task049ID(11430)
			fresh.ExpectedSubmissions = lateLedger.Expectation()
			settled, settledChanged, settleErr := arena.NewGoldenAttemptCommitUseCase(
				local,
				fixedArenaClock{now: execution.Start.Deadline},
			).CommitAttempt(t.Context(), fresh)
			require.NoError(t, settleErr)
			require.True(t, settledChanged)
			require.Len(t, settled.Positions.Positions, 2)
			require.Len(t, settled.Ordering.Order, 2)
			require.Equal(t, 1, local.commitCount())
		})

		t.Run("terminal first closes a late submission", func(t *testing.T) {
			localSubmission := newTask049SubmissionRepository(execution, *submitted, startedAt.Add(6*time.Second))
			lateParticipant := execution.Membership.ParticipantIDs[1]
			lateVerification := task049Verification(scope, execution, lateParticipant, 11440)
			localSubmission.verifications[lateVerification.ID] = lateVerification
			lateCommand := arena.GoldenSubmissionCommand{
				Scope: scope, CommandID: task049ID(11442), ActorParticipantID: lateParticipant,
				ParticipantID: lateParticipant, VerificationID: lateVerification.ID,
				ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(11443),
			}
			localPositions, buildErr := arena.NewGoldenPositionLedger(
				execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, task049ID(11444),
			)
			require.NoError(t, buildErr)
			local := newTask049CommitRepository(localSubmission, *submitted, localPositions, sentinel)
			localCommand := command
			localCommand.CommandID, localCommand.CommitID = task049ID(11445), task049ID(11446)
			localCommand.NextPositionRevisionID = task049ID(11447)
			localCommand.ExpectedPositions = localPositions.Expectation()
			locked, signalLocked := task049Latch(t)
			release, signalRelease := task049Latch(t)
			local.insideCommit = func() {
				signalLocked()
				task049AwaitSignal(t, release, "terminal-first transaction release")
			}

			terminalFinished := make(chan error, 1)
			go func() {
				_, _, commitErr := arena.NewGoldenAttemptCommitUseCase(
					local,
					fixedArenaClock{now: execution.Start.Deadline},
				).CommitAttempt(t.Context(), localCommand)
				terminalFinished <- commitErr
			}()
			require.True(t, task049AwaitSignal(t, locked, "terminal-first transaction entry"))
			submitEntered, signalSubmitEntered := task049Latch(t)
			localSubmission.beforeCommit = signalSubmitEntered
			type submitResult struct {
				ledger  *arena.GoldenSubmissionLedger
				changed bool
				err     error
			}
			submitFinished := make(chan submitResult, 1)
			go func() {
				value, didChange, submitErr := arena.NewGoldenSubmissionUseCase(localSubmission).Submit(
					t.Context(), lateCommand,
				)
				submitFinished <- submitResult{ledger: value, changed: didChange, err: submitErr}
			}()
			require.True(t, task049AwaitSignal(t, submitEntered, "terminal-first submission entry"))
			signalRelease()
			terminalErr, terminalReceived := task049AwaitValue(t, terminalFinished, "terminal-first terminal result")
			require.True(t, terminalReceived)
			require.NoError(t, terminalErr)
			late, submitReceived := task049AwaitValue(t, submitFinished, "terminal-first submission result")
			require.True(t, submitReceived)
			require.Nil(t, late.ledger)
			require.False(t, late.changed)
			require.ErrorIs(t, late.err, arena.ErrGoldenSubmissionClosed)
			require.Equal(t, 1, local.commitCount())
			require.Len(t, localSubmission.snapshot().Submissions, 1)
		})
	})

	t.Run("nonterminal and stale heads write nothing", func(t *testing.T) {
		localSubmission := newTask049SubmissionRepository(execution, *submitted, startedAt.Add(5*time.Second))
		localPositions, buildErr := arena.NewGoldenPositionLedger(
			execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, task049ID(11500),
		)
		require.NoError(t, buildErr)
		local := newTask049CommitRepository(localSubmission, *submitted, localPositions, sentinel)
		localCommand := command
		localCommand.CommandID, localCommand.CommitID = task049ID(11510), task049ID(11511)
		localCommand.NextPositionRevisionID = task049ID(11512)
		localCommand.ExpectedPositions = localPositions.Expectation()

		result, localChanged, commitErr := arena.NewGoldenAttemptCommitUseCase(
			local,
			fixedArenaClock{now: execution.Start.Deadline.Add(-time.Nanosecond)},
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, arena.ErrGoldenAttemptNotTerminal)
		require.Zero(t, local.commitCount())

		localCommand.ExpectedSubmissions.Revision++
		result, localChanged, commitErr = arena.NewGoldenAttemptCommitUseCase(
			local,
			fixedArenaClock{now: execution.Start.Deadline},
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, arena.ErrGoldenAttemptCommitAuthorityConflict)
		require.Zero(t, local.commitCount())
	})

	t.Run("repository error cannot leave terminal or position state", func(t *testing.T) {
		localSubmission := newTask049SubmissionRepository(execution, *submitted, startedAt.Add(5*time.Second))
		localPositions, buildErr := arena.NewGoldenPositionLedger(
			execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, task049ID(11600),
		)
		require.NoError(t, buildErr)
		local := newTask049CommitRepository(localSubmission, *submitted, localPositions, sentinel)
		local.commitError = errors.New("atomic settlement failed")
		localCommand := command
		localCommand.CommandID, localCommand.CommitID = task049ID(11610), task049ID(11611)
		localCommand.NextPositionRevisionID = task049ID(11612)
		localCommand.ExpectedPositions = localPositions.Expectation()
		result, localChanged, commitErr := arena.NewGoldenAttemptCommitUseCase(
			local,
			fixedArenaClock{now: execution.Start.Deadline},
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorContains(t, commitErr, "atomic settlement failed")
		require.Zero(t, local.commitCount())
		require.Empty(t, local.positions.Positions)
		require.Nil(t, local.current)
	})

	t.Run("valid foreign unchanged result is rejected", func(t *testing.T) {
		localSubmission := newTask049SubmissionRepository(execution, *submitted, startedAt.Add(5*time.Second))
		localPositions, buildErr := arena.NewGoldenPositionLedger(
			execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, task049ID(11620),
		)
		require.NoError(t, buildErr)
		local := newTask049CommitRepository(localSubmission, *submitted, localPositions, sentinel)
		foreign := record.Snapshot()
		local.returnRecord = &foreign
		local.returnChanged = false
		localCommand := command
		localCommand.CommandID, localCommand.CommitID = task049ID(11621), task049ID(11622)
		localCommand.NextPositionRevisionID = task049ID(11623)
		localCommand.ExpectedPositions = localPositions.Expectation()

		result, localChanged, commitErr := arena.NewGoldenAttemptCommitUseCase(
			local,
			fixedArenaClock{now: execution.Start.Deadline},
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, domain.ErrInternal)

		changedLocal := newTask049CommitRepository(
			newTask049SubmissionRepository(execution, *submitted, startedAt.Add(5*time.Second)),
			*submitted,
			positions,
			sentinel,
		)
		changedTimestamp := record.Snapshot()
		replacedAt := changedTimestamp.FinishedAt.Add(time.Nanosecond)
		changedTimestamp.FinishedAt = replacedAt
		changedTimestamp.Attempt.FinishedAt = &replacedAt
		changedTimestamp.Group.Attempts[len(changedTimestamp.Group.Attempts)-1].FinishedAt = &replacedAt
		task049SealAttemptRecord(t, &changedTimestamp)
		require.NoError(t, changedTimestamp.Validate())
		changedLocal.returnRecord = &changedTimestamp
		changedLocal.returnChanged = true
		result, localChanged, commitErr = arena.NewGoldenAttemptCommitUseCase(
			changedLocal,
			fixedArenaClock{now: execution.Start.Deadline},
		).CommitAttempt(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, domain.ErrInternal)
	})

	t.Run("same-command unchanged result cannot change a command head", func(t *testing.T) {
		localSubmission := newTask049SubmissionRepository(execution, *submitted, startedAt.Add(5*time.Second))
		local := newTask049CommitRepository(localSubmission, *submitted, positions, sentinel)
		tampered := record.Snapshot()
		tampered.SwissPoints = task049SwissPointSentinel(11624)
		task049SealAttemptRecord(t, &tampered)
		require.NoError(t, tampered.Validate())
		local.returnRecord = &tampered
		local.returnChanged = false

		result, localChanged, commitErr := arena.NewGoldenAttemptCommitUseCase(
			local,
			fixedArenaClock{now: execution.Start.Deadline},
		).CommitAttempt(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, domain.ErrInternal)
	})

	t.Run("commit identity cannot alias active attempt", func(t *testing.T) {
		localSubmission := newTask049SubmissionRepository(execution, *submitted, startedAt.Add(5*time.Second))
		localPositions, buildErr := arena.NewGoldenPositionLedger(
			execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, task049ID(11630),
		)
		require.NoError(t, buildErr)
		local := newTask049CommitRepository(localSubmission, *submitted, localPositions, sentinel)
		localCommand := command
		localCommand.CommandID = task049ID(11631)
		localCommand.CommitID = scope.AttemptID
		localCommand.NextPositionRevisionID = task049ID(11632)
		localCommand.ExpectedPositions = localPositions.Expectation()

		result, localChanged, commitErr := arena.NewGoldenAttemptCommitUseCase(
			local,
			fixedArenaClock{now: execution.Start.Deadline},
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, arena.ErrInvalidGoldenAttemptCommit)
		require.Zero(t, local.commitCount())
	})

	t.Run("commit identity cannot alias historical submission authority", func(t *testing.T) {
		historicalPositions := record.Positions.Snapshot()
		historicalWaveID := task049ID(11633)
		historicalPositions.Attempts[0].SubmissionHead.Scope.WaveID = historicalWaveID
		task049SealOrdering(t, &historicalPositions.Attempts[0])
		task049SealPositionLedger(t, &historicalPositions)
		require.NoError(t, historicalPositions.Validate())

		localSubmission := newTask049SubmissionRepository(execution, *submitted, startedAt.Add(5*time.Second))
		local := newTask049CommitRepository(localSubmission, *submitted, historicalPositions, sentinel)
		localCommand := command
		localCommand.CommandID = task049ID(11634)
		localCommand.CommitID = historicalWaveID
		localCommand.NextPositionRevisionID = task049ID(11635)
		localCommand.ExpectedPositions = historicalPositions.Expectation()
		result, localChanged, commitErr := arena.NewGoldenAttemptCommitUseCase(
			local,
			fixedArenaClock{now: execution.Start.Deadline},
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, arena.ErrInvalidGoldenAttemptCommit)
		require.Zero(t, local.commitCount())
	})

	t.Run("maximum position boundary is rejected without iteration", func(t *testing.T) {
		result, buildErr := arena.NewGoldenPositionLedger(
			execution.Scope,
			math.MaxInt,
			math.MaxInt,
			task049ID(11640),
		)
		require.ErrorIs(t, buildErr, arena.ErrInvalidGoldenAttemptCommit)
		require.Equal(t, arena.GoldenPositionLedger{}, result)
	})

	t.Run("position history retains gaps and every revision identity", func(t *testing.T) {
		withGap := record.Positions.Snapshot()
		withGap.Attempts[0].AttemptNo = 2
		withGap.Positions[0].AttemptNo = 2
		task049SealOrdering(t, &withGap.Attempts[0])
		task049SealPositionLedger(t, &withGap)
		require.NoError(t, withGap.Validate(), "cancelled pre-start attempts may precede the first settlement")

		advanced := record.Positions.Snapshot()
		secondAttemptID := task049ID(11641)
		emptyHead := submitted.Expectation()
		emptyHead.Scope.AttemptID = secondAttemptID
		emptyHead.RevisionID = task049ID(11642)
		emptyHead.Revision = 1
		emptyHead.NextSubmissionID = 1
		emptyHead.PayloadDigest = sha256.Sum256([]byte("empty second-attempt submission head"))
		emptyOrdering := arena.GoldenAttemptOrderingEvidence{
			AttemptID: secondAttemptID, AttemptNo: 2, SubmissionHead: emptyHead,
		}
		task049SealOrdering(t, &emptyOrdering)
		previousRevisionID := advanced.RevisionID
		advanced.PreviousRevisionID = &previousRevisionID
		advanced.RevisionID = task049ID(11643)
		advanced.Revision++
		advanced.RevisionIDs = append(advanced.RevisionIDs, advanced.RevisionID)
		advanced.Attempts = append(advanced.Attempts, emptyOrdering)
		task049SealPositionLedger(t, &advanced)
		require.NoError(t, advanced.Validate())

		emptySubmissions, submissionErr := arena.NewGoldenSubmissionLedger(scope, task049ID(11644))
		require.NoError(t, submissionErr)
		localSubmission := newTask049SubmissionRepository(execution, emptySubmissions, startedAt.Add(5*time.Second))
		local := newTask049CommitRepository(localSubmission, emptySubmissions, advanced, sentinel)
		localCommand := command
		localCommand.CommandID, localCommand.CommitID = task049ID(11645), task049ID(11646)
		localCommand.ExpectedSubmissions = emptySubmissions.Expectation()
		localCommand.ExpectedPositions = advanced.Expectation()
		localCommand.NextPositionRevisionID = advanced.RevisionIDs[0]
		result, localChanged, commitErr := arena.NewGoldenAttemptCommitUseCase(
			local,
			fixedArenaClock{now: execution.Start.Deadline},
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, arena.ErrInvalidGoldenAttemptCommit)
		require.Zero(t, local.commitCount())

		second := record.Snapshot()
		second.ID, second.CommandID = task049ID(11647), task049ID(11648)
		second.Scope.AttemptID = secondAttemptID
		second.ActiveExecution.AttemptID = secondAttemptID
		second.Assignment.AttemptID = secondAttemptID
		task049SealAttemptAssignmentEvidence(t, &second.Assignment)
		second.ExpectedSubmissions = emptyHead
		second.ExpectedPositions = record.Positions.Expectation()
		second.PriorPositions = record.Positions.Snapshot()
		second.Positions = advanced.Snapshot()
		second.Ordering = emptyOrdering.Snapshot()
		second.Attempt = record.Attempt
		second.Attempt.ID = secondAttemptID
		second.Attempt.AttemptNo = 2
		previousAttemptID := record.Attempt.ID
		second.Attempt.PreviousAttemptID = &previousAttemptID
		secondStartedAt := record.FinishedAt.Add(time.Second)
		secondFinishedAt := secondStartedAt.Add(time.Second)
		second.Attempt.StartedAt = &secondStartedAt
		second.Attempt.FinishedAt = &secondFinishedAt
		second.FinishedAt = secondFinishedAt
		second.Group = record.Group
		second.Group.Attempts = append(second.Group.Attempts, second.Attempt)
		task049SealAttemptRecord(t, &second)
		require.NoError(t, second.Validate())

		forged := second.Snapshot()
		forgedDigest := sha256.Sum256([]byte("forged prior ordering prefix"))
		forged.Positions.Positions[0].EvidenceDigest = forgedDigest
		forged.Positions.Attempts[0].Order[0].EvidenceDigest = forgedDigest
		task049SealOrdering(t, &forged.Positions.Attempts[0])
		task049SealPositionLedger(t, &forged.Positions)
		task049SealAttemptRecord(t, &forged)
		require.ErrorIs(t, forged.Validate(), arena.ErrInvalidGoldenAttemptCommit)
	})

	t.Run("malformed terminal replay cannot satisfy a valid digest", func(t *testing.T) {
		clean := record.Snapshot()
		task049SealAttemptRecord(t, &clean)
		require.NoError(t, clean.Validate())
		cleanOrdering := record.Ordering.Snapshot()
		task049SealOrdering(t, &cleanOrdering)
		require.NoError(t, cleanOrdering.Validate())
		assertRejectedWithoutWrite := func(t *testing.T, malformed arena.GoldenAttemptCommitRecord) {
			t.Helper()
			localSubmission := newTask049SubmissionRepository(execution, *submitted, startedAt.Add(5*time.Second))
			local := newTask049CommitRepository(localSubmission, *submitted, positions, sentinel)
			local.returnRecord = &malformed
			local.returnChanged = false
			beforeCommits := local.commitCount()
			before := local.stateSnapshot()
			var result *arena.GoldenAttemptCommitRecord
			var changed bool
			var commitErr error
			require.NotPanics(t, func() {
				result, changed, commitErr = arena.NewGoldenAttemptCommitUseCase(
					local,
					fixedArenaClock{now: execution.Start.Deadline},
				).CommitAttempt(t.Context(), command)
			})
			require.Nil(t, result)
			require.False(t, changed)
			require.ErrorIs(t, commitErr, domain.ErrInternal)
			require.Equal(t, beforeCommits, local.commitCount())
			require.Equal(t, before, local.stateSnapshot())
		}

		t.Run("position interval cannot exceed the group", func(t *testing.T) {
			tampered := record.Snapshot()
			tampered.PriorPositions.PositionTo++
			task049SealPositionLedger(t, &tampered.PriorPositions)
			tampered.ExpectedPositions = tampered.PriorPositions.Expectation()
			tampered.Positions.PositionTo++
			task049SealPositionLedger(t, &tampered.Positions)
			task049SealAttemptRecord(t, &tampered)
			require.NoError(t, tampered.PriorPositions.Validate())
			require.NoError(t, tampered.Positions.Validate())
			require.ErrorIs(t, tampered.Validate(), arena.ErrInvalidGoldenAttemptCommit)
			assertRejectedWithoutWrite(t, tampered)
		})

		t.Run("position evidence cannot name a foreign participant", func(t *testing.T) {
			tampered := record.Snapshot()
			foreignParticipantID := task049ID(11649)
			tampered.Ordering.Order[0].ParticipantID = foreignParticipantID
			task049SealOrdering(t, &tampered.Ordering)
			tampered.Positions.Positions[0].ParticipantID = foreignParticipantID
			tampered.Positions.Attempts[len(tampered.Positions.Attempts)-1] = tampered.Ordering.Snapshot()
			task049SealPositionLedger(t, &tampered.Positions)
			task049SealAttemptRecord(t, &tampered)
			require.NoError(t, tampered.Positions.Validate())
			require.ErrorIs(t, tampered.Validate(), arena.ErrInvalidGoldenAttemptCommit)
			assertRejectedWithoutWrite(t, tampered)
		})

		t.Run("over-capacity position evidence is panic-free", func(t *testing.T) {
			tampered := record.Snapshot()
			tampered.PriorPositions.PositionTo++
			task049SealPositionLedger(t, &tampered.PriorPositions)
			tampered.ExpectedPositions = tampered.PriorPositions.Expectation()
			tampered.Positions.PositionTo++
			foreignParticipantID := task049ID(11651)
			participants := []uuid.UUID{
				tampered.Group.Members[1].ParticipantID,
				tampered.Group.Members[2].ParticipantID,
				foreignParticipantID,
			}
			for index, participantID := range participants {
				submissionID := uint64(index + 2)
				evidenceDigest := sha256.Sum256([]byte{byte(index + 10)})
				tampered.Ordering.Order = append(tampered.Ordering.Order, arena.GoldenPositionOrderEntry{
					SubmissionID: submissionID, ParticipantID: participantID,
					CommittedAt:    tampered.Ordering.Order[0].CommittedAt.Add(time.Duration(index+1) * time.Nanosecond),
					EvidenceDigest: evidenceDigest,
				})
				tampered.Positions.Positions = append(tampered.Positions.Positions, arena.GoldenCommittedPosition{
					Position: tampered.Positions.PositionFrom + index + 1, ParticipantID: participantID,
					AttemptID: tampered.Attempt.ID, AttemptNo: tampered.Attempt.AttemptNo,
					SubmissionID: submissionID, EvidenceDigest: evidenceDigest, CommitID: tampered.ID,
				})
			}
			tampered.Ordering.SubmissionHead.NextSubmissionID = 5
			tampered.ExpectedSubmissions = tampered.Ordering.SubmissionHead
			task049SealOrdering(t, &tampered.Ordering)
			tampered.Positions.Attempts[len(tampered.Positions.Attempts)-1] = tampered.Ordering.Snapshot()
			task049SealPositionLedger(t, &tampered.Positions)
			task049SealAttemptRecord(t, &tampered)
			require.NoError(t, tampered.Positions.Validate())
			var validationErr error
			require.NotPanics(t, func() { validationErr = tampered.Validate() })
			require.ErrorIs(t, validationErr, arena.ErrInvalidGoldenAttemptCommit)
			assertRejectedWithoutWrite(t, tampered)
		})

		t.Run("group terminal attempt differs from record", func(t *testing.T) {
			tampered := record.Snapshot()
			last := len(tampered.Group.Attempts) - 1
			changedAt := tampered.Group.Attempts[last].FinishedAt.Add(time.Nanosecond)
			tampered.Group.Attempts[last].FinishedAt = &changedAt
			task049SealAttemptRecord(t, &tampered)
			require.ErrorIs(t, tampered.Validate(), arena.ErrInvalidGoldenAttemptCommit)
		})

		t.Run("active execution aliases another attempt", func(t *testing.T) {
			tampered := record.Snapshot()
			tampered.ActiveExecution.AttemptID = task049ID(11650)
			task049SealAttemptRecord(t, &tampered)
			require.ErrorIs(t, tampered.Validate(), arena.ErrInvalidGoldenAttemptCommit)
		})

		t.Run("private assignment aliases retained assignment authority", func(t *testing.T) {
			tampered := record.Snapshot()
			tampered.Assignment.Private[0].ID = tampered.Assignment.EdgeID
			task049SealAttemptAssignmentEvidence(t, &tampered.Assignment)
			task049SealAttemptRecord(t, &tampered)
			require.ErrorIs(t, tampered.Validate(), arena.ErrInvalidGoldenAttemptCommit)
		})

		t.Run("reserve assignment evidence is always the initial revision", func(t *testing.T) {
			tampered := record.Snapshot()
			tampered.Assignment.Revision = 2
			task049SealAttemptAssignmentEvidence(t, &tampered.Assignment)
			task049SealAttemptRecord(t, &tampered)
			require.ErrorIs(t, tampered.Validate(), arena.ErrInvalidGoldenAttemptCommit)
		})

		t.Run("ordering submission head is incomplete", func(t *testing.T) {
			tests := []struct {
				name   string
				mutate func(*arena.GoldenSubmissionLedgerExpectation)
			}{
				{name: "revision identity", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) {
					head.RevisionID = uuid.Nil
				}},
				{name: "revision", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) {
					head.Revision = 0
				}},
				{name: "next submission identity", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) {
					head.NextSubmissionID = 0
				}},
				{name: "missing ordered submission", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) {
					head.NextSubmissionID++
				}},
			}
			for _, testCase := range tests {
				t.Run(testCase.name, func(t *testing.T) {
					tampered := record.Ordering.Snapshot()
					testCase.mutate(&tampered.SubmissionHead)
					task049SealOrdering(t, &tampered)
					require.ErrorIs(t, tampered.Validate(), arena.ErrInvalidGoldenAttemptCommit)
				})
			}
		})
	})

	t.Run("returned and stored prior evidence are detached", func(t *testing.T) {
		before := repository.archivedSnapshot()
		record.Positions.Positions[0].ParticipantID = task049ID(11700)
		record.Ordering.Order[0].ParticipantID = task049ID(11701)
		after := repository.archivedSnapshot()
		require.Equal(t, before, after)
		tampered := after.Snapshot()
		tampered.Positions.Positions[0].ParticipantID = task049ID(11702)
		require.Error(t, tampered.Validate())
		require.NotContains(t, task049Encode(t, after), execution.Assignment.Snapshot.Flag)
	})
}

type task049CommitRepository struct {
	mu             sync.Mutex
	submissionRepo *task049SubmissionRepository
	execution      arena.GoldenWaveExecution
	submissions    arena.GoldenSubmissionLedger
	positions      arena.GoldenPositionLedger
	swissPoints    arena.GoldenSwissPointLedgerSentinel
	current        *arena.GoldenAttemptCommitRecord
	archived       *arena.GoldenAttemptCommitRecord
	replays        map[uuid.UUID]arena.GoldenAttemptCommitRecord
	loadError      error
	commitError    error
	commits        int
	returnRecord   *arena.GoldenAttemptCommitRecord
	returnChanged  bool
	beforeCommit   func()
	insideCommit   func()
}

type task049CommitRepositoryState struct {
	submission      task049SubmissionRepositoryState
	execution       arena.GoldenWaveExecution
	submissions     arena.GoldenSubmissionLedger
	positions       arena.GoldenPositionLedger
	swissPoints     arena.GoldenSwissPointLedgerSentinel
	current         *arena.GoldenAttemptCommitRecord
	archived        *arena.GoldenAttemptCommitRecord
	replays         map[uuid.UUID]arena.GoldenAttemptCommitRecord
	loadError       error
	commitError     error
	commits         int
	returnRecord    *arena.GoldenAttemptCommitRecord
	returnChanged   bool
	hasBeforeCommit bool
	hasInsideCommit bool
}

func newTask049CommitRepository(
	submissionRepo *task049SubmissionRepository,
	submissions arena.GoldenSubmissionLedger,
	positions arena.GoldenPositionLedger,
	sentinel arena.GoldenSwissPointLedgerSentinel,
) *task049CommitRepository {
	return &task049CommitRepository{
		submissionRepo: submissionRepo, execution: submissionRepo.execution.Snapshot(),
		submissions: submissions.Snapshot(), positions: positions.Snapshot(), swissPoints: sentinel,
		replays: make(map[uuid.UUID]arena.GoldenAttemptCommitRecord),
	}
}

func (r *task049CommitRepository) FindGoldenAttemptCommit(
	_ context.Context,
	_ uuid.UUID,
	commandID uuid.UUID,
) (*arena.GoldenAttemptCommitRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if replay, found := r.replays[commandID]; found {
		clone := replay.Snapshot()
		return &clone, nil
	}
	return nil, nil
}

func (r *task049CommitRepository) LoadGoldenAttemptCommitAuthority(
	_ context.Context,
	scope arena.GoldenSubmissionScope,
) (arena.GoldenAttemptCommitAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loadError != nil {
		return arena.GoldenAttemptCommitAuthority{}, r.loadError
	}
	r.submissionRepo.mu.Lock()
	liveSubmissions := r.submissionRepo.ledger.Snapshot()
	r.submissionRepo.mu.Unlock()
	return arena.GoldenAttemptCommitAuthority{
		Scope: scope, Execution: r.execution.Snapshot(), Submissions: liveSubmissions,
		Positions: r.positions.Snapshot(), SwissPoints: r.swissPoints,
	}, nil
}

func (r *task049CommitRepository) CommitGoldenAttempt(
	_ context.Context,
	record arena.GoldenAttemptCommitRecord,
) (*arena.GoldenAttemptCommitRecord, bool, error) {
	if r.beforeCommit != nil {
		r.beforeCommit()
	}
	r.submissionRepo.transactionMu.Lock()
	defer r.submissionRepo.transactionMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.insideCommit != nil {
		r.insideCommit()
	}
	if r.commitError != nil {
		return nil, false, r.commitError
	}
	if r.returnRecord != nil {
		result := r.returnRecord.Snapshot()
		return &result, r.returnChanged, nil
	}
	r.submissionRepo.mu.Lock()
	defer r.submissionRepo.mu.Unlock()
	liveSubmissions := r.submissionRepo.ledger.Snapshot()
	if r.current != nil || !r.execution.Expectation().Equal(record.ActiveExecution) ||
		r.submissionRepo.terminalID != uuid.Nil ||
		!r.submissionRepo.execution.Expectation().Equal(record.ActiveExecution) ||
		!liveSubmissions.Expectation().Equal(record.ExpectedSubmissions) ||
		!r.positions.Expectation().Equal(record.ExpectedPositions) || r.swissPoints != record.SwissPoints {
		return nil, false, domain.ErrConflict
	}
	clone := record.Snapshot()
	r.current = &clone
	r.submissions = liveSubmissions
	r.positions = record.Positions.Snapshot()
	r.replays[record.CommandID] = clone
	r.commits++
	r.submissionRepo.terminalID = record.ID
	r.submissionRepo.terminalDigest = record.PayloadDigest
	result := clone.Snapshot()
	return &result, true, nil
}

func (r *task049CommitRepository) archiveCurrent() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		clone := r.current.Snapshot()
		r.archived = &clone
		r.current = nil
	}
}

func (r *task049CommitRepository) restoreCurrent() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.archived != nil {
		clone := r.archived.Snapshot()
		r.current = &clone
	}
}

func (r *task049CommitRepository) archivedSnapshot() arena.GoldenAttemptCommitRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.archived != nil {
		return r.archived.Snapshot()
	}
	return r.current.Snapshot()
}

func (r *task049CommitRepository) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *task049CommitRepository) stateSnapshot() task049CommitRepositoryState {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := task049CommitRepositoryState{
		submission: r.submissionRepo.stateSnapshot(),
		execution:  r.execution.Snapshot(), submissions: r.submissions.Snapshot(),
		positions: r.positions.Snapshot(), swissPoints: r.swissPoints,
		replays:   make(map[uuid.UUID]arena.GoldenAttemptCommitRecord, len(r.replays)),
		loadError: r.loadError, commitError: r.commitError, commits: r.commits,
		returnChanged:   r.returnChanged,
		hasBeforeCommit: r.beforeCommit != nil, hasInsideCommit: r.insideCommit != nil,
	}
	if r.current != nil {
		clone := r.current.Snapshot()
		state.current = &clone
	}
	if r.archived != nil {
		clone := r.archived.Snapshot()
		state.archived = &clone
	}
	for key, value := range r.replays {
		state.replays[key] = value.Snapshot()
	}
	if r.returnRecord != nil {
		clone := r.returnRecord.Snapshot()
		state.returnRecord = &clone
	}
	return state
}

func task049SwissPointSentinel(base int) arena.GoldenSwissPointLedgerSentinel {
	return arena.GoldenSwissPointLedgerSentinel{
		RevisionID: task049ID(base), Revision: 7,
		Digest: sha256.Sum256([]byte("swiss-points-unchanged")),
	}
}

func task049SealOrdering(t *testing.T, evidence *arena.GoldenAttemptOrderingEvidence) {
	t.Helper()
	evidence.PayloadDigest = task049GobDigest(t, struct {
		AttemptID      uuid.UUID
		AttemptNo      int
		SubmissionHead arena.GoldenSubmissionLedgerExpectation
		Order          []arena.GoldenPositionOrderEntry
	}{
		AttemptID: evidence.AttemptID, AttemptNo: evidence.AttemptNo,
		SubmissionHead: evidence.SubmissionHead, Order: evidence.Order,
	})
}

func task049SealAttemptRecord(t *testing.T, record *arena.GoldenAttemptCommitRecord) {
	t.Helper()
	record.PayloadDigest = task049GobDigest(t, struct {
		ID                  uuid.UUID
		CommandID           uuid.UUID
		CommandDigest       [sha256.Size]byte
		Scope               arena.GoldenSubmissionScope
		ActiveExecution     arena.GoldenWaveExecutionExpectation
		Assignment          arena.GoldenAttemptAssignmentEvidence
		ExpectedSubmissions arena.GoldenSubmissionLedgerExpectation
		ExpectedPositions   arena.GoldenPositionLedgerExpectation
		SwissPoints         arena.GoldenSwissPointLedgerSentinel
		Reason              arena.GoldenAttemptTerminalReason
		FinishedAt          time.Time
		Attempt             domain.ArenaGoldenAttempt
		Group               domain.ArenaGoldenGroupState
		Wave                domain.ArenaWave
		Ordering            arena.GoldenAttemptOrderingEvidence
		PriorPositions      arena.GoldenPositionLedger
		Positions           arena.GoldenPositionLedger
	}{
		ID: record.ID, CommandID: record.CommandID, CommandDigest: record.CommandDigest,
		Scope: record.Scope, ActiveExecution: record.ActiveExecution, Assignment: record.Assignment,
		ExpectedSubmissions: record.ExpectedSubmissions, ExpectedPositions: record.ExpectedPositions,
		SwissPoints: record.SwissPoints, Reason: record.Reason, FinishedAt: record.FinishedAt,
		Attempt: record.Attempt, Group: record.Group, Wave: record.Wave,
		Ordering: record.Ordering, PriorPositions: record.PriorPositions, Positions: record.Positions,
	})
}

func task049SealAttemptAssignmentEvidence(t *testing.T, evidence *arena.GoldenAttemptAssignmentEvidence) {
	t.Helper()
	evidence.PayloadDigest = task049GobDigest(t, struct {
		ID                     uuid.UUID
		RevisionID             uuid.UUID
		Revision               int64
		Scope                  arena.GoldenStateScope
		AttemptID              uuid.UUID
		WaveID                 uuid.UUID
		MembershipID           uuid.UUID
		Plan                   arena.GoldenPlanStateBinding
		EdgeID                 uuid.UUID
		ReservationID          uuid.UUID
		SnapshotID             uuid.UUID
		TaskID                 uuid.UUID
		ContentDigest          [sha256.Size]byte
		Private                []arena.GoldenPrivateAssignment
		ExecutionPayloadDigest [sha256.Size]byte
	}{
		ID: evidence.ID, RevisionID: evidence.RevisionID, Revision: evidence.Revision,
		Scope: evidence.Scope, AttemptID: evidence.AttemptID, WaveID: evidence.WaveID,
		MembershipID: evidence.MembershipID, Plan: evidence.Plan, EdgeID: evidence.EdgeID,
		ReservationID: evidence.ReservationID, SnapshotID: evidence.SnapshotID,
		TaskID: evidence.TaskID, ContentDigest: evidence.ContentDigest, Private: evidence.Private,
		ExecutionPayloadDigest: evidence.ExecutionPayloadDigest,
	})
}

func task049SealPositionLedger(t *testing.T, ledger *arena.GoldenPositionLedger) {
	t.Helper()
	ledger.PayloadDigest = task049GobDigest(t, struct {
		Scope              arena.GoldenStateScope
		RevisionID         uuid.UUID
		Revision           int64
		PreviousRevisionID *uuid.UUID
		RevisionIDs        []uuid.UUID
		PositionFrom       int
		PositionTo         int
		Positions          []arena.GoldenCommittedPosition
		Attempts           []arena.GoldenAttemptOrderingEvidence
	}{
		Scope: ledger.Scope, RevisionID: ledger.RevisionID, Revision: ledger.Revision,
		PreviousRevisionID: ledger.PreviousRevisionID, RevisionIDs: ledger.RevisionIDs,
		PositionFrom: ledger.PositionFrom, PositionTo: ledger.PositionTo,
		Positions: ledger.Positions, Attempts: ledger.Attempts,
	})
}

func task049GobDigest(t *testing.T, value any) [sha256.Size]byte {
	t.Helper()
	var buffer bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buffer).Encode(value))
	return sha256.Sum256(buffer.Bytes())
}

var _ arena.GoldenAttemptCommitRepository = (*task049CommitRepository)(nil)
