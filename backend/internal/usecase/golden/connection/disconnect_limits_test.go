package golden_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenconnection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/connection"
	goldensubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGoldenIndividualDisconnectLimits(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 1, 18, 30, 0, 0, time.UTC)
	execution := connectionTask049StartedExecution(t, startedAt)
	scope := connectionTask049SubmissionScope(execution)
	submissions, err := goldensubmission.NewGoldenSubmissionLedger(scope, connectionTask049ID(29000))
	require.NoError(t, err)
	participantID := execution.Membership.ParticipantIDs[0]

	t.Run("same server timestamp reconnect", func(t *testing.T) {
		connections, buildErr := goldenconnection.NewGoldenIndividualConnectionLedger(
			scope, execution.Expectation(), submissions.Expectation(), execution.Start.StartedAt,
			execution.Start.Deadline, connectionTask049ID(29001), execution.Membership.ParticipantIDs,
		)
		require.NoError(t, buildErr)
		repository := newTask050ConnectionRepositoryHarness(t, execution, submissions, connections)
		occurredAt := startedAt.Add(5 * time.Second)
		disconnected, changed, disconnectErr := goldenconnection.NewGoldenIndividualDisconnectUseCase(
			repository, connectionNewGoldenClock(t, occurredAt),
		).Disconnect(t.Context(), goldenconnection.GoldenIndividualDisconnectCommand{
			Scope: scope, CommandID: connectionTask049ID(29002), ParticipantID: participantID,
			IntervalID: connectionTask049ID(29003), ExpectedExecution: execution.Expectation(),
			ExpectedSubmissions: submissions.Expectation(), ExpectedConnections: connections.Expectation(),
			NextConnectionRevisionID: connectionTask049ID(29004),
		})
		require.NoError(t, disconnectErr)
		require.True(t, changed)
		reconnected, reconnectChanged, reconnectErr := goldenconnection.NewGoldenIndividualDisconnectUseCase(
			repository, connectionNewGoldenClock(t, occurredAt),
		).Reconnect(t.Context(), goldenconnection.GoldenIndividualReconnectCommand{
			Scope: scope, CommandID: connectionTask049ID(29005), ActorParticipantID: participantID,
			ParticipantID: participantID, IntervalID: connectionTask049ID(29003),
			ExpectedExecution: execution.Expectation(), ExpectedSubmissions: submissions.Expectation(),
			ExpectedConnections: disconnected.Expectation(), NextConnectionRevisionID: connectionTask049ID(29006),
		})
		require.NoError(t, reconnectErr)
		require.True(t, reconnectChanged)
		require.Equal(t, occurredAt, *reconnected.Intervals[0].ReconnectedAt)
		require.NoError(t, reconnected.Validate())
	})

	t.Run("domain participant maximum", func(t *testing.T) {
		participantIDs := make([]uuid.UUID, domain.TournamentMaxParticipants+1)
		for index := range participantIDs {
			participantIDs[index] = connectionTask049ID(29020 + index)
		}
		connections, buildErr := goldenconnection.NewGoldenIndividualConnectionLedger(
			scope, execution.Expectation(), submissions.Expectation(), execution.Start.StartedAt,
			execution.Start.Deadline, connectionTask049ID(29050), participantIDs,
		)
		require.Error(t, buildErr)
		require.Equal(t, goldenconnection.GoldenIndividualConnectionLedger{}, connections)
	})

	t.Run("initial submission head validation", func(t *testing.T) {
		cases := []struct {
			name   string
			mutate func(*goldensubmission.GoldenSubmissionLedgerExpectation)
		}{
			{name: "nil revision ID", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) { head.RevisionID = uuid.Nil }},
			{name: "zero revision", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) { head.Revision = 0 }},
			{name: "zero next submission", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) { head.NextSubmissionID = 0 }},
			{name: "zero payload digest", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) {
				head.PayloadDigest = [sha256.Size]byte{}
			}},
		}
		for index, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				head := submissions.Expectation()
				test.mutate(&head)
				ledger, buildErr := goldenconnection.NewGoldenIndividualConnectionLedger(
					scope, execution.Expectation(), head, execution.Start.StartedAt,
					execution.Start.Deadline, connectionTask049ID(29052+index), execution.Membership.ParticipantIDs,
				)
				require.Error(t, buildErr)
				require.Equal(t, goldenconnection.GoldenIndividualConnectionLedger{}, ledger)
			})
		}
	})

	t.Run("durable ledger collection caps", func(t *testing.T) {
		connections, buildErr := goldenconnection.NewGoldenIndividualConnectionLedger(
			scope, execution.Expectation(), submissions.Expectation(), execution.Start.StartedAt,
			execution.Start.Deadline, connectionTask049ID(29051), execution.Membership.ParticipantIDs,
		)
		require.NoError(t, buildErr)
		tooManyIntervals := connections.Snapshot()
		tooManyIntervals.Intervals = make(
			[]goldenconnection.GoldenIndividualReconnectInterval,
			domain.TournamentMaxParticipants*domain.ReconnectCycleLimit+1,
		)
		require.Error(t, tooManyIntervals.Validate())
		tooManyReceipts := connections.Snapshot()
		tooManyReceipts.Receipts = make(
			[]goldenconnection.GoldenIndividualConnectionReceipt,
			domain.TournamentMaxParticipants*domain.ReconnectCycleLimit*2+1,
		)
		require.Error(t, tooManyReceipts.Validate())
	})

	t.Run("per participant reconnect cycle cap", func(t *testing.T) {
		connections, buildErr := goldenconnection.NewGoldenIndividualConnectionLedger(
			scope, execution.Expectation(), submissions.Expectation(), execution.Start.StartedAt,
			execution.Start.Deadline, connectionTask049ID(29060), execution.Membership.ParticipantIDs,
		)
		require.NoError(t, buildErr)
		repository := newTask050ConnectionRepositoryHarness(t, execution, submissions, connections)
		current := connections
		for cycle := range domain.ReconnectCycleLimit {
			base := 29100 + cycle*10
			disconnected, changed, disconnectErr := goldenconnection.NewGoldenIndividualDisconnectUseCase(
				repository, connectionNewGoldenClock(t, startedAt.Add(time.Duration(10+cycle*2)*time.Second)),
			).Disconnect(t.Context(), goldenconnection.GoldenIndividualDisconnectCommand{
				Scope: scope, CommandID: connectionTask049ID(base), ParticipantID: participantID,
				IntervalID: connectionTask049ID(base + 1), ExpectedExecution: execution.Expectation(),
				ExpectedSubmissions: submissions.Expectation(), ExpectedConnections: current.Expectation(),
				NextConnectionRevisionID: connectionTask049ID(base + 2),
			})
			require.NoError(t, disconnectErr)
			require.True(t, changed)
			reconnected, reconnectChanged, reconnectErr := goldenconnection.NewGoldenIndividualDisconnectUseCase(
				repository, connectionNewGoldenClock(t, startedAt.Add(time.Duration(11+cycle*2)*time.Second)),
			).Reconnect(t.Context(), goldenconnection.GoldenIndividualReconnectCommand{
				Scope: scope, CommandID: connectionTask049ID(base + 3), ActorParticipantID: participantID,
				ParticipantID: participantID, IntervalID: connectionTask049ID(base + 1),
				ExpectedExecution: execution.Expectation(), ExpectedSubmissions: submissions.Expectation(),
				ExpectedConnections: disconnected.Expectation(), NextConnectionRevisionID: connectionTask049ID(base + 4),
			})
			require.NoError(t, reconnectErr)
			require.True(t, reconnectChanged)
			current = *reconnected
		}
		beforeWrites := repository.writeCount()
		result, changed, limitErr := goldenconnection.NewGoldenIndividualDisconnectUseCase(
			repository, connectionNewGoldenClock(t, startedAt.Add(20*time.Second)),
		).Disconnect(t.Context(), goldenconnection.GoldenIndividualDisconnectCommand{
			Scope: scope, CommandID: connectionTask049ID(29150), ParticipantID: participantID,
			IntervalID: connectionTask049ID(29151), ExpectedExecution: execution.Expectation(),
			ExpectedSubmissions: submissions.Expectation(), ExpectedConnections: current.Expectation(),
			NextConnectionRevisionID: connectionTask049ID(29152),
		})
		require.Nil(t, result)
		require.False(t, changed)
		require.ErrorIs(t, limitErr, goldenconnection.ErrGoldenIndividualConnectionUnavailable)
		require.Equal(t, beforeWrites, repository.writeCount())
	})

	t.Run("observed submission head validation", func(t *testing.T) {
		initial, buildErr := goldensubmission.NewGoldenSubmissionLedger(scope, connectionTask049ID(29200))
		require.NoError(t, buildErr)
		verification := connectionTask049Verification(scope, execution, participantID, 29210)
		submissionRepository := connectionNewTask049SubmissionHarness(t, execution, initial, startedAt.Add(time.Second))
		submissionRepository.verifications[verification.ID] = verification
		advanced, advancedChanged, submitErr := goldensubmission.NewGoldenSubmissionUseCase(submissionRepository).Submit(
			t.Context(), goldensubmission.GoldenSubmissionCommand{
				Scope: scope, CommandID: connectionTask049ID(29220), ActorParticipantID: participantID,
				ParticipantID: participantID, VerificationID: verification.ID,
				ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: connectionTask049ID(29221),
			},
		)
		require.NoError(t, submitErr)
		require.True(t, advancedChanged)
		connections, connectionErr := goldenconnection.NewGoldenIndividualConnectionLedger(
			scope, execution.Expectation(), advanced.Expectation(), execution.Start.StartedAt,
			execution.Start.Deadline, connectionTask049ID(29201), execution.Membership.ParticipantIDs,
		)
		require.NoError(t, connectionErr)
		duplicateVerification := connectionTask049Verification(scope, execution, participantID, 29222)
		duplicateRepository := connectionNewTask049SubmissionHarness(t, execution, advanced.Snapshot(), startedAt.Add(2*time.Second))
		duplicateRepository.verifications[duplicateVerification.ID] = duplicateVerification
		later, laterChanged, duplicateErr := goldensubmission.NewGoldenSubmissionUseCase(duplicateRepository).Submit(
			t.Context(), goldensubmission.GoldenSubmissionCommand{
				Scope: scope, CommandID: connectionTask049ID(29224), ActorParticipantID: participantID,
				ParticipantID: participantID, VerificationID: duplicateVerification.ID,
				ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: connectionTask049ID(29225),
			},
		)
		require.NoError(t, duplicateErr)
		require.True(t, laterChanged)
		require.Equal(t, advanced.NextSubmissionID, later.NextSubmissionID)
		repository := newTask050ConnectionRepositoryHarness(t, execution, later.Snapshot(), connections)
		disconnected, changed, disconnectErr := goldenconnection.NewGoldenIndividualDisconnectUseCase(
			repository, connectionNewGoldenClock(t, startedAt.Add(3*time.Second)),
		).Disconnect(t.Context(), goldenconnection.GoldenIndividualDisconnectCommand{
			Scope: scope, CommandID: connectionTask049ID(29230), ParticipantID: participantID,
			IntervalID: connectionTask049ID(29231), ExpectedExecution: execution.Expectation(),
			ExpectedSubmissions: later.Expectation(), ExpectedConnections: connections.Expectation(),
			NextConnectionRevisionID: connectionTask049ID(29232),
		})
		require.NoError(t, disconnectErr)
		require.True(t, changed)
		require.NoError(t, disconnected.Validate())

		cases := []struct {
			name   string
			mutate func(*goldensubmission.GoldenSubmissionLedgerExpectation)
		}{
			{name: "nil revision ID", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) { head.RevisionID = uuid.Nil }},
			{name: "zero revision", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) { head.Revision = 0 }},
			{name: "zero next submission", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) { head.NextSubmissionID = 0 }},
			{name: "zero payload digest", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) {
				head.PayloadDigest = [sha256.Size]byte{}
			}},
			{name: "advanced revision reused ID", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) {
				head.RevisionID = connections.Submissions.RevisionID
			}},
			{name: "advanced head decreased next submission", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) {
				head.NextSubmissionID = connections.Submissions.NextSubmissionID - 1
			}},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				malformed := disconnected.Snapshot()
				test.mutate(&malformed.Receipts[0].ObservedSubmissions)
				malformed.Submissions = malformed.Receipts[0].ObservedSubmissions
				task050SealConnectionLedger(t, &malformed)
				require.Error(t, malformed.Validate())
			})
		}

		expectedCases := []struct {
			name   string
			mutate func(*goldensubmission.GoldenSubmissionLedgerExpectation)
		}{
			{name: "nil revision ID", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) { head.RevisionID = uuid.Nil }},
			{name: "zero revision", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) { head.Revision = 0 }},
			{name: "zero next submission", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) { head.NextSubmissionID = 0 }},
			{name: "zero payload digest", mutate: func(head *goldensubmission.GoldenSubmissionLedgerExpectation) {
				head.PayloadDigest = [sha256.Size]byte{}
			}},
		}
		for _, test := range expectedCases {
			t.Run("first expected "+test.name, func(t *testing.T) {
				malformed := disconnected.Snapshot()
				test.mutate(&malformed.Receipts[0].Expected.Submissions)
				task050SealFirstExpectedConnectionHead(t, &malformed)
				require.Error(t, malformed.Validate())
			})
		}
	})
}
