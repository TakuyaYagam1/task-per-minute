package golden_test

import (
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenconnection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/connection"
	goldensubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"

	"github.com/stretchr/testify/require"
)

func TestGoldenIndividualDisconnectRejectsDishonestUnchangedCommit(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 1, 18, 45, 0, 0, time.UTC)
	execution := connectionTask049StartedExecution(t, startedAt)
	scope := connectionTask049SubmissionScope(execution)
	submissions, err := goldensubmission.NewGoldenSubmissionLedger(scope, connectionTask049ID(29300))
	require.NoError(t, err)
	connections, err := goldenconnection.NewGoldenIndividualConnectionLedger(
		scope, execution.Expectation(), submissions.Expectation(), execution.Start.StartedAt,
		execution.Start.Deadline, connectionTask049ID(29301), execution.Membership.ParticipantIDs,
	)
	require.NoError(t, err)
	participantID := execution.Membership.ParticipantIDs[0]
	command := goldenconnection.GoldenIndividualDisconnectCommand{
		Scope: scope, CommandID: connectionTask049ID(29302), ParticipantID: participantID,
		IntervalID: connectionTask049ID(29303), ExpectedExecution: execution.Expectation(),
		ExpectedSubmissions: submissions.Expectation(), ExpectedConnections: connections.Expectation(),
		NextConnectionRevisionID: connectionTask049ID(29304),
	}
	source := newTask050ConnectionRepositoryHarness(t, execution, submissions, connections)
	committed, changed, err := goldenconnection.NewGoldenIndividualDisconnectUseCase(
		source, connectionNewGoldenClock(t, startedAt.Add(5*time.Second)),
	).Disconnect(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, committed.Validate())

	t.Run("honest unchanged winner may have another timestamp", func(t *testing.T) {
		repository := newTask050ConnectionRepositoryHarness(t, execution, submissions, connections)
		repository.returnConnections = committed
		result, resultChanged, resultErr := goldenconnection.NewGoldenIndividualDisconnectUseCase(
			repository, connectionNewGoldenClock(t, startedAt.Add(6*time.Second)),
		).Disconnect(t.Context(), command)
		require.NoError(t, resultErr)
		require.False(t, resultChanged)
		require.Equal(t, committed.Expectation(), result.Expectation())
		require.Equal(t, 0, repository.writeCount())
	})

	tests := []struct {
		name   string
		mutate func(*goldenconnection.GoldenIndividualConnectionLedger)
	}{
		{name: "wrong scope", mutate: func(ledger *goldenconnection.GoldenIndividualConnectionLedger) {
			ledger.Scope.TaskID = connectionTask049ID(29310)
			ledger.Submissions.Scope = ledger.Scope
			ledger.Receipts[0].Expected.Scope = ledger.Scope
			ledger.Receipts[0].Expected.Submissions.Scope = ledger.Scope
			ledger.Receipts[0].ObservedSubmissions.Scope = ledger.Scope
			task050SealFirstExpectedConnectionHead(t, ledger)
		}},
		{name: "wrong participant", mutate: func(ledger *goldenconnection.GoldenIndividualConnectionLedger) {
			otherID := execution.Membership.ParticipantIDs[1]
			ledger.Intervals[0].ParticipantID = otherID
			ledger.Receipts[0].ParticipantID = otherID
			ledger.PresentParticipantIDs = ledger.PresentParticipantIDs[:0]
			for _, candidateID := range ledger.ParticipantIDs {
				if candidateID != otherID {
					ledger.PresentParticipantIDs = append(ledger.PresentParticipantIDs, candidateID)
				}
			}
			task050SealConnectionLedger(t, ledger)
		}},
		{name: "wrong interval", mutate: func(ledger *goldenconnection.GoldenIndividualConnectionLedger) {
			ledger.Intervals[0].ID = connectionTask049ID(29311)
			ledger.Receipts[0].IntervalID = connectionTask049ID(29311)
			task050SealConnectionLedger(t, ledger)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dishonest := committed.Snapshot()
			test.mutate(&dishonest)
			require.NoError(t, dishonest.Validate())
			repository := newTask050ConnectionRepositoryHarness(t, execution, submissions, connections)
			repository.returnConnections = &dishonest
			result, resultChanged, resultErr := goldenconnection.NewGoldenIndividualDisconnectUseCase(
				repository, connectionNewGoldenClock(t, startedAt.Add(6*time.Second)),
			).Disconnect(t.Context(), command)
			require.Nil(t, result)
			require.False(t, resultChanged)
			require.ErrorIs(t, resultErr, domain.ErrInternal)
			require.Equal(t, 0, repository.writeCount())
		})
	}
}
