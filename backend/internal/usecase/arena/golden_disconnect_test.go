package arena_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenIndividualDisconnect(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
	execution := task049StartedExecution(t, startedAt)
	scope := task049SubmissionScope(execution)
	submissions, err := arena.NewGoldenSubmissionLedger(scope, task049ID(20001))
	require.NoError(t, err)
	participantID := execution.Membership.ParticipantIDs[0]
	verification := task049Verification(scope, execution, participantID, 20010)
	submissionRepository := newTask049SubmissionRepository(execution, submissions, startedAt.Add(time.Second))
	submissionRepository.verifications[verification.ID] = verification
	submitted, changed, err := arena.NewGoldenSubmissionUseCase(submissionRepository).Submit(t.Context(), arena.GoldenSubmissionCommand{
		Scope: scope, CommandID: task049ID(20020), ActorParticipantID: participantID,
		ParticipantID: participantID, VerificationID: verification.ID,
		ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(20021),
	})
	require.NoError(t, err)
	require.True(t, changed)

	connections, err := arena.NewGoldenIndividualConnectionLedger(
		scope,
		execution.Expectation(),
		submitted.Expectation(),
		execution.Start.StartedAt,
		execution.Start.Deadline,
		task049ID(20030),
		execution.Membership.ParticipantIDs,
	)
	require.NoError(t, err)
	repository := newTask050ConnectionRepository(execution, submitted.Snapshot(), connections)
	disconnect := arena.GoldenIndividualDisconnectCommand{
		Scope: scope, CommandID: task049ID(20040), ParticipantID: participantID,
		IntervalID: task049ID(20041), ExpectedExecution: execution.Expectation(),
		ExpectedSubmissions: submitted.Expectation(), ExpectedConnections: connections.Expectation(),
		NextConnectionRevisionID: task049ID(20042),
	}

	disconnected, changed, err := arena.NewGoldenIndividualDisconnectUseCase(
		repository,
		fixedArenaClock{now: startedAt.Add(5 * time.Second)},
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

	replayed, replayChanged, replayErr := arena.NewGoldenIndividualDisconnectUseCase(
		repository,
		fixedArenaClock{now: execution.Start.Deadline.Add(time.Minute)},
	).Disconnect(t.Context(), disconnect)
	require.NoError(t, replayErr)
	require.False(t, replayChanged)
	require.Equal(t, disconnected.Expectation(), replayed.Expectation())

	peerID := execution.Membership.ParticipantIDs[1]
	peerVerification := task049Verification(scope, execution, peerID, 29960)
	peerSubmissions := newTask049SubmissionRepository(execution, submitted.Snapshot(), startedAt.Add(6*time.Second))
	peerSubmissions.verifications[peerVerification.ID] = peerVerification
	peerUpdated, peerChanged, peerErr := arena.NewGoldenSubmissionUseCase(peerSubmissions).Submit(
		t.Context(),
		arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(29970), ActorParticipantID: peerID,
			ParticipantID: peerID, VerificationID: peerVerification.ID,
			ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(29971),
		},
	)
	require.NoError(t, peerErr)
	require.True(t, peerChanged)
	repository.replaceSubmissions(peerUpdated.Snapshot())

	reconnect := arena.GoldenIndividualReconnectCommand{
		Scope: scope, CommandID: task049ID(20050), ActorParticipantID: participantID,
		ParticipantID: participantID, IntervalID: disconnect.IntervalID,
		ExpectedExecution: execution.Expectation(), ExpectedSubmissions: peerUpdated.Expectation(),
		ExpectedConnections: disconnected.Expectation(), NextConnectionRevisionID: task049ID(20051),
	}
	atDeadline, deadlineChanged, deadlineErr := arena.NewGoldenIndividualDisconnectUseCase(
		repository,
		fixedArenaClock{now: execution.Start.Deadline},
	).Reconnect(t.Context(), reconnect)
	require.Nil(t, atDeadline)
	require.False(t, deadlineChanged)
	require.ErrorIs(t, deadlineErr, arena.ErrGoldenIndividualReconnectDeadline)

	reconnected, changed, err := arena.NewGoldenIndividualDisconnectUseCase(
		repository,
		fixedArenaClock{now: execution.Start.Deadline.Add(-time.Nanosecond)},
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
	wrongActor.CommandID = task049ID(20060)
	wrongActor.ActorParticipantID = execution.Membership.ParticipantIDs[1]
	wrongActor.ExpectedConnections = reconnected.Expectation()
	wrongActor.NextConnectionRevisionID = task049ID(20061)
	result, wrongChanged, wrongErr := arena.NewGoldenIndividualDisconnectUseCase(
		repository,
		fixedArenaClock{now: execution.Start.Deadline.Add(-time.Second)},
	).Reconnect(t.Context(), wrongActor)
	require.Nil(t, result)
	require.False(t, wrongChanged)
	require.ErrorIs(t, wrongErr, domain.ErrArenaAssignmentParticipant)

	disconnected.PresentParticipantIDs[0] = task049ID(20999)
	stored := repository.connectionSnapshot()
	require.NotEqual(t, task049ID(20999), stored.PresentParticipantIDs[0])

	concurrentConnections, err := arena.NewGoldenIndividualConnectionLedger(
		scope, execution.Expectation(), peerUpdated.Expectation(), execution.Start.StartedAt,
		execution.Start.Deadline, task049ID(28000), execution.Membership.ParticipantIDs,
	)
	require.NoError(t, err)
	concurrentRepository := newTask050ConnectionRepository(execution, peerUpdated.Snapshot(), concurrentConnections)
	concurrentRepository.beforeCommit = task049TwoPartyBarrier(t)
	concurrentCommand := arena.GoldenIndividualDisconnectCommand{
		Scope: scope, CommandID: task049ID(28001), ParticipantID: peerID, IntervalID: task049ID(28002),
		ExpectedExecution: execution.Expectation(), ExpectedSubmissions: peerUpdated.Expectation(),
		ExpectedConnections: concurrentConnections.Expectation(), NextConnectionRevisionID: task049ID(28003),
	}
	type concurrentResult struct {
		ledger  *arena.GoldenIndividualConnectionLedger
		changed bool
		err     error
	}
	results := make(chan concurrentResult, 2)
	for range 2 {
		go func() {
			ledger, changed, err := arena.NewGoldenIndividualDisconnectUseCase(
				concurrentRepository, fixedArenaClock{now: startedAt.Add(7 * time.Second)},
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

func TestGoldenIndividualDisconnectLimits(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 1, 18, 30, 0, 0, time.UTC)
	execution := task049StartedExecution(t, startedAt)
	scope := task049SubmissionScope(execution)
	submissions, err := arena.NewGoldenSubmissionLedger(scope, task049ID(29000))
	require.NoError(t, err)
	participantID := execution.Membership.ParticipantIDs[0]

	t.Run("same server timestamp reconnect", func(t *testing.T) {
		connections, buildErr := arena.NewGoldenIndividualConnectionLedger(
			scope, execution.Expectation(), submissions.Expectation(), execution.Start.StartedAt,
			execution.Start.Deadline, task049ID(29001), execution.Membership.ParticipantIDs,
		)
		require.NoError(t, buildErr)
		repository := newTask050ConnectionRepository(execution, submissions, connections)
		occurredAt := startedAt.Add(5 * time.Second)
		disconnected, changed, disconnectErr := arena.NewGoldenIndividualDisconnectUseCase(
			repository, fixedArenaClock{now: occurredAt},
		).Disconnect(t.Context(), arena.GoldenIndividualDisconnectCommand{
			Scope: scope, CommandID: task049ID(29002), ParticipantID: participantID,
			IntervalID: task049ID(29003), ExpectedExecution: execution.Expectation(),
			ExpectedSubmissions: submissions.Expectation(), ExpectedConnections: connections.Expectation(),
			NextConnectionRevisionID: task049ID(29004),
		})
		require.NoError(t, disconnectErr)
		require.True(t, changed)
		reconnected, reconnectChanged, reconnectErr := arena.NewGoldenIndividualDisconnectUseCase(
			repository, fixedArenaClock{now: occurredAt},
		).Reconnect(t.Context(), arena.GoldenIndividualReconnectCommand{
			Scope: scope, CommandID: task049ID(29005), ActorParticipantID: participantID,
			ParticipantID: participantID, IntervalID: task049ID(29003),
			ExpectedExecution: execution.Expectation(), ExpectedSubmissions: submissions.Expectation(),
			ExpectedConnections: disconnected.Expectation(), NextConnectionRevisionID: task049ID(29006),
		})
		require.NoError(t, reconnectErr)
		require.True(t, reconnectChanged)
		require.Equal(t, occurredAt, *reconnected.Intervals[0].ReconnectedAt)
		require.NoError(t, reconnected.Validate())
	})

	t.Run("domain participant maximum", func(t *testing.T) {
		participantIDs := make([]uuid.UUID, domain.ArenaMaxParticipants+1)
		for index := range participantIDs {
			participantIDs[index] = task049ID(29020 + index)
		}
		connections, buildErr := arena.NewGoldenIndividualConnectionLedger(
			scope, execution.Expectation(), submissions.Expectation(), execution.Start.StartedAt,
			execution.Start.Deadline, task049ID(29050), participantIDs,
		)
		require.Error(t, buildErr)
		require.Equal(t, arena.GoldenIndividualConnectionLedger{}, connections)
	})

	t.Run("initial submission head validation", func(t *testing.T) {
		cases := []struct {
			name   string
			mutate func(*arena.GoldenSubmissionLedgerExpectation)
		}{
			{name: "nil revision ID", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) { head.RevisionID = uuid.Nil }},
			{name: "zero revision", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) { head.Revision = 0 }},
			{name: "zero next submission", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) { head.NextSubmissionID = 0 }},
			{name: "zero payload digest", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) { head.PayloadDigest = [sha256.Size]byte{} }},
		}
		for index, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				head := submissions.Expectation()
				test.mutate(&head)
				ledger, buildErr := arena.NewGoldenIndividualConnectionLedger(
					scope, execution.Expectation(), head, execution.Start.StartedAt,
					execution.Start.Deadline, task049ID(29052+index), execution.Membership.ParticipantIDs,
				)
				require.Error(t, buildErr)
				require.Equal(t, arena.GoldenIndividualConnectionLedger{}, ledger)
			})
		}
	})

	t.Run("durable ledger collection caps", func(t *testing.T) {
		connections, buildErr := arena.NewGoldenIndividualConnectionLedger(
			scope, execution.Expectation(), submissions.Expectation(), execution.Start.StartedAt,
			execution.Start.Deadline, task049ID(29051), execution.Membership.ParticipantIDs,
		)
		require.NoError(t, buildErr)
		tooManyIntervals := connections.Snapshot()
		tooManyIntervals.Intervals = make(
			[]arena.GoldenIndividualReconnectInterval,
			domain.ArenaMaxParticipants*arena.ReconnectCycleLimit+1,
		)
		require.Error(t, tooManyIntervals.Validate())
		tooManyReceipts := connections.Snapshot()
		tooManyReceipts.Receipts = make(
			[]arena.GoldenIndividualConnectionReceipt,
			domain.ArenaMaxParticipants*arena.ReconnectCycleLimit*2+1,
		)
		require.Error(t, tooManyReceipts.Validate())
	})

	t.Run("per participant reconnect cycle cap", func(t *testing.T) {
		connections, buildErr := arena.NewGoldenIndividualConnectionLedger(
			scope, execution.Expectation(), submissions.Expectation(), execution.Start.StartedAt,
			execution.Start.Deadline, task049ID(29060), execution.Membership.ParticipantIDs,
		)
		require.NoError(t, buildErr)
		repository := newTask050ConnectionRepository(execution, submissions, connections)
		current := connections
		for cycle := range arena.ReconnectCycleLimit {
			base := 29100 + cycle*10
			disconnected, changed, disconnectErr := arena.NewGoldenIndividualDisconnectUseCase(
				repository, fixedArenaClock{now: startedAt.Add(time.Duration(10+cycle*2) * time.Second)},
			).Disconnect(t.Context(), arena.GoldenIndividualDisconnectCommand{
				Scope: scope, CommandID: task049ID(base), ParticipantID: participantID,
				IntervalID: task049ID(base + 1), ExpectedExecution: execution.Expectation(),
				ExpectedSubmissions: submissions.Expectation(), ExpectedConnections: current.Expectation(),
				NextConnectionRevisionID: task049ID(base + 2),
			})
			require.NoError(t, disconnectErr)
			require.True(t, changed)
			reconnected, reconnectChanged, reconnectErr := arena.NewGoldenIndividualDisconnectUseCase(
				repository, fixedArenaClock{now: startedAt.Add(time.Duration(11+cycle*2) * time.Second)},
			).Reconnect(t.Context(), arena.GoldenIndividualReconnectCommand{
				Scope: scope, CommandID: task049ID(base + 3), ActorParticipantID: participantID,
				ParticipantID: participantID, IntervalID: task049ID(base + 1),
				ExpectedExecution: execution.Expectation(), ExpectedSubmissions: submissions.Expectation(),
				ExpectedConnections: disconnected.Expectation(), NextConnectionRevisionID: task049ID(base + 4),
			})
			require.NoError(t, reconnectErr)
			require.True(t, reconnectChanged)
			current = *reconnected
		}
		beforeWrites := repository.writeCount()
		result, changed, limitErr := arena.NewGoldenIndividualDisconnectUseCase(
			repository, fixedArenaClock{now: startedAt.Add(20 * time.Second)},
		).Disconnect(t.Context(), arena.GoldenIndividualDisconnectCommand{
			Scope: scope, CommandID: task049ID(29150), ParticipantID: participantID,
			IntervalID: task049ID(29151), ExpectedExecution: execution.Expectation(),
			ExpectedSubmissions: submissions.Expectation(), ExpectedConnections: current.Expectation(),
			NextConnectionRevisionID: task049ID(29152),
		})
		require.Nil(t, result)
		require.False(t, changed)
		require.ErrorIs(t, limitErr, arena.ErrGoldenIndividualConnectionUnavailable)
		require.Equal(t, beforeWrites, repository.writeCount())
	})

	t.Run("observed submission head validation", func(t *testing.T) {
		initial, buildErr := arena.NewGoldenSubmissionLedger(scope, task049ID(29200))
		require.NoError(t, buildErr)
		verification := task049Verification(scope, execution, participantID, 29210)
		submissionRepository := newTask049SubmissionRepository(execution, initial, startedAt.Add(time.Second))
		submissionRepository.verifications[verification.ID] = verification
		advanced, advancedChanged, submitErr := arena.NewGoldenSubmissionUseCase(submissionRepository).Submit(
			t.Context(), arena.GoldenSubmissionCommand{
				Scope: scope, CommandID: task049ID(29220), ActorParticipantID: participantID,
				ParticipantID: participantID, VerificationID: verification.ID,
				ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(29221),
			},
		)
		require.NoError(t, submitErr)
		require.True(t, advancedChanged)
		connections, connectionErr := arena.NewGoldenIndividualConnectionLedger(
			scope, execution.Expectation(), advanced.Expectation(), execution.Start.StartedAt,
			execution.Start.Deadline, task049ID(29201), execution.Membership.ParticipantIDs,
		)
		require.NoError(t, connectionErr)
		duplicateVerification := task049Verification(scope, execution, participantID, 29222)
		duplicateRepository := newTask049SubmissionRepository(execution, advanced.Snapshot(), startedAt.Add(2*time.Second))
		duplicateRepository.verifications[duplicateVerification.ID] = duplicateVerification
		later, laterChanged, duplicateErr := arena.NewGoldenSubmissionUseCase(duplicateRepository).Submit(
			t.Context(), arena.GoldenSubmissionCommand{
				Scope: scope, CommandID: task049ID(29224), ActorParticipantID: participantID,
				ParticipantID: participantID, VerificationID: duplicateVerification.ID,
				ExpectedExecution: execution.Expectation(), NextLedgerRevisionID: task049ID(29225),
			},
		)
		require.NoError(t, duplicateErr)
		require.True(t, laterChanged)
		require.Equal(t, advanced.NextSubmissionID, later.NextSubmissionID)
		repository := newTask050ConnectionRepository(execution, later.Snapshot(), connections)
		disconnected, changed, disconnectErr := arena.NewGoldenIndividualDisconnectUseCase(
			repository, fixedArenaClock{now: startedAt.Add(3 * time.Second)},
		).Disconnect(t.Context(), arena.GoldenIndividualDisconnectCommand{
			Scope: scope, CommandID: task049ID(29230), ParticipantID: participantID,
			IntervalID: task049ID(29231), ExpectedExecution: execution.Expectation(),
			ExpectedSubmissions: later.Expectation(), ExpectedConnections: connections.Expectation(),
			NextConnectionRevisionID: task049ID(29232),
		})
		require.NoError(t, disconnectErr)
		require.True(t, changed)
		require.NoError(t, disconnected.Validate())

		cases := []struct {
			name   string
			mutate func(*arena.GoldenSubmissionLedgerExpectation)
		}{
			{name: "nil revision ID", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) { head.RevisionID = uuid.Nil }},
			{name: "zero revision", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) { head.Revision = 0 }},
			{name: "zero next submission", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) { head.NextSubmissionID = 0 }},
			{name: "zero payload digest", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) { head.PayloadDigest = [sha256.Size]byte{} }},
			{name: "advanced revision reused ID", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) {
				head.RevisionID = connections.Submissions.RevisionID
			}},
			{name: "advanced head decreased next submission", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) {
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
			mutate func(*arena.GoldenSubmissionLedgerExpectation)
		}{
			{name: "nil revision ID", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) { head.RevisionID = uuid.Nil }},
			{name: "zero revision", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) { head.Revision = 0 }},
			{name: "zero next submission", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) { head.NextSubmissionID = 0 }},
			{name: "zero payload digest", mutate: func(head *arena.GoldenSubmissionLedgerExpectation) { head.PayloadDigest = [sha256.Size]byte{} }},
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

func TestGoldenIndividualDisconnectRejectsDishonestUnchangedCommit(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 1, 18, 45, 0, 0, time.UTC)
	execution := task049StartedExecution(t, startedAt)
	scope := task049SubmissionScope(execution)
	submissions, err := arena.NewGoldenSubmissionLedger(scope, task049ID(29300))
	require.NoError(t, err)
	connections, err := arena.NewGoldenIndividualConnectionLedger(
		scope, execution.Expectation(), submissions.Expectation(), execution.Start.StartedAt,
		execution.Start.Deadline, task049ID(29301), execution.Membership.ParticipantIDs,
	)
	require.NoError(t, err)
	participantID := execution.Membership.ParticipantIDs[0]
	command := arena.GoldenIndividualDisconnectCommand{
		Scope: scope, CommandID: task049ID(29302), ParticipantID: participantID,
		IntervalID: task049ID(29303), ExpectedExecution: execution.Expectation(),
		ExpectedSubmissions: submissions.Expectation(), ExpectedConnections: connections.Expectation(),
		NextConnectionRevisionID: task049ID(29304),
	}
	source := newTask050ConnectionRepository(execution, submissions, connections)
	committed, changed, err := arena.NewGoldenIndividualDisconnectUseCase(
		source, fixedArenaClock{now: startedAt.Add(5 * time.Second)},
	).Disconnect(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, committed.Validate())

	t.Run("honest unchanged winner may have another timestamp", func(t *testing.T) {
		repository := newTask050ConnectionRepository(execution, submissions, connections)
		repository.returnConnections = committed
		result, resultChanged, resultErr := arena.NewGoldenIndividualDisconnectUseCase(
			repository, fixedArenaClock{now: startedAt.Add(6 * time.Second)},
		).Disconnect(t.Context(), command)
		require.NoError(t, resultErr)
		require.False(t, resultChanged)
		require.Equal(t, committed.Expectation(), result.Expectation())
		require.Equal(t, 0, repository.writeCount())
	})

	tests := []struct {
		name   string
		mutate func(*arena.GoldenIndividualConnectionLedger)
	}{
		{name: "wrong scope", mutate: func(ledger *arena.GoldenIndividualConnectionLedger) {
			ledger.Scope.TaskID = task049ID(29310)
			ledger.Submissions.Scope = ledger.Scope
			ledger.Receipts[0].Expected.Scope = ledger.Scope
			ledger.Receipts[0].Expected.Submissions.Scope = ledger.Scope
			ledger.Receipts[0].ObservedSubmissions.Scope = ledger.Scope
			task050SealFirstExpectedConnectionHead(t, ledger)
		}},
		{name: "wrong participant", mutate: func(ledger *arena.GoldenIndividualConnectionLedger) {
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
		{name: "wrong interval", mutate: func(ledger *arena.GoldenIndividualConnectionLedger) {
			ledger.Intervals[0].ID = task049ID(29311)
			ledger.Receipts[0].IntervalID = task049ID(29311)
			task050SealConnectionLedger(t, ledger)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dishonest := committed.Snapshot()
			test.mutate(&dishonest)
			require.NoError(t, dishonest.Validate())
			repository := newTask050ConnectionRepository(execution, submissions, connections)
			repository.returnConnections = &dishonest
			result, resultChanged, resultErr := arena.NewGoldenIndividualDisconnectUseCase(
				repository, fixedArenaClock{now: startedAt.Add(6 * time.Second)},
			).Disconnect(t.Context(), command)
			require.Nil(t, result)
			require.False(t, resultChanged)
			require.ErrorIs(t, resultErr, domain.ErrInternal)
			require.Equal(t, 0, repository.writeCount())
		})
	}
}

func task050SealConnectionLedger(t *testing.T, ledger *arena.GoldenIndividualConnectionLedger) {
	t.Helper()
	clone := ledger.Snapshot()
	clone.PayloadDigest = [sha256.Size]byte{}
	var payload bytes.Buffer
	require.NoError(t, gob.NewEncoder(&payload).Encode(clone))
	ledger.PayloadDigest = sha256.Sum256(payload.Bytes())
}

func task050SealFirstExpectedConnectionHead(
	t *testing.T,
	ledger *arena.GoldenIndividualConnectionLedger,
) {
	t.Helper()
	receipt := &ledger.Receipts[0]
	initial := arena.GoldenIndividualConnectionLedger{
		Scope: ledger.Scope, Execution: ledger.Execution,
		Submissions: receipt.Expected.Submissions, StartedAt: ledger.StartedAt, Deadline: ledger.Deadline,
		RevisionID: receipt.Expected.RevisionID, Revision: receipt.Expected.Revision,
		ParticipantIDs:        append([]uuid.UUID(nil), ledger.ParticipantIDs...),
		PresentParticipantIDs: append([]uuid.UUID(nil), ledger.ParticipantIDs...),
	}
	task050SealConnectionLedger(t, &initial)
	receipt.Expected.PayloadDigest = initial.PayloadDigest
	task050SealConnectionLedger(t, ledger)
}

type task050ConnectionRepository struct {
	mu                sync.Mutex
	execution         arena.GoldenWaveExecution
	submissions       arena.GoldenSubmissionLedger
	connections       arena.GoldenIndividualConnectionLedger
	replays           map[uuid.UUID]arena.GoldenIndividualConnectionReplay
	beforeCommit      func()
	returnConnections *arena.GoldenIndividualConnectionLedger
	writes            int
}

func newTask050ConnectionRepository(
	execution arena.GoldenWaveExecution,
	submissions arena.GoldenSubmissionLedger,
	connections arena.GoldenIndividualConnectionLedger,
) *task050ConnectionRepository {
	return &task050ConnectionRepository{
		execution: execution.Snapshot(), submissions: submissions.Snapshot(), connections: connections.Snapshot(),
		replays: make(map[uuid.UUID]arena.GoldenIndividualConnectionReplay),
	}
}

func (r *task050ConnectionRepository) FindGoldenIndividualConnectionCommand(
	_ context.Context,
	_ uuid.UUID,
	commandID uuid.UUID,
) (*arena.GoldenIndividualConnectionReplay, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	replay, found := r.replays[commandID]
	if !found {
		return nil, nil
	}
	clone := replay.Snapshot()
	return &clone, nil
}

func (r *task050ConnectionRepository) LoadGoldenIndividualDisconnectAuthority(
	_ context.Context,
	_ arena.GoldenSubmissionScope,
) (arena.GoldenIndividualDisconnectAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return arena.GoldenIndividualDisconnectAuthority{
		Execution: r.execution.Snapshot(), Submissions: r.submissions.Snapshot(),
		Connections: r.connections.Snapshot(),
	}, nil
}

func (r *task050ConnectionRepository) CommitGoldenIndividualConnection(
	_ context.Context,
	commit arena.GoldenIndividualConnectionCommit,
) (*arena.GoldenIndividualConnectionLedger, bool, error) {
	if r.beforeCommit != nil {
		r.beforeCommit()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.execution.Expectation().Equal(commit.ExpectedExecution) ||
		!r.submissions.Expectation().Equal(commit.ExpectedSubmissions) ||
		!r.connections.Expectation().Equal(commit.ExpectedConnections) {
		return nil, false, domain.ErrConflict
	}
	last := commit.Next.Receipts[len(commit.Next.Receipts)-1]
	if _, exists := r.replays[last.CommandID]; exists {
		return nil, false, errors.New("duplicate command")
	}
	if r.returnConnections != nil {
		clone := r.returnConnections.Snapshot()
		return &clone, false, nil
	}
	r.connections = commit.Next.Snapshot()
	r.writes++
	r.replays[last.CommandID] = arena.GoldenIndividualConnectionReplay{
		Receipt: last, Connections: r.connections.Snapshot(),
	}
	clone := r.connections.Snapshot()
	return &clone, true, nil
}

func (r *task050ConnectionRepository) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func (r *task050ConnectionRepository) executionSnapshot() arena.GoldenWaveExecution {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.execution.Snapshot()
}

func (r *task050ConnectionRepository) submissionSnapshot() arena.GoldenSubmissionLedger {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.submissions.Snapshot()
}

func (r *task050ConnectionRepository) replaceSubmissions(submissions arena.GoldenSubmissionLedger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.submissions = submissions.Snapshot()
}

func (r *task050ConnectionRepository) connectionSnapshot() arena.GoldenIndividualConnectionLedger {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.connections.Snapshot()
}

var _ arena.GoldenIndividualDisconnectRepository = (*task050ConnectionRepository)(nil)
