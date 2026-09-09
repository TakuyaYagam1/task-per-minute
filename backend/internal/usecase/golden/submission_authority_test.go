package golden_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGoldenSubmissionAuthorityAndClosure(t *testing.T) {
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

	t.Run("rejects paused expired and terminal executions", func(t *testing.T) {
		for _, state := range []domain.WaveState{
			domain.WaveStatePaused,
			domain.WaveStateCompleted,
		} {
			local := repository.clone(t)
			local.execution.Wave.State = state
			if state == domain.WaveStatePaused {
				pausedAt := startedAt.Add(time.Second)
				local.execution.Wave.PausedAt = &pausedAt
			}
			command := commands[0]
			command.CommandID, command.NextLedgerRevisionID = uuid.New(), uuid.New()
			result, changed, submitErr := goldenusecase.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
			require.Nil(t, result)
			require.False(t, changed)
			require.ErrorIs(t, submitErr, goldenusecase.ErrGoldenSubmissionClosed)
		}

		localLedger, buildErr := goldenusecase.NewGoldenSubmissionLedger(scope, submissionTask049ID(10590))
		require.NoError(t, buildErr)
		local := submissionNewTask049SubmissionHarness(t, execution, localLedger, execution.Start.Deadline.Add(time.Nanosecond))
		local.committedAt = execution.Start.Deadline.Add(time.Nanosecond)
		verification := submissionTask049Verification(scope, execution, participants[1], 10600)
		local.verifications[verification.ID] = verification
		command := goldenusecase.GoldenSubmissionCommand{
			Scope: scope, CommandID: submissionTask049ID(10603), ActorParticipantID: participants[1],
			ParticipantID: participants[1], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: submissionTask049ID(10604),
		}
		result, changed, submitErr := goldenusecase.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
		require.Nil(t, result)
		require.False(t, changed)
		require.ErrorIs(t, submitErr, goldenusecase.ErrGoldenSubmissionClosed)
	})

	t.Run("deadline equality is closed", func(t *testing.T) {
		localLedger, buildErr := goldenusecase.NewGoldenSubmissionLedger(scope, submissionTask049ID(10610))
		require.NoError(t, buildErr)
		local := submissionNewTask049SubmissionHarness(t, execution, localLedger, execution.Start.Deadline)
		verification := submissionTask049Verification(scope, execution, participants[0], 10620)
		local.verifications[verification.ID] = verification
		command := goldenusecase.GoldenSubmissionCommand{
			Scope: scope, CommandID: submissionTask049ID(10622), ActorParticipantID: participants[0],
			ParticipantID: participants[0], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: submissionTask049ID(10623),
		}

		result, localChanged, submitErr := goldenusecase.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, submitErr, goldenusecase.ErrGoldenSubmissionClosed)
		require.Empty(t, local.snapshot().Receipts)
		require.Equal(t, uint64(1), local.snapshot().NextSubmissionID)
	})

	t.Run("repository cannot return a receipt at the deadline", func(t *testing.T) {
		initial, buildErr := goldenusecase.NewGoldenSubmissionLedger(scope, submissionTask049ID(10670))
		require.NoError(t, buildErr)
		verification := submissionTask049Verification(scope, execution, participants[0], 10671)
		command := goldenusecase.GoldenSubmissionCommand{
			Scope: scope, CommandID: submissionTask049ID(10673), ActorParticipantID: participants[0],
			ParticipantID: participants[0], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: submissionTask049ID(10674),
		}
		canonical := submissionNewTask049SubmissionHarness(t, execution, initial, startedAt.Add(5*time.Second))
		canonical.verifications[verification.ID] = verification
		returned, returnedChanged, submitErr := goldenusecase.NewGoldenSubmissionUseCase(canonical).Submit(t.Context(), command)
		require.NoError(t, submitErr)
		require.True(t, returnedChanged)
		late := returned.Snapshot()
		late.Submissions[0].CommittedAt = execution.Start.Deadline
		late.Receipts[0].CommittedAt = execution.Start.Deadline
		submissionTask049SealSubmissionLedger(t, &late)
		require.NoError(t, late.Validate())

		malicious := submissionNewTask049SubmissionHarness(t, execution, initial, startedAt.Add(5*time.Second))
		malicious.verifications[verification.ID] = verification
		malicious.returnLedger = &late
		malicious.returnChanged = true
		result, localChanged, submitErr := goldenusecase.NewGoldenSubmissionUseCase(malicious).Submit(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, submitErr, domain.ErrInternal)
	})

	t.Run("verification drift after load consumes no receipt or sequence", func(t *testing.T) {
		localLedger, buildErr := goldenusecase.NewGoldenSubmissionLedger(scope, submissionTask049ID(10630))
		require.NoError(t, buildErr)
		local := submissionNewTask049SubmissionHarness(t, execution, localLedger, startedAt.Add(5*time.Second))
		verification := submissionTask049Verification(scope, execution, participants[0], 10640)
		local.verifications[verification.ID] = verification
		entered, signalEntered := submissionTask049Latch(t)
		release, signalRelease := submissionTask049Latch(t)
		local.beforeCommit = func() {
			signalEntered()
			submissionTask049AwaitSignal(t, release, "submission verification release")
		}
		command := goldenusecase.GoldenSubmissionCommand{
			Scope: scope, CommandID: submissionTask049ID(10642), ActorParticipantID: participants[0],
			ParticipantID: participants[0], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: submissionTask049ID(10643),
		}
		type result struct {
			ledger  *goldenusecase.GoldenSubmissionLedger
			changed bool
			err     error
		}
		finished := make(chan result, 1)
		go func() {
			value, changed, err := goldenusecase.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
			finished <- result{ledger: value, changed: changed, err: err}
		}()
		require.True(t, submissionTask049AwaitSignal(t, entered, "submission verification commit entry"))
		local.mu.Lock()
		changedVerification := local.verifications[verification.ID]
		changedVerification.RevisionID = submissionTask049ID(10644)
		changedVerification.EvidenceDigest = sha256.Sum256([]byte("replacement evidence"))
		local.verifications[verification.ID] = changedVerification
		local.mu.Unlock()
		signalRelease()

		got, received := submissionTask049AwaitValue(t, finished, "submission verification result")
		require.True(t, received)
		require.Nil(t, got.ledger)
		require.False(t, got.changed)
		require.ErrorIs(t, got.err, goldenusecase.ErrGoldenSubmissionAuthorityConflict)
		require.Empty(t, local.snapshot().Receipts)
		require.Equal(t, uint64(1), local.snapshot().NextSubmissionID)
	})

	t.Run("new identities cannot alias active execution authority", func(t *testing.T) {
		localLedger, buildErr := goldenusecase.NewGoldenSubmissionLedger(scope, submissionTask049ID(10650))
		require.NoError(t, buildErr)
		local := submissionNewTask049SubmissionHarness(t, execution, localLedger, startedAt.Add(5*time.Second))
		verification := submissionTask049Verification(scope, execution, participants[0], 10660)
		local.verifications[verification.ID] = verification
		command := goldenusecase.GoldenSubmissionCommand{
			Scope: scope, CommandID: submissionTask049ID(10662), ActorParticipantID: participants[0],
			ParticipantID: participants[0], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: scope.AssignmentID,
		}

		result, localChanged, submitErr := goldenusecase.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, submitErr, goldenusecase.ErrInvalidGoldenSubmission)
		require.Empty(t, local.snapshot().Receipts)

		localLedger, buildErr = goldenusecase.NewGoldenSubmissionLedger(scope, uuid.New())
		require.NoError(t, buildErr)
		local = submissionNewTask049SubmissionHarness(t, execution, localLedger, startedAt.Add(5*time.Second))
		verification = submissionTask049Verification(scope, execution, participants[0], 10710)
		verification.ID = execution.Wave.ID
		local.verifications[verification.ID] = verification
		command = goldenusecase.GoldenSubmissionCommand{
			Scope: scope, CommandID: submissionTask049ID(10712), ActorParticipantID: participants[0],
			ParticipantID: participants[0], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: submissionTask049ID(10713),
		}
		result, localChanged, submitErr = goldenusecase.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, submitErr, goldenusecase.ErrInvalidGoldenSubmission)
		require.Empty(t, local.snapshot().Receipts)
	})

	t.Run("new identities cannot alias the live verification revision", func(t *testing.T) {
		for _, aliasCommand := range []bool{true, false} {
			localLedger, buildErr := goldenusecase.NewGoldenSubmissionLedger(scope, uuid.New())
			require.NoError(t, buildErr)
			local := submissionNewTask049SubmissionHarness(t, execution, localLedger, startedAt.Add(5*time.Second))
			verification := submissionTask049Verification(scope, execution, participants[0], 10680)
			local.verifications[verification.ID] = verification
			command := goldenusecase.GoldenSubmissionCommand{
				Scope: scope, CommandID: submissionTask049ID(10682), ActorParticipantID: participants[0],
				ParticipantID: participants[0], VerificationID: verification.ID,
				ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: submissionTask049ID(10683),
			}
			if aliasCommand {
				command.CommandID = verification.RevisionID
			} else {
				command.NextLedgerRevisionID = verification.RevisionID
			}

			result, localChanged, submitErr := goldenusecase.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
			require.Nil(t, result)
			require.False(t, localChanged)
			require.ErrorIs(t, submitErr, goldenusecase.ErrInvalidGoldenSubmission)
			require.Empty(t, local.snapshot().Receipts)
		}

		localLedger, buildErr := goldenusecase.NewGoldenSubmissionLedger(scope, uuid.New())
		require.NoError(t, buildErr)
		local := submissionNewTask049SubmissionHarness(t, execution, localLedger, startedAt.Add(5*time.Second))
		verification := submissionTask049Verification(scope, execution, participants[0], 10690)
		verification.RevisionID = execution.RevisionID
		local.verifications[verification.ID] = verification
		command := goldenusecase.GoldenSubmissionCommand{
			Scope: scope, CommandID: submissionTask049ID(10692), ActorParticipantID: participants[0],
			ParticipantID: participants[0], VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: submissionTask049ID(10693),
		}
		result, localChanged, submitErr := goldenusecase.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, submitErr, goldenusecase.ErrInvalidGoldenSubmission)
		require.Empty(t, local.snapshot().Receipts)
	})

	t.Run("returns defensive snapshots and retains no raw flag or validator text", func(t *testing.T) {
		participantID := participants[0]
		localLedger, buildErr := goldenusecase.NewGoldenSubmissionLedger(scope, submissionTask049ID(10709))
		require.NoError(t, buildErr)
		local := submissionNewTask049SubmissionHarness(t, execution, localLedger, startedAt.Add(5*time.Second))
		verification := submissionTask049Verification(scope, execution, participantID, 10700)
		local.verifications[verification.ID] = verification
		command := goldenusecase.GoldenSubmissionCommand{
			Scope: scope, CommandID: submissionTask049ID(10702), ActorParticipantID: participantID,
			ParticipantID: participantID, VerificationID: verification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: submissionTask049ID(10703),
		}
		returned, localChanged, submitErr := goldenusecase.NewGoldenSubmissionUseCase(local).Submit(t.Context(), command)
		require.NoError(t, submitErr)
		require.True(t, localChanged)
		before := local.snapshot()
		returned.Submissions[0].ParticipantID = submissionTask049ID(10704)
		returned.Receipts[0].CommandID = submissionTask049ID(10705)
		after := local.snapshot()
		require.Equal(t, before, after)
		require.NotContains(t, submissionTask049Encode(t, after), execution.Assignment.Snapshot.Flag)
		require.NotContains(t, submissionTask049Encode(t, after), "validator internal detail")
	})
}
