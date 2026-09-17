package submission_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"
	"github.com/stretchr/testify/require"
)

func TestGoldenProvisionalOrder(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 1, 15, 0, 0, 0, time.UTC)
	execution := submissionTask049StartedExecution(t, startedAt)
	participants := execution.Membership.ParticipantIDs
	require.GreaterOrEqual(t, len(participants), 3)
	scope := submissionTask049SubmissionScope(execution)
	ledger, err := goldenusecase.NewGoldenSubmissionLedger(scope, submissionTask049ID(10001))
	require.NoError(t, err)
	repository := submissionNewTask049SubmissionHarness(t, execution, ledger, startedAt.Add(10*time.Second))

	commands := make([]goldenusecase.GoldenSubmissionCommand, 3)
	for index, participantID := range participants[:3] {
		verification := submissionTask049Verification(scope, execution, participantID, 10100+index*10)
		repository.verifications[verification.ID] = verification
		commands[index] = goldenusecase.GoldenSubmissionCommand{
			Scope: scope, CommandID: submissionTask049ID(10200 + index),
			ActorParticipantID: participantID, ParticipantID: participantID,
			VerificationID: verification.ID, ExpectedExecution: execution.Expectation(),
			NextLedgerRevisionID: submissionTask049ID(10300 + index),
		}
	}

	type result struct {
		ledger  *goldenusecase.GoldenSubmissionLedger
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
				goldenusecase.NewGoldenSubmissionUseCase(repository).Submit(workerContext, commands[index])
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
		localLedger, buildErr := goldenusecase.NewGoldenSubmissionLedger(scope, submissionTask049ID(10320))
		require.NoError(t, buildErr)
		local := submissionNewTask049SubmissionHarness(t, execution, localLedger, startedAt.Add(9*time.Second))
		first := commands[0]
		first.CommandID, first.NextLedgerRevisionID = submissionTask049ID(10321), submissionTask049ID(10322)
		second := commands[1]
		second.CommandID, second.NextLedgerRevisionID = submissionTask049ID(10323), submissionTask049ID(10324)
		local.verifications[first.VerificationID] = repository.verifications[first.VerificationID]
		local.verifications[second.VerificationID] = repository.verifications[second.VerificationID]
		local.committedAtByCommand[first.CommandID] = startedAt.Add(9 * time.Second)
		local.committedAtByCommand[second.CommandID] = startedAt.Add(8 * time.Second)

		_, firstChanged, firstErr := goldenusecase.NewGoldenSubmissionUseCase(local).Submit(t.Context(), first)
		require.NoError(t, firstErr)
		require.True(t, firstChanged)
		ordered, secondChanged, secondErr := goldenusecase.NewGoldenSubmissionUseCase(local).Submit(t.Context(), second)
		require.NoError(t, secondErr)
		require.True(t, secondChanged)
		require.Equal(t, []uint64{2, 1}, task049SubmissionIDs(ordered.ProvisionalOrder()))
		require.Equal(t, []uint64{1, 2}, task049SubmissionIDs(ordered.Submissions))

		require.Equal(t, []uint64{2, 1}, task049SubmissionIDs(ordered.ProvisionalOrder()))
	})

	t.Run("concurrent correct commands for one participant retain only the first", func(t *testing.T) {
		localLedger, buildErr := goldenusecase.NewGoldenSubmissionLedger(scope, submissionTask049ID(10330))
		require.NoError(t, buildErr)
		local := submissionNewTask049SubmissionHarness(t, execution, localLedger, startedAt.Add(9*time.Second))
		participantID := participants[0]
		localCommands := make([]goldenusecase.GoldenSubmissionCommand, 2)
		for index := range localCommands {
			verification := submissionTask049Verification(scope, execution, participantID, 10340+index*10)
			local.verifications[verification.ID] = verification
			localCommands[index] = goldenusecase.GoldenSubmissionCommand{
				Scope: scope, CommandID: submissionTask049ID(10342 + index*10), ActorParticipantID: participantID,
				ParticipantID: participantID, VerificationID: verification.ID,
				ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: submissionTask049ID(10343 + index*10),
			}
		}
		local.beforeCommit = submissionTask049TwoPartyBarrier(t)

		localResults := make([]result, len(localCommands))
		workerContext, cancelWorkers := context.WithCancel(t.Context())
		t.Cleanup(cancelWorkers)
		completed := make(chan struct{}, len(localCommands))
		for index := range localCommands {
			go func(index int) {
				defer func() { completed <- struct{}{} }()
				localResults[index].ledger, localResults[index].changed, localResults[index].err =
					goldenusecase.NewGoldenSubmissionUseCase(local).Submit(workerContext, localCommands[index])
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
		dispositions := map[goldenusecase.GoldenSubmissionDisposition]int{}
		for _, receipt := range retained.Receipts {
			dispositions[receipt.Disposition]++
		}
		require.Equal(t, 1, dispositions[goldenusecase.GoldenSubmissionAccepted])
		require.Equal(t, 1, dispositions[goldenusecase.GoldenSubmissionDuplicate])
	})

	t.Run("first correct result wins and every command replays globally", func(t *testing.T) {
		participantID := stored.Submissions[0].ParticipantID
		verification := submissionTask049Verification(scope, execution, participantID, 10400)
		repository.verifications[verification.ID] = verification
		duplicate := goldenusecase.GoldenSubmissionCommand{
			Scope: scope, CommandID: submissionTask049ID(10410), ActorParticipantID: participantID,
			ParticipantID: participantID, VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: submissionTask049ID(10411),
		}
		updated, changed, submitErr := goldenusecase.NewGoldenSubmissionUseCase(repository).Submit(t.Context(), duplicate)
		require.NoError(t, submitErr)
		require.True(t, changed, "the duplicate command receipt must be durable")
		require.Len(t, updated.Submissions, 3)
		require.Len(t, updated.Receipts, 4)
		require.Equal(t, goldenusecase.GoldenSubmissionDuplicate, updated.Receipts[3].Disposition)
		require.Equal(t, updated.Submissions[0].ID, updated.Receipts[3].SubmissionID)

		repository.archiveCurrent()
		beforeReplayCommits := repository.commitCount()
		beforeReplay := repository.stateSnapshot()
		replayed, replayChanged, replayErr := goldenusecase.NewGoldenSubmissionUseCase(repository).Submit(t.Context(), duplicate)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, updated.Expectation(), replayed.Expectation())
		require.Equal(t, beforeReplayCommits, repository.commitCount())
		require.Equal(t, beforeReplay, repository.stateSnapshot())
		repository.restoreCurrent()

		secondVerification := submissionTask049Verification(scope, execution, participantID, 10430)
		repository.verifications[secondVerification.ID] = secondVerification
		secondDuplicate := goldenusecase.GoldenSubmissionCommand{
			Scope: scope, CommandID: submissionTask049ID(10440), ActorParticipantID: participantID,
			ParticipantID: participantID, VerificationID: secondVerification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: submissionTask049ID(10441),
		}
		beforeSecondDuplicate := repository.snapshot()
		beforeSecondDuplicateCommits := repository.commitCount()
		beforeSecondDuplicateState := repository.stateSnapshot()
		result, secondChanged, secondErr := goldenusecase.NewGoldenSubmissionUseCase(repository).Submit(
			t.Context(), secondDuplicate,
		)
		require.Nil(t, result)
		require.False(t, secondChanged)
		require.ErrorIs(t, secondErr, goldenusecase.ErrGoldenSubmissionDuplicateLimit)
		require.Equal(t, beforeSecondDuplicate, repository.snapshot())
		require.Equal(t, beforeSecondDuplicateCommits, repository.commitCount())
		require.Equal(t, beforeSecondDuplicateState, repository.stateSnapshot())

		forgedAmplification := beforeSecondDuplicate.Snapshot()
		expected := forgedAmplification.Expectation()
		previousRevisionID := forgedAmplification.RevisionID
		forgedAmplification.PreviousRevisionID = &previousRevisionID
		forgedAmplification.RevisionID = submissionTask049ID(10442)
		forgedAmplification.Revision++
		forgedAmplification.Receipts = append(forgedAmplification.Receipts, goldenusecase.GoldenSubmissionReceipt{
			CommandID: submissionTask049ID(10443), Scope: scope, ParticipantID: participantID,
			VerificationID: secondVerification.ID,
			CommandDigest:  sha256.Sum256([]byte("second duplicate command")),
			Disposition:    goldenusecase.GoldenSubmissionDuplicate,
			SubmissionID:   forgedAmplification.Submissions[0].ID, Expected: expected,
			ResultRevisionID: forgedAmplification.RevisionID,
			ResultRevision:   forgedAmplification.Revision,
			CommittedAt:      startedAt.Add(12 * time.Second),
		})
		submissionTask049SealSubmissionLedger(t, &forgedAmplification)
		require.ErrorIs(t, forgedAmplification.Validate(), goldenusecase.ErrInvalidGoldenSubmission)

		reused := duplicate
		reused.Scope.State.GroupID = submissionTask049ID(10420)
		beforeReuseCommits := repository.commitCount()
		beforeReuse := repository.stateSnapshot()
		result, reusedChanged, reusedErr := goldenusecase.NewGoldenSubmissionUseCase(repository).Submit(t.Context(), reused)
		require.Nil(t, result)
		require.False(t, reusedChanged)
		require.ErrorIs(t, reusedErr, goldenusecase.ErrGoldenSubmissionCommandReuse)
		require.Equal(t, beforeReuseCommits, repository.commitCount())
		require.Equal(t, beforeReuse, repository.stateSnapshot())

		beforeMutation := repository.snapshot()
		updated.Submissions[0].ParticipantID = submissionTask049ID(10421)
		updated.Receipts[0].CommandID = submissionTask049ID(10422)
		require.Equal(t, beforeMutation, repository.snapshot(), "Submit return must not alias repository storage")

		forged := beforeMutation.Snapshot()
		forged.Receipts[len(forged.Receipts)-1].SubmissionID = forged.Submissions[1].ID
		submissionTask049SealSubmissionLedger(t, &forged)
		require.ErrorIs(t, forged.Validate(), goldenusecase.ErrInvalidGoldenSubmission)

		forgedPredecessor := beforeMutation.Snapshot()
		forgedPreviousRevisionID := submissionTask049ID(10423)
		forgedPredecessor.PreviousRevisionID = &forgedPreviousRevisionID
		submissionTask049SealSubmissionLedger(t, &forgedPredecessor)
		require.ErrorIs(t, forgedPredecessor.Validate(), goldenusecase.ErrInvalidGoldenSubmission)
	})

	t.Run("fails closed before any write", func(t *testing.T) {
		base := commands[0]
		tests := []struct {
			name      string
			mutate    func(*goldenusecase.GoldenSubmissionCommand, *submissionTask049SubmissionHarness)
			wantError error
		}{
			{name: "wrong actor", mutate: func(command *goldenusecase.GoldenSubmissionCommand, _ *submissionTask049SubmissionHarness) {
				command.CommandID, command.NextLedgerRevisionID = submissionTask049ID(10501), submissionTask049ID(10502)
				command.ActorParticipantID = participants[1]
			}, wantError: domain.ErrAssignmentParticipant},
			{name: "stale execution", mutate: func(command *goldenusecase.GoldenSubmissionCommand, _ *submissionTask049SubmissionHarness) {
				command.CommandID, command.NextLedgerRevisionID = submissionTask049ID(10511), submissionTask049ID(10512)
				command.ExpectedExecution.Revision++
			}, wantError: goldenusecase.ErrGoldenSubmissionAuthorityConflict},
			{name: "stale assignment", mutate: func(command *goldenusecase.GoldenSubmissionCommand, _ *submissionTask049SubmissionHarness) {
				command.CommandID, command.NextLedgerRevisionID = submissionTask049ID(10521), submissionTask049ID(10522)
				command.Scope.AssignmentID = submissionTask049ID(10523)
			}, wantError: goldenusecase.ErrInvalidGoldenSubmission},
			{name: "stale snapshot", mutate: func(command *goldenusecase.GoldenSubmissionCommand, _ *submissionTask049SubmissionHarness) {
				command.CommandID, command.NextLedgerRevisionID = submissionTask049ID(10531), submissionTask049ID(10532)
				command.Scope.SnapshotID = submissionTask049ID(10533)
			}, wantError: goldenusecase.ErrInvalidGoldenSubmission},
			{name: "incorrect attestation", mutate: func(command *goldenusecase.GoldenSubmissionCommand, repository *submissionTask049SubmissionHarness) {
				command.CommandID, command.NextLedgerRevisionID = submissionTask049ID(10541), submissionTask049ID(10542)
				verification := submissionTask049Verification(scope, execution, participants[1], 10543)
				verification.Correct = false
				repository.verifications[verification.ID] = verification
				command.ActorParticipantID, command.ParticipantID, command.VerificationID = participants[1], participants[1], verification.ID
			}, wantError: goldenusecase.ErrGoldenSubmissionIncorrect},
			{name: "ambiguous evidence", mutate: func(command *goldenusecase.GoldenSubmissionCommand, repository *submissionTask049SubmissionHarness) {
				command.CommandID, command.NextLedgerRevisionID = submissionTask049ID(10551), submissionTask049ID(10552)
				verification := submissionTask049Verification(scope, execution, participants[1], 10553)
				verification.EvidenceDigest = [sha256.Size]byte{}
				repository.verifications[verification.ID] = verification
				command.ActorParticipantID, command.ParticipantID, command.VerificationID = participants[1], participants[1], verification.ID
			}, wantError: goldenusecase.ErrInvalidGoldenSubmission},
		}
		for _, testCase := range tests {
			t.Run(testCase.name, func(t *testing.T) {
				local := repository.clone(t)
				command := base
				testCase.mutate(&command, local)
				before := local.commitCount()
				result, changed, submitErr := goldenusecase.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, submitErr, testCase.wantError)
				require.Equal(t, before, local.commitCount())
			})
		}
	})
}
