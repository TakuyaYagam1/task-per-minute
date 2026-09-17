package golden_test

import (
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenconnection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/connection"
	goldensubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"

	"github.com/stretchr/testify/require"
)

func TestGoldenIndividualDisconnect(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
	execution := connectionTask049StartedExecution(t, startedAt)
	scope := connectionTask049SubmissionScope(execution)
	submissions, err := goldensubmission.NewGoldenSubmissionLedger(scope, connectionTask049ID(20001))
	require.NoError(t, err)
	participantID := execution.Membership.ParticipantIDs[0]
	verification := connectionTask049Verification(scope, execution, participantID, 20010)
	submissionRepository := connectionNewTask049SubmissionHarness(t, execution, submissions, startedAt.Add(time.Second))
	submissionRepository.verifications[verification.ID] = verification
	submitted, changed, err := goldensubmission.NewGoldenSubmissionUseCase(submissionRepository).Submit(t.Context(), goldensubmission.GoldenSubmissionCommand{
		Scope: scope, CommandID: connectionTask049ID(20020), ActorParticipantID: participantID,
		ParticipantID: participantID, VerificationID: verification.ID,
		ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: connectionTask049ID(20021),
	})
	require.NoError(t, err)
	require.True(t, changed)

	connections, err := goldenconnection.NewGoldenIndividualConnectionLedger(
		scope,
		execution.Expectation(),
		submitted.Expectation(),
		execution.Start.StartedAt,
		execution.Start.Deadline,
		connectionTask049ID(20030),
		execution.Membership.ParticipantIDs,
	)
	require.NoError(t, err)
	repository := newTask050ConnectionRepositoryHarness(t, execution, submitted.Snapshot(), connections)
	disconnect := goldenconnection.GoldenIndividualDisconnectCommand{
		Scope: scope, CommandID: connectionTask049ID(20040), ParticipantID: participantID,
		IntervalID: connectionTask049ID(20041), ExpectedExecution: execution.Expectation(),
		ExpectedSubmissions: submitted.Expectation(), ExpectedConnections: connections.Expectation(),
		NextConnectionRevisionID: connectionTask049ID(20042),
	}

	disconnected, changed, err := goldenconnection.NewGoldenIndividualDisconnectUseCase(
		repository,
		connectionNewGoldenClock(t, startedAt.Add(5*time.Second)),
	).Disconnect(t.Context(), disconnect)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, disconnected.Validate())
	require.False(t, disconnected.IsPresent(participantID))
	require.Len(t, disconnected.Intervals, 1)
	require.Equal(t, execution.Start.Deadline, disconnected.Intervals[0].Deadline)
	require.Equal(t, execution.Expectation(), repository.executionSnapshot().Expectation())
	require.Equal(t, submitted.Expectation(), repository.submissionSnapshot().Expectation())
	require.Equal(t, submitted.ProvisionalOrder(), repository.submissionSnapshot().ProvisionalOrder())
	for _, otherID := range execution.Membership.ParticipantIDs[1:] {
		require.True(t, disconnected.IsPresent(otherID))
	}

	replayed, replayChanged, replayErr := goldenconnection.NewGoldenIndividualDisconnectUseCase(
		repository,
		connectionNewGoldenClock(t, execution.Start.Deadline.Add(time.Minute)),
	).Disconnect(t.Context(), disconnect)
	require.NoError(t, replayErr)
	require.False(t, replayChanged)
	require.Equal(t, disconnected.Expectation(), replayed.Expectation())

	peerID := execution.Membership.ParticipantIDs[1]
	peerVerification := connectionTask049Verification(scope, execution, peerID, 29960)
	peerSubmissions := connectionNewTask049SubmissionHarness(t, execution, submitted.Snapshot(), startedAt.Add(6*time.Second))
	peerSubmissions.verifications[peerVerification.ID] = peerVerification
	peerUpdated, peerChanged, peerErr := goldensubmission.NewGoldenSubmissionUseCase(peerSubmissions).Submit(
		t.Context(),
		goldensubmission.GoldenSubmissionCommand{
			Scope: scope, CommandID: connectionTask049ID(29970), ActorParticipantID: peerID,
			ParticipantID: peerID, VerificationID: peerVerification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: connectionTask049ID(29971),
		},
	)
	require.NoError(t, peerErr)
	require.True(t, peerChanged)
	repository.replaceSubmissions(peerUpdated.Snapshot())

	reconnect := goldenconnection.GoldenIndividualReconnectCommand{
		Scope: scope, CommandID: connectionTask049ID(20050), ActorParticipantID: participantID,
		ParticipantID: participantID, IntervalID: disconnect.IntervalID,
		ExpectedExecution: execution.Expectation(), ExpectedSubmissions: peerUpdated.Expectation(),
		ExpectedConnections: disconnected.Expectation(), NextConnectionRevisionID: connectionTask049ID(20051),
	}
	atDeadline, deadlineChanged, deadlineErr := goldenconnection.NewGoldenIndividualDisconnectUseCase(
		repository,
		connectionNewGoldenClock(t, execution.Start.Deadline),
	).Reconnect(t.Context(), reconnect)
	require.Nil(t, atDeadline)
	require.False(t, deadlineChanged)
	require.ErrorIs(t, deadlineErr, goldenconnection.ErrGoldenIndividualReconnectDeadline)

	reconnected, changed, err := goldenconnection.NewGoldenIndividualDisconnectUseCase(
		repository,
		connectionNewGoldenClock(t, execution.Start.Deadline.Add(-time.Nanosecond)),
	).Reconnect(t.Context(), reconnect)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, reconnected.IsPresent(participantID))
	require.Equal(t, execution.Start.Deadline, reconnected.Intervals[0].Deadline)
	require.Equal(t, execution.Start.Deadline.Add(-time.Nanosecond), *reconnected.Intervals[0].ReconnectedAt)
	require.Equal(t, peerUpdated.Expectation(), repository.submissionSnapshot().Expectation())
	require.Equal(t, peerUpdated.ProvisionalOrder(), repository.submissionSnapshot().ProvisionalOrder())
	require.Equal(t, peerUpdated.Expectation(), reconnected.Submissions)

	wrongActor := reconnect
	wrongActor.CommandID = connectionTask049ID(20060)
	wrongActor.ActorParticipantID = execution.Membership.ParticipantIDs[1]
	wrongActor.ExpectedConnections = reconnected.Expectation()
	wrongActor.NextConnectionRevisionID = connectionTask049ID(20061)
	result, wrongChanged, wrongErr := goldenconnection.NewGoldenIndividualDisconnectUseCase(
		repository,
		connectionNewGoldenClock(t, execution.Start.Deadline.Add(-time.Second)),
	).Reconnect(t.Context(), wrongActor)
	require.Nil(t, result)
	require.False(t, wrongChanged)
	require.ErrorIs(t, wrongErr, domain.ErrAssignmentParticipant)

	disconnected.PresentParticipantIDs[0] = connectionTask049ID(20999)
	stored := repository.connectionSnapshot()
	require.NotEqual(t, connectionTask049ID(20999), stored.PresentParticipantIDs[0])

	concurrentConnections, err := goldenconnection.NewGoldenIndividualConnectionLedger(
		scope, execution.Expectation(), peerUpdated.Expectation(), execution.Start.StartedAt,
		execution.Start.Deadline, connectionTask049ID(28000), execution.Membership.ParticipantIDs,
	)
	require.NoError(t, err)
	concurrentRepository := newTask050ConnectionRepositoryHarness(t, execution, peerUpdated.Snapshot(), concurrentConnections)
	concurrentRepository.beforeCommit = connectionTask049TwoPartyBarrier(t)
	concurrentCommand := goldenconnection.GoldenIndividualDisconnectCommand{
		Scope: scope, CommandID: connectionTask049ID(28001), ParticipantID: peerID, IntervalID: connectionTask049ID(28002),
		ExpectedExecution: execution.Expectation(), ExpectedSubmissions: peerUpdated.Expectation(),
		ExpectedConnections: concurrentConnections.Expectation(), NextConnectionRevisionID: connectionTask049ID(28003),
	}
	type concurrentResult struct {
		ledger  *goldenconnection.GoldenIndividualConnectionLedger
		changed bool
		err     error
	}
	clock := connectionNewGoldenClock(t, startedAt.Add(7*time.Second))
	results := make(chan concurrentResult, 2)
	for range 2 {
		go func() {
			ledger, changed, err := goldenconnection.NewGoldenIndividualDisconnectUseCase(
				concurrentRepository, clock,
			).Disconnect(t.Context(), concurrentCommand)
			results <- concurrentResult{ledger: ledger, changed: changed, err: err}
		}()
	}
	changedCount := 0
	for range 2 {
		result := <-results
		require.NoError(t, result.err)
		require.NotNil(t, result.ledger)
		if result.changed {
			changedCount++
		}
	}
	require.Equal(t, 1, changedCount)
}
