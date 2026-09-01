package arena_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenProvisionalOrder(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC)
	execution := task049StartedExecution(t, startedAt)
	participants := execution.Membership.ParticipantIDs
	require.GreaterOrEqual(t, len(participants), 3)
	scope := task049SubmissionScope(execution)
	ledger, err := arena.NewGoldenSubmissionLedger(scope, task049ID(10001))
	require.NoError(t, err)
	repository := newTask049SubmissionRepository(execution, ledger, startedAt.Add(10*time.Second))

	commands := make([]arena.GoldenSubmissionCommand, 3)
	for index, participantID := range participants[:3] {
		verification := task049Verification(scope, execution, participantID, 10100+index*10)
		repository.verifications[verification.ID] = verification
		commands[index] = arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(10200 + index),
			ActorParticipantID: participantID, ParticipantID: participantID,
			VerificationID: verification.ID, ExpectedExecution: execution.Expectation(),
			NextLedgerRevisionID: task049ID(10300 + index),
		}
	}

	type result struct {
		ledger  *arena.GoldenSubmissionLedger
		changed bool
		err     error
	}
	results := make([]result, len(commands))
	workerContext, cancelWorkers := context.WithCancel(t.Context())
	t.Cleanup(cancelWorkers)
	completed := make(chan struct{}, len(commands))
	for index := range commands {
		go func(index int) {
			defer func() { completed <- struct{}{} }()
			results[index].ledger, results[index].changed, results[index].err =
				arena.NewGoldenSubmissionUseCase(repository).Submit(workerContext, commands[index])
		}(index)
	}
	require.True(t, task049AwaitCompletions(t, completed, len(commands), "concurrent submissions"))
	cancelWorkers()
	for _, result := range results {
		require.NoError(t, result.err)
		require.True(t, result.changed)
		require.NotNil(t, result.ledger)
	}

	stored := repository.snapshot()
	require.NoError(t, stored.Validate())
	require.Len(t, stored.Submissions, 3)
	require.Len(t, stored.Receipts, 3)
	for index, submission := range stored.Submissions {
		require.Equal(t, uint64(index+1), submission.ID)
		require.Equal(t, repository.committedAt, submission.CommittedAt)
		require.NotZero(t, submission.EvidenceDigest)
		if index > 0 {
			require.Less(t, stored.Submissions[index-1].ID, submission.ID)
		}
	}
	require.Equal(t, []uint64{1, 2, 3}, task049SubmissionIDs(stored.ProvisionalOrder()))

	t.Run("server timestamps determine order before monotonic identity", func(t *testing.T) {
		localLedger, buildErr := arena.NewGoldenSubmissionLedger(scope, task049ID(10320))
		require.NoError(t, buildErr)
		local := newTask049SubmissionRepository(execution, localLedger, startedAt.Add(9*time.Second))
		first := commands[0]
		first.CommandID, first.NextLedgerRevisionID = task049ID(10321), task049ID(10322)
		second := commands[1]
		second.CommandID, second.NextLedgerRevisionID = task049ID(10323), task049ID(10324)
		local.verifications[first.VerificationID] = repository.verifications[first.VerificationID]
		local.verifications[second.VerificationID] = repository.verifications[second.VerificationID]
		local.committedAtByCommand[first.CommandID] = startedAt.Add(9 * time.Second)
		local.committedAtByCommand[second.CommandID] = startedAt.Add(8 * time.Second)

		_, firstChanged, firstErr := arena.NewGoldenSubmissionUseCase(local).Submit(t.Context(), first)
		require.NoError(t, firstErr)
		require.True(t, firstChanged)
		ordered, secondChanged, secondErr := arena.NewGoldenSubmissionUseCase(local).Submit(t.Context(), second)
		require.NoError(t, secondErr)
		require.True(t, secondChanged)
		require.Equal(t, []uint64{2, 1}, task049SubmissionIDs(ordered.ProvisionalOrder()))
		require.Equal(t, []uint64{1, 2}, task049SubmissionIDs(ordered.Submissions))

		positions, positionErr := arena.NewGoldenPositionLedger(
			execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, task049ID(10325),
		)
		require.NoError(t, positionErr)
		sentinel := task049SwissPointSentinel(10326)
		terminalRepository := newTask049CommitRepository(local, *ordered, positions, sentinel)
		terminal, terminalChanged, terminalErr := arena.NewGoldenAttemptCommitUseCase(
			terminalRepository,
			fixedArenaClock{now: execution.Start.Deadline},
		).CommitAttempt(t.Context(), arena.GoldenAttemptCommitCommand{
			Scope: scope, CommandID: task049ID(10327), CommitID: task049ID(10328),
			ExpectedExecution: execution.Expectation(), ExpectedSubmissions: ordered.Expectation(),
			ExpectedPositions: positions.Expectation(), ExpectedSwissPoints: sentinel,
			NextPositionRevisionID: task049ID(10329), Reason: arena.GoldenAttemptTerminalDeadline,
		})
		require.NoError(t, terminalErr)
		require.True(t, terminalChanged)
		require.Equal(t, []uint64{2, 1}, task049PositionOrderIDs(terminal.Ordering.Order))
	})

	t.Run("concurrent correct commands for one participant retain only the first", func(t *testing.T) {
		localLedger, buildErr := arena.NewGoldenSubmissionLedger(scope, task049ID(10330))
		require.NoError(t, buildErr)
		local := newTask049SubmissionRepository(execution, localLedger, startedAt.Add(9*time.Second))
		participantID := participants[0]
		localCommands := make([]arena.GoldenSubmissionCommand, 2)
		for index := range localCommands {
			verification := task049Verification(scope, execution, participantID, 10340+index*10)
			local.verifications[verification.ID] = verification
			localCommands[index] = arena.GoldenSubmissionCommand{
				Scope: scope, CommandID: task049ID(10342 + index*10), ActorParticipantID: participantID,
				ParticipantID: participantID, VerificationID: verification.ID,
				ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(10343 + index*10),
			}
		}
		local.beforeCommit = task049TwoPartyBarrier(t)

		localResults := make([]result, len(localCommands))
		workerContext, cancelWorkers := context.WithCancel(t.Context())
		t.Cleanup(cancelWorkers)
		completed := make(chan struct{}, len(localCommands))
		for index := range localCommands {
			go func(index int) {
				defer func() { completed <- struct{}{} }()
				localResults[index].ledger, localResults[index].changed, localResults[index].err =
					arena.NewGoldenSubmissionUseCase(local).Submit(workerContext, localCommands[index])
			}(index)
		}
		require.True(t, task049AwaitCompletions(t, completed, len(localCommands), "duplicate submissions"))
		cancelWorkers()
		for _, localResult := range localResults {
			require.NoError(t, localResult.err)
			require.True(t, localResult.changed)
		}
		retained := local.snapshot()
		require.Len(t, retained.Submissions, 1)
		require.Len(t, retained.Receipts, 2)
		require.Equal(t, uint64(2), retained.NextSubmissionID)
		dispositions := map[arena.GoldenSubmissionDisposition]int{}
		for _, receipt := range retained.Receipts {
			dispositions[receipt.Disposition]++
		}
		require.Equal(t, 1, dispositions[arena.GoldenSubmissionAccepted])
		require.Equal(t, 1, dispositions[arena.GoldenSubmissionDuplicate])
	})

	t.Run("first correct result wins and every command replays globally", func(t *testing.T) {
		participantID := stored.Submissions[0].ParticipantID
		verification := task049Verification(scope, execution, participantID, 10400)
		repository.verifications[verification.ID] = verification
		duplicate := arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(10410), ActorParticipantID: participantID,
			ParticipantID: participantID, VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(10411),
		}
		updated, changed, submitErr := arena.NewGoldenSubmissionUseCase(repository).Submit(t.Context(), duplicate)
		require.NoError(t, submitErr)
		require.True(t, changed, "the duplicate command receipt must be durable")
		require.Len(t, updated.Submissions, 3)
		require.Len(t, updated.Receipts, 4)
		require.Equal(t, arena.GoldenSubmissionDuplicate, updated.Receipts[3].Disposition)
		require.Equal(t, updated.Submissions[0].ID, updated.Receipts[3].SubmissionID)

		repository.archiveCurrent()
		beforeReplayCommits := repository.commitCount()
		beforeReplay := repository.stateSnapshot()
		replayed, replayChanged, replayErr := arena.NewGoldenSubmissionUseCase(repository).Submit(t.Context(), duplicate)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, updated.Expectation(), replayed.Expectation())
		require.Equal(t, beforeReplayCommits, repository.commitCount())
		require.Equal(t, beforeReplay, repository.stateSnapshot())
		repository.restoreCurrent()

		secondVerification := task049Verification(scope, execution, participantID, 10430)
		repository.verifications[secondVerification.ID] = secondVerification
		secondDuplicate := arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(10440), ActorParticipantID: participantID,
			ParticipantID: participantID, VerificationID: secondVerification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(10441),
		}
		beforeSecondDuplicate := repository.snapshot()
		beforeSecondDuplicateCommits := repository.commitCount()
		beforeSecondDuplicateState := repository.stateSnapshot()
		result, secondChanged, secondErr := arena.NewGoldenSubmissionUseCase(repository).Submit(
			t.Context(), secondDuplicate,
		)
		require.Nil(t, result)
		require.False(t, secondChanged)
		require.ErrorIs(t, secondErr, arena.ErrGoldenSubmissionDuplicateLimit)
		require.Equal(t, beforeSecondDuplicate, repository.snapshot())
		require.Equal(t, beforeSecondDuplicateCommits, repository.commitCount())
		require.Equal(t, beforeSecondDuplicateState, repository.stateSnapshot())

		forgedAmplification := beforeSecondDuplicate.Snapshot()
		expected := forgedAmplification.Expectation()
		previousRevisionID := forgedAmplification.RevisionID
		forgedAmplification.PreviousRevisionID = &previousRevisionID
		forgedAmplification.RevisionID = task049ID(10442)
		forgedAmplification.Revision++
		forgedAmplification.Receipts = append(forgedAmplification.Receipts, arena.GoldenSubmissionReceipt{
			CommandID: task049ID(10443), Scope: scope, ParticipantID: participantID,
			VerificationID: secondVerification.ID,
			CommandDigest:  sha256.Sum256([]byte("second duplicate command")),
			Disposition:    arena.GoldenSubmissionDuplicate,
			SubmissionID:   forgedAmplification.Submissions[0].ID, Expected: expected,
			ResultRevisionID: forgedAmplification.RevisionID,
			ResultRevision:   forgedAmplification.Revision,
			CommittedAt:      startedAt.Add(12 * time.Second),
		})
		task049SealSubmissionLedger(t, &forgedAmplification)
		require.ErrorIs(t, forgedAmplification.Validate(), arena.ErrInvalidGoldenSubmission)

		reused := duplicate
		reused.Scope.State.GroupID = task049ID(10420)
		beforeReuseCommits := repository.commitCount()
		beforeReuse := repository.stateSnapshot()
		result, reusedChanged, reusedErr := arena.NewGoldenSubmissionUseCase(repository).Submit(t.Context(), reused)
		require.Nil(t, result)
		require.False(t, reusedChanged)
		require.ErrorIs(t, reusedErr, arena.ErrGoldenSubmissionCommandReuse)
		require.Equal(t, beforeReuseCommits, repository.commitCount())
		require.Equal(t, beforeReuse, repository.stateSnapshot())

		beforeMutation := repository.snapshot()
		updated.Submissions[0].ParticipantID = task049ID(10421)
		updated.Receipts[0].CommandID = task049ID(10422)
		require.Equal(t, beforeMutation, repository.snapshot(), "Submit return must not alias repository storage")

		forged := beforeMutation.Snapshot()
		forged.Receipts[len(forged.Receipts)-1].SubmissionID = forged.Submissions[1].ID
		task049SealSubmissionLedger(t, &forged)
		require.ErrorIs(t, forged.Validate(), arena.ErrInvalidGoldenSubmission)

		forgedPredecessor := beforeMutation.Snapshot()
		forgedPreviousRevisionID := task049ID(10423)
		forgedPredecessor.PreviousRevisionID = &forgedPreviousRevisionID
		task049SealSubmissionLedger(t, &forgedPredecessor)
		require.ErrorIs(t, forgedPredecessor.Validate(), arena.ErrInvalidGoldenSubmission)
	})

	t.Run("fails closed before any write", func(t *testing.T) {
		base := commands[0]
		tests := []struct {
			name      string
			mutate    func(*arena.GoldenSubmissionCommand, *task049SubmissionRepository)
			wantError error
		}{
			{name: "wrong actor", mutate: func(command *arena.GoldenSubmissionCommand, _ *task049SubmissionRepository) {
				command.CommandID, command.NextLedgerRevisionID = task049ID(10501), task049ID(10502)
				command.ActorParticipantID = participants[1]
			}, wantError: domain.ErrArenaAssignmentParticipant},
			{name: "stale execution", mutate: func(command *arena.GoldenSubmissionCommand, _ *task049SubmissionRepository) {
				command.CommandID, command.NextLedgerRevisionID = task049ID(10511), task049ID(10512)
				command.ExpectedExecution.Revision++
			}, wantError: arena.ErrGoldenSubmissionAuthorityConflict},
			{name: "stale assignment", mutate: func(command *arena.GoldenSubmissionCommand, _ *task049SubmissionRepository) {
				command.CommandID, command.NextLedgerRevisionID = task049ID(10521), task049ID(10522)
				command.Scope.AssignmentID = task049ID(10523)
			}, wantError: arena.ErrInvalidGoldenSubmission},
			{name: "stale snapshot", mutate: func(command *arena.GoldenSubmissionCommand, _ *task049SubmissionRepository) {
				command.CommandID, command.NextLedgerRevisionID = task049ID(10531), task049ID(10532)
				command.Scope.SnapshotID = task049ID(10533)
			}, wantError: arena.ErrInvalidGoldenSubmission},
			{name: "incorrect attestation", mutate: func(command *arena.GoldenSubmissionCommand, repository *task049SubmissionRepository) {
				command.CommandID, command.NextLedgerRevisionID = task049ID(10541), task049ID(10542)
				verification := task049Verification(scope, execution, participants[1], 10543)
				verification.Correct = false
				repository.verifications[verification.ID] = verification
				command.ActorParticipantID, command.ParticipantID, command.VerificationID = participants[1], participants[1], verification.ID
			}, wantError: arena.ErrGoldenSubmissionIncorrect},
			{name: "ambiguous evidence", mutate: func(command *arena.GoldenSubmissionCommand, repository *task049SubmissionRepository) {
				command.CommandID, command.NextLedgerRevisionID = task049ID(10551), task049ID(10552)
				verification := task049Verification(scope, execution, participants[1], 10553)
				verification.EvidenceDigest = [sha256.Size]byte{}
				repository.verifications[verification.ID] = verification
				command.ActorParticipantID, command.ParticipantID, command.VerificationID = participants[1], participants[1], verification.ID
			}, wantError: arena.ErrInvalidGoldenSubmission},
		}
		for _, testCase := range tests {
			t.Run(testCase.name, func(t *testing.T) {
				local := repository.clone()
				command := base
				testCase.mutate(&command, local)
				before := local.commitCount()
				result, changed, submitErr := arena.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, submitErr, testCase.wantError)
				require.Equal(t, before, local.commitCount())
			})
		}
	})

	t.Run("rejects paused expired and terminal executions", func(t *testing.T) {
		for _, state := range []domain.ArenaWaveState{
			domain.ArenaWaveStatePaused,
			domain.ArenaWaveStateCompleted,
		} {
			local := repository.clone()
			local.execution.Wave.State = state
			if state == domain.ArenaWaveStatePaused {
				pausedAt := startedAt.Add(time.Second)
				local.execution.Wave.PausedAt = &pausedAt
			}
			command := commands[0]
			command.CommandID, command.NextLedgerRevisionID = uuid.New(), uuid.New()
			result, changed, submitErr := arena.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
			require.Nil(t, result)
			require.False(t, changed)
			require.ErrorIs(t, submitErr, arena.ErrGoldenSubmissionClosed)
		}

		localLedger, buildErr := arena.NewGoldenSubmissionLedger(scope, task049ID(10590))
		require.NoError(t, buildErr)
		local := newTask049SubmissionRepository(execution, localLedger, execution.Start.Deadline.Add(time.Nanosecond))
		local.committedAt = execution.Start.Deadline.Add(time.Nanosecond)
		verification := task049Verification(scope, execution, participants[1], 10600)
		local.verifications[verification.ID] = verification
		command := arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(10603), ActorParticipantID: participants[1],
			ParticipantID: participants[1], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(10604),
		}
		result, changed, submitErr := arena.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
		require.Nil(t, result)
		require.False(t, changed)
		require.ErrorIs(t, submitErr, arena.ErrGoldenSubmissionClosed)
	})

	t.Run("deadline equality is closed", func(t *testing.T) {
		localLedger, buildErr := arena.NewGoldenSubmissionLedger(scope, task049ID(10610))
		require.NoError(t, buildErr)
		local := newTask049SubmissionRepository(execution, localLedger, execution.Start.Deadline)
		verification := task049Verification(scope, execution, participants[0], 10620)
		local.verifications[verification.ID] = verification
		command := arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(10622), ActorParticipantID: participants[0],
			ParticipantID: participants[0], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(10623),
		}

		result, localChanged, submitErr := arena.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, submitErr, arena.ErrGoldenSubmissionClosed)
		require.Empty(t, local.snapshot().Receipts)
		require.Equal(t, uint64(1), local.snapshot().NextSubmissionID)
	})

	t.Run("repository cannot return a receipt at the deadline", func(t *testing.T) {
		initial, buildErr := arena.NewGoldenSubmissionLedger(scope, task049ID(10670))
		require.NoError(t, buildErr)
		verification := task049Verification(scope, execution, participants[0], 10671)
		command := arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(10673), ActorParticipantID: participants[0],
			ParticipantID: participants[0], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(10674),
		}
		canonical := newTask049SubmissionRepository(execution, initial, startedAt.Add(5*time.Second))
		canonical.verifications[verification.ID] = verification
		returned, returnedChanged, submitErr := arena.NewGoldenSubmissionUseCase(canonical).Submit(t.Context(), command)
		require.NoError(t, submitErr)
		require.True(t, returnedChanged)
		late := returned.Snapshot()
		late.Submissions[0].CommittedAt = execution.Start.Deadline
		late.Receipts[0].CommittedAt = execution.Start.Deadline
		task049SealSubmissionLedger(t, &late)
		require.NoError(t, late.Validate())

		malicious := newTask049SubmissionRepository(execution, initial, startedAt.Add(5*time.Second))
		malicious.verifications[verification.ID] = verification
		malicious.returnLedger = &late
		malicious.returnChanged = true
		result, localChanged, submitErr := arena.NewGoldenSubmissionUseCase(malicious).Submit(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, submitErr, domain.ErrInternal)
	})

	t.Run("verification drift after load consumes no receipt or sequence", func(t *testing.T) {
		localLedger, buildErr := arena.NewGoldenSubmissionLedger(scope, task049ID(10630))
		require.NoError(t, buildErr)
		local := newTask049SubmissionRepository(execution, localLedger, startedAt.Add(5*time.Second))
		verification := task049Verification(scope, execution, participants[0], 10640)
		local.verifications[verification.ID] = verification
		entered, signalEntered := task049Latch(t)
		release, signalRelease := task049Latch(t)
		local.beforeCommit = func() {
			signalEntered()
			task049AwaitSignal(t, release, "submission verification release")
		}
		command := arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(10642), ActorParticipantID: participants[0],
			ParticipantID: participants[0], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(10643),
		}
		type result struct {
			ledger  *arena.GoldenSubmissionLedger
			changed bool
			err     error
		}
		finished := make(chan result, 1)
		go func() {
			value, changed, err := arena.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
			finished <- result{ledger: value, changed: changed, err: err}
		}()
		require.True(t, task049AwaitSignal(t, entered, "submission verification commit entry"))
		local.mu.Lock()
		changedVerification := local.verifications[verification.ID]
		changedVerification.RevisionID = task049ID(10644)
		changedVerification.EvidenceDigest = sha256.Sum256([]byte("replacement evidence"))
		local.verifications[verification.ID] = changedVerification
		local.mu.Unlock()
		signalRelease()

		got, received := task049AwaitValue(t, finished, "submission verification result")
		require.True(t, received)
		require.Nil(t, got.ledger)
		require.False(t, got.changed)
		require.ErrorIs(t, got.err, arena.ErrGoldenSubmissionAuthorityConflict)
		require.Empty(t, local.snapshot().Receipts)
		require.Equal(t, uint64(1), local.snapshot().NextSubmissionID)
	})

	t.Run("new identities cannot alias active execution authority", func(t *testing.T) {
		localLedger, buildErr := arena.NewGoldenSubmissionLedger(scope, task049ID(10650))
		require.NoError(t, buildErr)
		local := newTask049SubmissionRepository(execution, localLedger, startedAt.Add(5*time.Second))
		verification := task049Verification(scope, execution, participants[0], 10660)
		local.verifications[verification.ID] = verification
		command := arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(10662), ActorParticipantID: participants[0],
			ParticipantID: participants[0], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: scope.AssignmentID,
		}

		result, localChanged, submitErr := arena.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, submitErr, arena.ErrInvalidGoldenSubmission)
		require.Empty(t, local.snapshot().Receipts)

		localLedger, buildErr = arena.NewGoldenSubmissionLedger(scope, uuid.New())
		require.NoError(t, buildErr)
		local = newTask049SubmissionRepository(execution, localLedger, startedAt.Add(5*time.Second))
		verification = task049Verification(scope, execution, participants[0], 10710)
		verification.ID = execution.Wave.ID
		local.verifications[verification.ID] = verification
		command = arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(10712), ActorParticipantID: participants[0],
			ParticipantID: participants[0], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(10713),
		}
		result, localChanged, submitErr = arena.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, submitErr, arena.ErrInvalidGoldenSubmission)
		require.Empty(t, local.snapshot().Receipts)
	})

	t.Run("new identities cannot alias the live verification revision", func(t *testing.T) {
		for _, aliasCommand := range []bool{true, false} {
			localLedger, buildErr := arena.NewGoldenSubmissionLedger(scope, uuid.New())
			require.NoError(t, buildErr)
			local := newTask049SubmissionRepository(execution, localLedger, startedAt.Add(5*time.Second))
			verification := task049Verification(scope, execution, participants[0], 10680)
			local.verifications[verification.ID] = verification
			command := arena.GoldenSubmissionCommand{
				Scope: scope, CommandID: task049ID(10682), ActorParticipantID: participants[0],
				ParticipantID: participants[0], VerificationID: verification.ID,
				ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(10683),
			}
			if aliasCommand {
				command.CommandID = verification.RevisionID
			} else {
				command.NextLedgerRevisionID = verification.RevisionID
			}

			result, localChanged, submitErr := arena.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
			require.Nil(t, result)
			require.False(t, localChanged)
			require.ErrorIs(t, submitErr, arena.ErrInvalidGoldenSubmission)
			require.Empty(t, local.snapshot().Receipts)
		}

		localLedger, buildErr := arena.NewGoldenSubmissionLedger(scope, uuid.New())
		require.NoError(t, buildErr)
		local := newTask049SubmissionRepository(execution, localLedger, startedAt.Add(5*time.Second))
		verification := task049Verification(scope, execution, participants[0], 10690)
		verification.RevisionID = execution.RevisionID
		local.verifications[verification.ID] = verification
		command := arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(10692), ActorParticipantID: participants[0],
			ParticipantID: participants[0], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(10693),
		}
		result, localChanged, submitErr := arena.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, submitErr, arena.ErrInvalidGoldenSubmission)
		require.Empty(t, local.snapshot().Receipts)
	})

	t.Run("returns defensive snapshots and retains no raw flag or validator text", func(t *testing.T) {
		participantID := participants[0]
		localLedger, buildErr := arena.NewGoldenSubmissionLedger(scope, task049ID(10709))
		require.NoError(t, buildErr)
		local := newTask049SubmissionRepository(execution, localLedger, startedAt.Add(5*time.Second))
		verification := task049Verification(scope, execution, participantID, 10700)
		local.verifications[verification.ID] = verification
		command := arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(10702), ActorParticipantID: participantID,
			ParticipantID: participantID, VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(10703),
		}
		returned, localChanged, submitErr := arena.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
		require.NoError(t, submitErr)
		require.True(t, localChanged)
		before := local.snapshot()
		returned.Submissions[0].ParticipantID = task049ID(10704)
		returned.Receipts[0].CommandID = task049ID(10705)
		after := local.snapshot()
		require.Equal(t, before, after)
		require.NotContains(t, task049Encode(t, after), execution.Assignment.Snapshot.Flag)
		require.NotContains(t, task049Encode(t, after), "validator internal detail")
	})
}

type task049SubmissionRepository struct {
	transactionMu        *sync.Mutex
	mu                   sync.Mutex
	execution            arena.GoldenWaveExecution
	ledger               arena.GoldenSubmissionLedger
	archived             arena.GoldenSubmissionLedger
	verifications        map[uuid.UUID]arena.GoldenSubmissionVerification
	replays              map[uuid.UUID]arena.GoldenSubmissionReceipt
	committedAt          time.Time
	committedAtByCommand map[uuid.UUID]time.Time
	nextID               uint64
	commits              int
	terminalID           uuid.UUID
	terminalDigest       [sha256.Size]byte
	beforeCommit         func()
	returnLedger         *arena.GoldenSubmissionLedger
	returnChanged        bool
}

type task049SubmissionRepositoryState struct {
	execution            arena.GoldenWaveExecution
	ledger               arena.GoldenSubmissionLedger
	archived             arena.GoldenSubmissionLedger
	verifications        map[uuid.UUID]arena.GoldenSubmissionVerification
	replays              map[uuid.UUID]arena.GoldenSubmissionReceipt
	committedAt          time.Time
	committedAtByCommand map[uuid.UUID]time.Time
	nextID               uint64
	commits              int
	terminalID           uuid.UUID
	terminalDigest       [sha256.Size]byte
	returnLedger         *arena.GoldenSubmissionLedger
	returnChanged        bool
	hasBeforeCommit      bool
}

func newTask049SubmissionRepository(
	execution arena.GoldenWaveExecution,
	ledger arena.GoldenSubmissionLedger,
	committedAt time.Time,
) *task049SubmissionRepository {
	return &task049SubmissionRepository{
		transactionMu: &sync.Mutex{},
		execution:     execution.Snapshot(), ledger: ledger.Snapshot(),
		verifications:        make(map[uuid.UUID]arena.GoldenSubmissionVerification),
		replays:              make(map[uuid.UUID]arena.GoldenSubmissionReceipt),
		committedAtByCommand: make(map[uuid.UUID]time.Time),
		committedAt:          committedAt, nextID: ledger.NextSubmissionID,
	}
}

func (r *task049SubmissionRepository) LoadGoldenSubmissionAuthority(
	_ context.Context,
	scope arena.GoldenSubmissionScope,
	commandID uuid.UUID,
	verificationID uuid.UUID,
) (arena.GoldenSubmissionAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ledger := r.ledger
	if ledger.Scope.State.TournamentID == uuid.Nil {
		ledger = r.archived
	}
	authority := arena.GoldenSubmissionAuthority{
		Scope: scope, Execution: r.execution.Snapshot(), Ledger: ledger.Snapshot(),
		Verification:     r.verifications[verificationID],
		TerminalCommitID: r.terminalID, TerminalCommitDigest: r.terminalDigest,
	}
	if replay, found := r.replays[commandID]; found {
		clone := replay
		authority.Replay = &clone
	}
	return authority, nil
}

func (r *task049SubmissionRepository) CommitGoldenSubmission(
	_ context.Context,
	commit arena.GoldenSubmissionCommit,
) (*arena.GoldenSubmissionLedger, bool, error) {
	if r.beforeCommit != nil {
		r.beforeCommit()
	}
	r.transactionMu.Lock()
	defer r.transactionMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.execution.Expectation().Equal(commit.ExpectedExecution) ||
		!r.ledger.Expectation().Equal(commit.ExpectedLedger) {
		return nil, false, domain.ErrConflict
	}
	if _, found := r.replays[commit.Command.CommandID]; found {
		return nil, false, domain.ErrConflict
	}
	live := arena.GoldenSubmissionAuthority{
		Scope: commit.Command.Scope, Execution: r.execution.Snapshot(), Ledger: r.ledger.Snapshot(),
		Verification:     r.verifications[commit.Command.VerificationID],
		TerminalCommitID: r.terminalID, TerminalCommitDigest: r.terminalDigest,
	}
	nextID := uint64(0)
	if !r.ledger.HasParticipant(commit.Command.ParticipantID) {
		nextID = r.nextID
	}
	committedAt := r.committedAt
	if specific, found := r.committedAtByCommand[commit.Command.CommandID]; found {
		committedAt = specific
	}
	next, applyErr := arena.ApplyGoldenSubmissionCommit(commit, live, nextID, committedAt)
	if applyErr != nil {
		return nil, false, applyErr
	}
	if nextID != 0 {
		r.nextID++
	}
	r.ledger = next.Snapshot()
	r.replays[commit.Command.CommandID] = next.Receipts[len(next.Receipts)-1]
	r.commits++
	result := r.ledger.Snapshot()
	if r.returnLedger != nil {
		result = r.returnLedger.Snapshot()
		return &result, r.returnChanged, nil
	}
	return &result, true, nil
}

func (r *task049SubmissionRepository) snapshot() arena.GoldenSubmissionLedger {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ledger.Scope.State.TournamentID == uuid.Nil {
		return r.archived.Snapshot()
	}
	return r.ledger.Snapshot()
}

func (r *task049SubmissionRepository) archiveCurrent() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.archived = r.ledger.Snapshot()
	r.ledger = arena.GoldenSubmissionLedger{}
}

func (r *task049SubmissionRepository) restoreCurrent() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ledger = r.archived.Snapshot()
}

func (r *task049SubmissionRepository) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *task049SubmissionRepository) stateSnapshot() task049SubmissionRepositoryState {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := task049SubmissionRepositoryState{
		execution: r.execution.Snapshot(), ledger: r.ledger.Snapshot(), archived: r.archived.Snapshot(),
		verifications:        make(map[uuid.UUID]arena.GoldenSubmissionVerification, len(r.verifications)),
		replays:              make(map[uuid.UUID]arena.GoldenSubmissionReceipt, len(r.replays)),
		committedAt:          r.committedAt,
		committedAtByCommand: make(map[uuid.UUID]time.Time, len(r.committedAtByCommand)),
		nextID:               r.nextID, commits: r.commits,
		terminalID: r.terminalID, terminalDigest: r.terminalDigest,
		returnChanged: r.returnChanged, hasBeforeCommit: r.beforeCommit != nil,
	}
	for key, value := range r.verifications {
		state.verifications[key] = value
	}
	for key, value := range r.replays {
		state.replays[key] = value
	}
	for key, value := range r.committedAtByCommand {
		state.committedAtByCommand[key] = value
	}
	if r.returnLedger != nil {
		clone := r.returnLedger.Snapshot()
		state.returnLedger = &clone
	}
	return state
}

func (r *task049SubmissionRepository) clone() *task049SubmissionRepository {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := newTask049SubmissionRepository(r.execution, r.ledger, r.committedAt)
	clone.archived = r.archived.Snapshot()
	clone.nextID = r.nextID
	for key, value := range r.verifications {
		clone.verifications[key] = value
	}
	for key, value := range r.replays {
		clone.replays[key] = value
	}
	for key, value := range r.committedAtByCommand {
		clone.committedAtByCommand[key] = value
	}
	return clone
}

func task049StartedExecution(t *testing.T, startedAt time.Time) arena.GoldenWaveExecution {
	t.Helper()
	_, execution := task049StartedFixture(t, startedAt)
	return execution
}

func task049StartedFixture(t *testing.T, startedAt time.Time) (arena.GoldenState, arena.GoldenWaveExecution) {
	t.Helper()
	openedAt := startedAt.Add(-20 * time.Second)
	state := task048GoldenState(t, openedAt)
	repository := &goldenWaveRepositoryFake{state: state}
	execution := task048OpenGoldenExecution(t, repository, state, openedAt, 20000)
	execution = task048ReadyAll(t, repository, execution, openedAt, 20100)
	authority := task048AuthorityLease(startedAt, state.Scope.TournamentID)
	repository.setAuthority(authority, startedAt)
	started, changed, err := arena.NewGoldenStartUseCase(
		repository,
		fixedArenaClock{now: startedAt},
	).Start(t.Context(), task048StartCommand(execution, authority, 20200))
	require.NoError(t, err)
	require.True(t, changed)
	return state, *started
}

func task049SubmissionScope(execution arena.GoldenWaveExecution) arena.GoldenSubmissionScope {
	return arena.GoldenSubmissionScope{
		State: execution.Scope, AttemptID: execution.Attempt.ID, WaveID: execution.Wave.ID,
		AssignmentID: execution.Assignment.ID, SnapshotID: execution.Assignment.Snapshot.SnapshotID,
		TaskID: execution.Assignment.Snapshot.TaskID,
	}
}

func task049Verification(
	scope arena.GoldenSubmissionScope,
	execution arena.GoldenWaveExecution,
	participantID uuid.UUID,
	base int,
) arena.GoldenSubmissionVerification {
	digest := sha256.Sum256([]byte("correct:" + participantID.String()))
	return arena.GoldenSubmissionVerification{
		ID: task049ID(base), RevisionID: task049ID(base + 1), Scope: scope,
		ParticipantID: participantID, Correct: true, VerifiedAt: execution.Start.StartedAt.Add(time.Second),
		EvidenceDigest: digest, Authority: execution.Start.Authority.Identity,
		AssignmentDigest: execution.Assignment.PayloadDigest,
	}
}

func task049SubmissionIDs(values []arena.GoldenSubmissionRecord) []uint64 {
	result := make([]uint64, len(values))
	for index, value := range values {
		result[index] = value.ID
	}
	return result
}

func task049PositionOrderIDs(values []arena.GoldenPositionOrderEntry) []uint64 {
	result := make([]uint64, len(values))
	for index, value := range values {
		result[index] = value.SubmissionID
	}
	return result
}

func task049SealSubmissionLedger(t *testing.T, ledger *arena.GoldenSubmissionLedger) {
	t.Helper()
	ledger.PayloadDigest = task049GobDigest(t, struct {
		Scope              arena.GoldenSubmissionScope
		RevisionID         uuid.UUID
		Revision           int64
		PreviousRevisionID *uuid.UUID
		NextSubmissionID   uint64
		Submissions        []arena.GoldenSubmissionRecord
		Receipts           []arena.GoldenSubmissionReceipt
	}{
		Scope: ledger.Scope, RevisionID: ledger.RevisionID, Revision: ledger.Revision,
		PreviousRevisionID: ledger.PreviousRevisionID, NextSubmissionID: ledger.NextSubmissionID,
		Submissions: ledger.Submissions, Receipts: ledger.Receipts,
	})
}

func task049Encode(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

func task049ID(value int) uuid.UUID {
	return uuid.MustParse("49000000-0000-0000-0000-" + task046Suffix(value))
}

func task049TwoPartyBarrier(t *testing.T) func() {
	t.Helper()
	var mu sync.Mutex
	arrived := 0
	release := make(chan struct{})
	timedOut := make(chan struct{})
	timer := time.AfterFunc(2*time.Second, func() { close(timedOut) })
	return func() {
		mu.Lock()
		if arrived >= 2 {
			mu.Unlock()
			return
		}
		arrived++
		if arrived == 2 {
			timer.Stop()
			close(release)
		}
		mu.Unlock()
		select {
		case <-release:
		case <-timedOut:
			t.Errorf("two-party repository barrier timed out")
		}
	}
}

const task049WaitTimeout = 2 * time.Second

func task049AwaitCompletions(t *testing.T, completed <-chan struct{}, count int, label string) bool {
	t.Helper()
	timer := time.NewTimer(task049WaitTimeout)
	defer timer.Stop()
	for received := 0; received < count; received++ {
		select {
		case <-completed:
		case <-timer.C:
			t.Errorf("%s timed out after %d of %d completions", label, received, count)
			return false
		}
	}
	return true
}

func task049Latch(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	ready := make(chan struct{})
	var once sync.Once
	signal := func() {
		once.Do(func() { close(ready) })
	}
	t.Cleanup(signal)
	return ready, signal
}

func task049AwaitSignal(t *testing.T, signal <-chan struct{}, label string) bool {
	t.Helper()
	select {
	case <-signal:
		return true
	case <-time.After(task049WaitTimeout):
		t.Errorf("%s timed out", label)
		return false
	}
}

func task049AwaitValue[T any](t *testing.T, values <-chan T, label string) (T, bool) {
	t.Helper()
	select {
	case value := <-values:
		return value, true
	case <-time.After(task049WaitTimeout):
		t.Errorf("%s timed out", label)
		var zero T
		return zero, false
	}
}

var _ arena.GoldenSubmissionRepository = (*task049SubmissionRepository)(nil)
