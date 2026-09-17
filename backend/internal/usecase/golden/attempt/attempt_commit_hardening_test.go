package golden_test

import (
	"crypto/sha256"
	"math"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/attempt"
	goldensubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGoldenAttemptCommitHardening(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC)
	execution := attemptTask049StartedExecution(t, startedAt)
	scope := attemptTask049SubmissionScope(execution)
	submissions, err := goldensubmission.NewGoldenSubmissionLedger(scope, attemptTask049ID(11001))
	require.NoError(t, err)
	submissionRepository := attemptNewTask049SubmissionHarness(t, execution, submissions, startedAt.Add(5*time.Second))
	participantID := execution.Membership.ParticipantIDs[0]
	verification := attemptTask049Verification(scope, execution, participantID, 11100)
	submissionRepository.verifications[verification.ID] = verification
	submissionCommand := goldensubmission.GoldenSubmissionCommand{
		Scope: scope, CommandID: attemptTask049ID(11110), ActorParticipantID: participantID,
		ParticipantID: participantID, VerificationID: verification.ID,
		ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: attemptTask049ID(11111),
	}
	submitted, changed, err := goldensubmission.NewGoldenSubmissionUseCase(submissionRepository).Submit(t.Context(), submissionCommand)
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
	repository.archiveCurrent()
	repository.restoreCurrent()

	t.Run("commit identity cannot alias active attempt", func(t *testing.T) {
		localSubmission := attemptNewTask049SubmissionHarness(t, execution, *submitted, startedAt.Add(5*time.Second))
		localPositions, buildErr := goldenusecase.NewGoldenPositionLedger(
			execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, attemptTask049ID(11630),
		)
		require.NoError(t, buildErr)
		local := attemptNewTask049CommitHarness(t, localSubmission, *submitted, localPositions, sentinel)
		localCommand := command
		localCommand.CommandID = attemptTask049ID(11631)
		localCommand.CommitID = scope.AttemptID
		localCommand.NextPositionRevisionID = attemptTask049ID(11632)
		localCommand.ExpectedPositions = localPositions.Expectation()

		result, localChanged, commitErr := goldenusecase.NewGoldenAttemptCommitUseCase(
			local,
			attemptNewGoldenClock(t, execution.Start.Deadline),
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, goldenusecase.ErrInvalidGoldenAttemptCommit)
		require.Zero(t, local.commitCount())
	})

	t.Run("commit identity cannot alias historical submission authority", func(t *testing.T) {
		historicalPositions := record.Positions.Snapshot()
		historicalWaveID := attemptTask049ID(11633)
		historicalPositions.Attempts[0].SubmissionHead.Scope.WaveID = historicalWaveID
		attemptTask049SealOrdering(t, &historicalPositions.Attempts[0])
		attemptTask049SealPositionLedger(t, &historicalPositions)
		require.NoError(t, historicalPositions.Validate())

		localSubmission := attemptNewTask049SubmissionHarness(t, execution, *submitted, startedAt.Add(5*time.Second))
		local := attemptNewTask049CommitHarness(t, localSubmission, *submitted, historicalPositions, sentinel)
		localCommand := command
		localCommand.CommandID = attemptTask049ID(11634)
		localCommand.CommitID = historicalWaveID
		localCommand.NextPositionRevisionID = attemptTask049ID(11635)
		localCommand.ExpectedPositions = historicalPositions.Expectation()
		result, localChanged, commitErr := goldenusecase.NewGoldenAttemptCommitUseCase(
			local,
			attemptNewGoldenClock(t, execution.Start.Deadline),
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, goldenusecase.ErrInvalidGoldenAttemptCommit)
		require.Zero(t, local.commitCount())
	})

	t.Run("maximum position boundary is rejected without iteration", func(t *testing.T) {
		result, buildErr := goldenusecase.NewGoldenPositionLedger(
			execution.Scope,
			math.MaxInt,
			math.MaxInt,
			attemptTask049ID(11640),
		)
		require.ErrorIs(t, buildErr, goldenusecase.ErrInvalidGoldenAttemptCommit)
		require.Equal(t, goldenusecase.GoldenPositionLedger{}, result)
	})

	t.Run("position history retains gaps and every revision identity", func(t *testing.T) {
		withGap := record.Positions.Snapshot()
		withGap.Attempts[0].AttemptNo = 2
		withGap.Positions[0].AttemptNo = 2
		attemptTask049SealOrdering(t, &withGap.Attempts[0])
		attemptTask049SealPositionLedger(t, &withGap)
		require.NoError(t, withGap.Validate(), "cancelled pre-start attempts may precede the first settlement")

		advanced := record.Positions.Snapshot()
		secondAttemptID := attemptTask049ID(11641)
		emptyHead := submitted.Expectation()
		emptyHead.Scope.AttemptID = secondAttemptID
		emptyHead.RevisionID = attemptTask049ID(11642)
		emptyHead.Revision = 1
		emptyHead.NextSubmissionID = 1
		emptyHead.PayloadDigest = sha256.Sum256([]byte("empty second-attempt submission head"))
		emptyOrdering := goldenusecase.GoldenAttemptOrderingEvidence{
			AttemptID: secondAttemptID, AttemptNo: 2, SubmissionHead: emptyHead,
		}
		attemptTask049SealOrdering(t, &emptyOrdering)
		previousRevisionID := advanced.RevisionID
		advanced.PreviousRevisionID = &previousRevisionID
		advanced.RevisionID = attemptTask049ID(11643)
		advanced.Revision++
		advanced.RevisionIDs = append(advanced.RevisionIDs, advanced.RevisionID)
		advanced.Attempts = append(advanced.Attempts, emptyOrdering)
		attemptTask049SealPositionLedger(t, &advanced)
		require.NoError(t, advanced.Validate())

		emptySubmissions, submissionErr := goldensubmission.NewGoldenSubmissionLedger(scope, attemptTask049ID(11644))
		require.NoError(t, submissionErr)
		localSubmission := attemptNewTask049SubmissionHarness(t, execution, emptySubmissions, startedAt.Add(5*time.Second))
		local := attemptNewTask049CommitHarness(t, localSubmission, emptySubmissions, advanced, sentinel)
		localCommand := command
		localCommand.CommandID, localCommand.CommitID = attemptTask049ID(11645), attemptTask049ID(11646)
		localCommand.ExpectedSubmissions = emptySubmissions.Expectation()
		localCommand.ExpectedPositions = advanced.Expectation()
		localCommand.NextPositionRevisionID = advanced.RevisionIDs[0]
		result, localChanged, commitErr := goldenusecase.NewGoldenAttemptCommitUseCase(
			local,
			attemptNewGoldenClock(t, execution.Start.Deadline),
		).CommitAttempt(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, commitErr, goldenusecase.ErrInvalidGoldenAttemptCommit)
		require.Zero(t, local.commitCount())

		second := record.Snapshot()
		second.ID, second.CommandID = attemptTask049ID(11647), attemptTask049ID(11648)
		second.Scope.AttemptID = secondAttemptID
		second.ActiveExecution.AttemptID = secondAttemptID
		second.Assignment.AttemptID = secondAttemptID
		attemptTask049SealAttemptAssignmentEvidence(t, &second.Assignment)
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
		attemptTask049SealAttemptRecord(t, &second)
		require.NoError(t, second.Validate())

		forged := second.Snapshot()
		forgedDigest := sha256.Sum256([]byte("forged prior ordering prefix"))
		forged.Positions.Positions[0].EvidenceDigest = forgedDigest
		forged.Positions.Attempts[0].Order[0].EvidenceDigest = forgedDigest
		attemptTask049SealOrdering(t, &forged.Positions.Attempts[0])
		attemptTask049SealPositionLedger(t, &forged.Positions)
		attemptTask049SealAttemptRecord(t, &forged)
		require.ErrorIs(t, forged.Validate(), goldenusecase.ErrInvalidGoldenAttemptCommit)
	})

	t.Run("malformed terminal replay cannot satisfy a valid digest", func(t *testing.T) {
		clean := record.Snapshot()
		attemptTask049SealAttemptRecord(t, &clean)
		require.NoError(t, clean.Validate())
		cleanOrdering := record.Ordering.Snapshot()
		attemptTask049SealOrdering(t, &cleanOrdering)
		require.NoError(t, cleanOrdering.Validate())
		assertRejectedWithoutWrite := func(t *testing.T, malformed goldenusecase.GoldenAttemptCommitRecord) {
			t.Helper()
			localSubmission := attemptNewTask049SubmissionHarness(t, execution, *submitted, startedAt.Add(5*time.Second))
			local := attemptNewTask049CommitHarness(t, localSubmission, *submitted, positions, sentinel)
			local.returnRecord = &malformed
			local.returnChanged = false
			beforeCommits := local.commitCount()
			before := local.stateSnapshot()
			var result *goldenusecase.GoldenAttemptCommitRecord
			var changed bool
			var commitErr error
			require.NotPanics(t, func() {
				result, changed, commitErr = goldenusecase.NewGoldenAttemptCommitUseCase(
					local,
					attemptNewGoldenClock(t, execution.Start.Deadline),
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
			attemptTask049SealPositionLedger(t, &tampered.PriorPositions)
			tampered.ExpectedPositions = tampered.PriorPositions.Expectation()
			tampered.Positions.PositionTo++
			attemptTask049SealPositionLedger(t, &tampered.Positions)
			attemptTask049SealAttemptRecord(t, &tampered)
			require.NoError(t, tampered.PriorPositions.Validate())
			require.NoError(t, tampered.Positions.Validate())
			require.ErrorIs(t, tampered.Validate(), goldenusecase.ErrInvalidGoldenAttemptCommit)
			assertRejectedWithoutWrite(t, tampered)
		})

		t.Run("position evidence cannot name a foreign participant", func(t *testing.T) {
			tampered := record.Snapshot()
			foreignParticipantID := attemptTask049ID(11649)
			tampered.Ordering.Order[0].ParticipantID = foreignParticipantID
			attemptTask049SealOrdering(t, &tampered.Ordering)
			tampered.Positions.Positions[0].ParticipantID = foreignParticipantID
			tampered.Positions.Attempts[len(tampered.Positions.Attempts)-1] = tampered.Ordering.Snapshot()
			attemptTask049SealPositionLedger(t, &tampered.Positions)
			attemptTask049SealAttemptRecord(t, &tampered)
			require.NoError(t, tampered.Positions.Validate())
			require.ErrorIs(t, tampered.Validate(), goldenusecase.ErrInvalidGoldenAttemptCommit)
			assertRejectedWithoutWrite(t, tampered)
		})

		t.Run("over-capacity position evidence is panic-free", func(t *testing.T) {
			tampered := record.Snapshot()
			tampered.PriorPositions.PositionTo++
			attemptTask049SealPositionLedger(t, &tampered.PriorPositions)
			tampered.ExpectedPositions = tampered.PriorPositions.Expectation()
			tampered.Positions.PositionTo++
			foreignParticipantID := attemptTask049ID(11651)
			participants := []uuid.UUID{
				tampered.Group.Members[1].ParticipantID,
				tampered.Group.Members[2].ParticipantID,
				foreignParticipantID,
			}
			for index, participantID := range participants {
				submissionID := uint64(index + 2)
				evidenceDigest := sha256.Sum256([]byte{byte(index + 10)})
				tampered.Ordering.Order = append(tampered.Ordering.Order, goldenusecase.GoldenPositionOrderEntry{
					SubmissionID: submissionID, ParticipantID: participantID,
					CommittedAt:    tampered.Ordering.Order[0].CommittedAt.Add(time.Duration(index+1) * time.Nanosecond),
					EvidenceDigest: evidenceDigest,
				})
				tampered.Positions.Positions = append(tampered.Positions.Positions, goldenusecase.GoldenCommittedPosition{
					Position: tampered.Positions.PositionFrom + index + 1, ParticipantID: participantID,
					AttemptID: tampered.Attempt.ID, AttemptNo: tampered.Attempt.AttemptNo,
					SubmissionID: submissionID, EvidenceDigest: evidenceDigest, CommitID: tampered.ID,
				})
			}
			tampered.Ordering.SubmissionHead.NextSubmissionID = 5
			tampered.ExpectedSubmissions = tampered.Ordering.SubmissionHead
			attemptTask049SealOrdering(t, &tampered.Ordering)
			tampered.Positions.Attempts[len(tampered.Positions.Attempts)-1] = tampered.Ordering.Snapshot()
			attemptTask049SealPositionLedger(t, &tampered.Positions)
			attemptTask049SealAttemptRecord(t, &tampered)
			require.NoError(t, tampered.Positions.Validate())
			var validationErr error
			require.NotPanics(t, func() { validationErr = tampered.Validate() })
			require.ErrorIs(t, validationErr, goldenusecase.ErrInvalidGoldenAttemptCommit)
			assertRejectedWithoutWrite(t, tampered)
		})

		t.Run("group terminal attempt differs from record", func(t *testing.T) {
			tampered := record.Snapshot()
			last := len(tampered.Group.Attempts) - 1
			changedAt := tampered.Group.Attempts[last].FinishedAt.Add(time.Nanosecond)
			tampered.Group.Attempts[last].FinishedAt = &changedAt
			attemptTask049SealAttemptRecord(t, &tampered)
			require.ErrorIs(t, tampered.Validate(), goldenusecase.ErrInvalidGoldenAttemptCommit)
		})

		t.Run("active execution aliases another attempt", func(t *testing.T) {
			tampered := record.Snapshot()
			tampered.ActiveExecution.AttemptID = attemptTask049ID(11650)
			attemptTask049SealAttemptRecord(t, &tampered)
			require.ErrorIs(t, tampered.Validate(), goldenusecase.ErrInvalidGoldenAttemptCommit)
		})

		t.Run("private assignment aliases retained assignment authority", func(t *testing.T) {
			tampered := record.Snapshot()
			tampered.Assignment.Private[0].ID = tampered.Assignment.EdgeID
			attemptTask049SealAttemptAssignmentEvidence(t, &tampered.Assignment)
			attemptTask049SealAttemptRecord(t, &tampered)
			require.ErrorIs(t, tampered.Validate(), goldenusecase.ErrInvalidGoldenAttemptCommit)
		})

		t.Run("reserve assignment evidence is always the initial revision", func(t *testing.T) {
			tampered := record.Snapshot()
			tampered.Assignment.Revision = 2
			attemptTask049SealAttemptAssignmentEvidence(t, &tampered.Assignment)
			attemptTask049SealAttemptRecord(t, &tampered)
			require.ErrorIs(t, tampered.Validate(), goldenusecase.ErrInvalidGoldenAttemptCommit)
		})

		t.Run("ordering submission head is incomplete", func(t *testing.T) {
			tests := []struct {
				name   string
				mutate func(*goldensubmission.GoldenSubmissionLedgerExpectation)
			}{
				{name: "revision identity", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) {
					head.RevisionID = uuid.Nil
				}},
				{name: "revision", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) {
					head.Revision = 0
				}},
				{name: "next submission identity", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) {
					head.NextSubmissionID = 0
				}},
				{name: "missing ordered submission", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) {
					head.NextSubmissionID++
				}},
			}
			for _, testCase := range tests {
				t.Run(testCase.name, func(t *testing.T) {
					tampered := record.Ordering.Snapshot()
					testCase.mutate(&tampered.SubmissionHead)
					attemptTask049SealOrdering(t, &tampered)
					require.ErrorIs(t, tampered.Validate(), goldenusecase.ErrInvalidGoldenAttemptCommit)
				})
			}
		})
	})

	t.Run("returned and stored prior evidence are detached", func(t *testing.T) {
		before := repository.archivedSnapshot()
		record.Positions.Positions[0].ParticipantID = attemptTask049ID(11700)
		record.Ordering.Order[0].ParticipantID = attemptTask049ID(11701)
		after := repository.archivedSnapshot()
		require.Equal(t, before, after)
		tampered := after.Snapshot()
		tampered.Positions.Positions[0].ParticipantID = attemptTask049ID(11702)
		require.Error(t, tampered.Validate())
		require.NotContains(t, attemptTask049Encode(t, after), execution.Assignment.Snapshot.Flag)
	})
}
