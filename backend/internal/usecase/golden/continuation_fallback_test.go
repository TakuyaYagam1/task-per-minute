package golden_test

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGoldenContinuationFallbackAndReserves(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 1, 17, 0, 0, 0, time.UTC)
	state, execution := continuationTask049StartedFixture(t, startedAt)
	terminal, positions, sentinel := task049TerminalFixture(t, execution, 1, 12000)
	require.Len(t, terminal.Group.Attempts, len(state.Group.Attempts)+1)
	require.Equal(t, state.Group.Members, terminal.Group.Members)
	require.True(t, terminal.Group.ParticipationEstablished)
	require.Equal(t, state.Group.Attempts, terminal.Group.Attempts[:len(state.Group.Attempts)])
	require.Equal(t, terminal.Attempt, terminal.Group.Attempts[len(terminal.Group.Attempts)-1])
	repository := newTask049ContinuationHarness(t, state, terminal, positions, sentinel)
	unresolved := task049Unresolved(terminal)
	require.GreaterOrEqual(t, len(unresolved), 2)
	private := make([]goldenusecase.GoldenPrivateAssignmentCommand, len(unresolved))
	for index, participantID := range unresolved {
		private[index] = goldenusecase.GoldenPrivateAssignmentCommand{
			ParticipantID: participantID, AssignmentID: continuationTask049ID(12120 + index),
		}
	}
	command := goldenusecase.GoldenContinuationCommand{
		Scope: terminal.Scope.State, CommandID: continuationTask049ID(12100), ContinuationID: continuationTask049ID(12101),
		ExpectedTerminalID: terminal.ID, ExpectedTerminalDigest: terminal.PayloadDigest,
		ExpectedState: state.Expectation(), ExpectedPlan: state.Plan,
		ExpectedPositions: positions.Expectation(), ExpectedSwissPoints: sentinel,
		NextAttemptID: continuationTask049ID(12102), NextAssignmentID: continuationTask049ID(12103),
		NextAssignmentRevisionID: continuationTask049ID(12104), PrivateAssignments: private,
	}
	record, changed, err := goldenusecase.NewGoldenContinuationUseCase(
		repository,
		continuationNewGoldenClock(t, terminal.FinishedAt.Add(time.Second)),
	).Continue(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, terminal.ID, record.SourceTerminalID)
	require.Equal(t, terminal.PayloadDigest, record.SourceTerminalDigest)
	require.Equal(t, positions.Expectation(), record.Positions.Expectation())
	require.Equal(t, sentinel, record.SwissPoints)
	require.Equal(t, unresolved, record.UnresolvedParticipantIDs)
	require.Equal(t, terminal.Attempt.AttemptNo+1, record.Attempt.AttemptNo)
	require.Equal(t, unresolved, record.Attempt.ParticipantIDs)
	require.Equal(t, domain.GoldenAttemptStatePlanned, record.Attempt.State)
	require.Equal(t, terminal.Attempt.ID, *record.Attempt.PreviousAttemptID)
	require.Len(t, record.Group.Attempts, len(terminal.Group.Attempts)+1)
	require.Equal(t, state.ExactPlan.Groups[1].Edges[1].ID, record.Assignment.EdgeID)
	require.Equal(t, state.ExactPlan.Groups[1].Edges[1].ReservationID, record.Assignment.ReservationID)
	require.Equal(t, state.ExactPlan.Groups[1].Edges[1].Snapshot.SnapshotID, record.Assignment.SnapshotID)
	require.Equal(t, task049RemainingPositions(terminal), record.RemainingPositions)
	for _, member := range record.Group.Members {
		if task049Contains(record.ResolvedParticipantIDs, member.ParticipantID) {
			require.False(t, member.Excluded, "committed solvers remain historical members, not no-show exclusions")
		}
	}
	repository.archiveCurrent()
	repository.restoreCurrent()

	t.Run("sole or zero unresolved members route to fallback", func(t *testing.T) {
		for _, solved := range []int{len(execution.Membership.ParticipantIDs) - 1, len(execution.Membership.ParticipantIDs)} {
			localTerminal, localPositions, localSentinel := task049TerminalFixture(t, execution, solved, 12600+solved*100)
			local := newTask049ContinuationHarness(t, state, localTerminal, localPositions, localSentinel)
			localCommand := command
			localCommand.CommandID, localCommand.ContinuationID = uuid.New(), uuid.New()
			localCommand.ExpectedTerminalID = localTerminal.ID
			localCommand.ExpectedTerminalDigest = localTerminal.PayloadDigest
			localCommand.ExpectedPositions = localPositions.Expectation()
			localCommand.ExpectedSwissPoints = localSentinel
			remaining := task049Unresolved(localTerminal)
			localCommand.PrivateAssignments = make([]goldenusecase.GoldenPrivateAssignmentCommand, len(remaining))
			for index, participantID := range remaining {
				localCommand.PrivateAssignments[index] = goldenusecase.GoldenPrivateAssignmentCommand{
					ParticipantID: participantID, AssignmentID: uuid.New(),
				}
			}
			result, localChanged, continueErr := goldenusecase.NewGoldenContinuationUseCase(
				local,
				continuationNewGoldenClock(t, localTerminal.FinishedAt.Add(time.Second)),
			).Continue(t.Context(), localCommand)
			require.Nil(t, result)
			require.False(t, localChanged)
			require.ErrorIs(t, continueErr, goldenusecase.ErrGoldenContinuationFallbackRequired)
			require.Zero(t, local.commitCount())
		}
	})

	t.Run("reserve exhaustion writes no successor", func(t *testing.T) {
		exhaustedOpenedAt := startedAt.Add(time.Hour)
		exhaustedState := continuationTask048GoldenState(t, exhaustedOpenedAt)
		participants := exhaustedState.ActiveParticipantIDs()
		firstAttemptID := continuationTask049ID(12800)
		secondAttemptID := continuationTask049ID(12801)
		firstFinishedAt := exhaustedOpenedAt.Add(-3 * time.Minute)
		secondFinishedAt := exhaustedOpenedAt.Add(-time.Minute)
		exhaustedState.Group.Attempts = []domain.GoldenAttempt{
			{
				ID: firstAttemptID, GroupID: exhaustedState.Scope.GroupID,
				GroupRevisionID: exhaustedState.Scope.GroupRevisionID, AttemptNo: 1,
				State: domain.GoldenAttemptStateCancelled, ParticipantIDs: participants,
				FinishedAt: &firstFinishedAt,
			},
			{
				ID: secondAttemptID, GroupID: exhaustedState.Scope.GroupID,
				GroupRevisionID: exhaustedState.Scope.GroupRevisionID, AttemptNo: 2,
				PreviousAttemptID: &firstAttemptID, State: domain.GoldenAttemptStateCancelled,
				ParticipantIDs: participants, FinishedAt: &secondFinishedAt,
			},
		}
		exhaustedState.PayloadDigest = [sha256.Size]byte{}
		exhaustedState, buildErr := goldenusecase.BuildGoldenState(exhaustedState)
		require.NoError(t, buildErr)
		waveRepository := continuationNewGoldenWaveRepositoryHarness(t, exhaustedState)
		exhaustedExecution := continuationTask048OpenGoldenExecution(t, waveRepository, exhaustedState, exhaustedOpenedAt, 30000)
		exhaustedExecution = continuationTask048ReadyAll(t, waveRepository, exhaustedExecution, exhaustedOpenedAt, 30100)
		exhaustedStartedAt := exhaustedOpenedAt.Add(20 * time.Second)
		authority := continuationTask048AuthorityLease(exhaustedStartedAt, exhaustedState.Scope.TournamentID)
		waveRepository.setAuthority(authority, exhaustedStartedAt)
		started, startedChanged, startErr := goldenusecase.NewGoldenStartUseCase(
			waveRepository,
			continuationNewGoldenClock(t, exhaustedStartedAt),
		).Start(t.Context(), continuationTask048StartCommand(exhaustedExecution, authority, 30200))
		require.NoError(t, startErr)
		require.True(t, startedChanged)
		require.Equal(t, domain.AssignmentReserveCount+1, started.Attempt.AttemptNo)

		exhaustedTerminal, exhaustedPositions, exhaustedSentinel := task049TerminalFixture(t, *started, 1, 12810)
		local := newTask049ContinuationHarness(t, exhaustedState, exhaustedTerminal, exhaustedPositions, exhaustedSentinel)
		exhaustedUnresolved := task049Unresolved(exhaustedTerminal)
		exhaustedPrivate := make([]goldenusecase.GoldenPrivateAssignmentCommand, len(exhaustedUnresolved))
		for index, participantID := range exhaustedUnresolved {
			exhaustedPrivate[index] = goldenusecase.GoldenPrivateAssignmentCommand{
				ParticipantID: participantID, AssignmentID: continuationTask049ID(13420 + index),
			}
		}
		exhaustedCommand := goldenusecase.GoldenContinuationCommand{
			Scope: exhaustedTerminal.Scope.State, CommandID: continuationTask049ID(13400),
			ContinuationID: continuationTask049ID(13401), ExpectedTerminalID: exhaustedTerminal.ID,
			ExpectedTerminalDigest: exhaustedTerminal.PayloadDigest,
			ExpectedState:          exhaustedState.Expectation(), ExpectedPlan: exhaustedState.Plan,
			ExpectedPositions: exhaustedPositions.Expectation(), ExpectedSwissPoints: exhaustedSentinel,
			NextAttemptID: continuationTask049ID(13402), NextAssignmentID: continuationTask049ID(13403),
			NextAssignmentRevisionID: continuationTask049ID(13404), PrivateAssignments: exhaustedPrivate,
		}
		result, localChanged, continueErr := goldenusecase.NewGoldenContinuationUseCase(
			local,
			continuationNewGoldenClock(t, exhaustedTerminal.FinishedAt.Add(time.Second)),
		).Continue(t.Context(), exhaustedCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, continueErr, goldenusecase.ErrGoldenContinuationReservesExhausted)
		require.Zero(t, local.commitCount())
	})

	t.Run("terminal membership cannot diverge from its source state", func(t *testing.T) {
		tamperedTerminal := terminal.Snapshot()
		tamperedTerminal.Group.Members[1].Excluded = true
		continuationTask049SealAttemptRecord(t, &tamperedTerminal)
		require.NoError(t, tamperedTerminal.Validate())
		local := newTask049ContinuationHarness(t, state, tamperedTerminal, positions, sentinel)
		localCommand := command
		localCommand.ExpectedTerminalDigest = tamperedTerminal.PayloadDigest
		result, localChanged, continueErr := goldenusecase.NewGoldenContinuationUseCase(
			local,
			continuationNewGoldenClock(t, tamperedTerminal.FinishedAt.Add(time.Second)),
		).Continue(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, continueErr, goldenusecase.ErrGoldenContinuationAuthorityConflict)
		require.Zero(t, local.commitCount())
	})

	t.Run("excluded unresolved member is removed before successor creation", func(t *testing.T) {
		firstOpenedAt := startedAt.Add(time.Hour)
		initial := continuationGoldenStateFixture(t, firstOpenedAt)
		stateRepository := continuationNewGoldenStateRepository(t, initial)
		localState := initial
		for index, member := range initial.Group.Members[:2] {
			localState = continuationGoldenAcceptReady(
				t, stateRepository, localState, member.ParticipantID,
				firstOpenedAt.Add(time.Duration(index+1)*time.Second), 12800+index*10,
			)
		}
		localState = continuationGoldenResolveNoShow(t, stateRepository, localState, firstOpenedAt, 12830)
		excludedID := initial.Group.Members[2].ParticipantID
		require.NotContains(t, localState.ActiveParticipantIDs(), excludedID)

		secondOpenedAt := localState.NoShows[0].ResolvedAt.Add(time.Second)
		waveRepository := continuationNewGoldenWaveRepositoryHarness(t, localState)
		localExecution := continuationTask048OpenGoldenExecution(t, waveRepository, localState, secondOpenedAt, 22000)
		localExecution = continuationTask048ReadyAll(t, waveRepository, localExecution, secondOpenedAt, 22100)
		localStartedAt := secondOpenedAt.Add(10 * time.Second)
		authority := continuationTask048AuthorityLease(localStartedAt, localState.Scope.TournamentID)
		waveRepository.setAuthority(authority, localStartedAt)
		started, startedChanged, startErr := goldenusecase.NewGoldenStartUseCase(
			waveRepository,
			continuationNewGoldenClock(t, localStartedAt),
		).Start(t.Context(), continuationTask048StartCommand(localExecution, authority, 22200))
		require.NoError(t, startErr)
		require.True(t, startedChanged)
		localTerminal, localPositions, localSentinel := task049TerminalFixture(t, *started, 0, 12900)
		local := newTask049ContinuationHarness(t, localState, localTerminal, localPositions, localSentinel)
		localUnresolved := task049Unresolved(localTerminal)
		require.Len(t, localUnresolved, 2)
		require.NotContains(t, localUnresolved, excludedID)
		localPrivate := make([]goldenusecase.GoldenPrivateAssignmentCommand, len(localUnresolved))
		for index, participantID := range localUnresolved {
			localPrivate[index] = goldenusecase.GoldenPrivateAssignmentCommand{
				ParticipantID: participantID, AssignmentID: continuationTask049ID(13020 + index),
			}
		}
		localCommand := goldenusecase.GoldenContinuationCommand{
			Scope: localTerminal.Scope.State, CommandID: continuationTask049ID(13010), ContinuationID: continuationTask049ID(13011),
			ExpectedTerminalID: localTerminal.ID, ExpectedTerminalDigest: localTerminal.PayloadDigest,
			ExpectedState: localState.Expectation(), ExpectedPlan: localState.Plan,
			ExpectedPositions: localPositions.Expectation(), ExpectedSwissPoints: localSentinel,
			NextAttemptID: continuationTask049ID(13012), NextAssignmentID: continuationTask049ID(13013),
			NextAssignmentRevisionID: continuationTask049ID(13014), PrivateAssignments: localPrivate,
		}
		successor, localChanged, continueErr := goldenusecase.NewGoldenContinuationUseCase(
			local,
			continuationNewGoldenClock(t, localTerminal.FinishedAt.Add(time.Second)),
		).Continue(t.Context(), localCommand)
		require.NoError(t, continueErr)
		require.True(t, localChanged)
		require.NotContains(t, successor.UnresolvedParticipantIDs, excludedID)
		require.NotContains(t, successor.Attempt.ParticipantIDs, excludedID)
		require.Equal(t, localPositions, successor.Positions)
		for _, assignment := range successor.Assignment.Private {
			require.NotEqual(t, excludedID, assignment.ParticipantID)
		}
	})

	t.Run("returned successor cannot mutate prior position bytes", func(t *testing.T) {
		before := repository.archivedSnapshot()
		record.Positions.Positions[0].ParticipantID = continuationTask049ID(12900)
		record.ResolvedParticipantIDs[0] = continuationTask049ID(12901)
		after := repository.archivedSnapshot()
		require.Equal(t, before, after)
		tampered := after.Snapshot()
		tampered.Positions.Positions[0].ParticipantID = continuationTask049ID(12902)
		require.Error(t, tampered.Validate())
		require.NotContains(t, continuationTask049Encode(t, after), execution.Assignment.Snapshot.Flag)
	})

	t.Run("parent continuation advances the next exact reserve", func(t *testing.T) {
		chainState, chainExecution := continuationTask049StartedFixture(t, startedAt.Add(3*time.Hour))
		firstTerminal, firstPositions, chainSentinel := task049TerminalFixture(t, chainExecution, 1, 13000)
		chainRepository := newTask049ContinuationHarness(t,
			chainState,
			firstTerminal,
			firstPositions,
			chainSentinel)

		firstCommand := task049ContinuationCommand(firstTerminal, chainState, firstPositions, chainSentinel, nil, 13100)
		first, firstChanged, firstErr := goldenusecase.NewGoldenContinuationUseCase(
			chainRepository,
			continuationNewGoldenClock(t, firstTerminal.FinishedAt.Add(time.Second)),
		).Continue(t.Context(), firstCommand)
		require.NoError(t, firstErr)
		require.True(t, firstChanged)

		secondTerminal, secondPositions := task049NextTerminalFixture(t, firstTerminal, *first, 0, 13200)
		foreignParticipantID := continuationTask049ID(13450)
		replacedParticipantID := first.UnresolvedParticipantIDs[len(first.UnresolvedParticipantIDs)-1]
		forgedParent := first.Snapshot()
		task049ReplaceContinuationParticipant(&forgedParent, replacedParticipantID, foreignParticipantID)
		task049SealReserveAssignment(t, &forgedParent.Assignment)
		task049SealContinuation(t, &forgedParent)
		require.NoError(t, forgedParent.Assignment.Validate())
		require.NoError(t, forgedParent.Attempt.Validate())
		_, forgedGroupErr := domain.NewGoldenGroup(forgedParent.Group)
		require.NoError(t, forgedGroupErr)
		require.Equal(t, forgedParent.Attempt, forgedParent.Group.Attempts[len(forgedParent.Group.Attempts)-1])
		require.NoError(t, forgedParent.Validate())
		forgedTerminal := secondTerminal.Snapshot()
		task049ReplaceTerminalParticipant(&forgedTerminal, replacedParticipantID, foreignParticipantID)
		continuationTask049SealAttemptAssignmentEvidence(t, &forgedTerminal.Assignment)
		continuationTask049SealAttemptRecord(t, &forgedTerminal)
		require.NoError(t, forgedTerminal.Validate())
		foreignRepository := newTask049ContinuationHarness(t,
			chainState,
			firstTerminal,
			firstPositions,
			chainSentinel)

		foreignRepository.advanceTerminal(forgedParent, forgedTerminal, secondPositions)
		foreignCommand := task049ContinuationCommand(
			forgedTerminal,
			chainState,
			secondPositions,
			chainSentinel,
			&forgedParent,
			13500,
		)
		foreignResult, foreignChanged, foreignErr := goldenusecase.NewGoldenContinuationUseCase(
			foreignRepository,
			continuationNewGoldenClock(t, forgedTerminal.FinishedAt.Add(time.Second)),
		).Continue(t.Context(), foreignCommand)
		require.Nil(t, foreignResult)
		require.False(t, foreignChanged)
		require.ErrorIs(t, foreignErr, goldenusecase.ErrGoldenContinuationAuthorityConflict)
		require.Zero(t, foreignRepository.commitCount())

		wrongTerminal := secondTerminal.Snapshot()
		wrongTerminal.Assignment.EdgeID = continuationTask049ID(13250)
		continuationTask049SealAttemptAssignmentEvidence(t, &wrongTerminal.Assignment)
		continuationTask049SealAttemptRecord(t, &wrongTerminal)
		require.NoError(t, wrongTerminal.Validate())
		wrongParentRepository := newTask049ContinuationHarness(t,
			chainState,
			firstTerminal,
			firstPositions,
			chainSentinel)

		wrongParentRepository.advanceTerminal(*first, wrongTerminal, secondPositions)
		wrongParentCommand := task049ContinuationCommand(
			wrongTerminal,
			chainState,
			secondPositions,
			chainSentinel,
			first,
			13260,
		)
		wrongResult, wrongChanged, wrongErr := goldenusecase.NewGoldenContinuationUseCase(
			wrongParentRepository,
			continuationNewGoldenClock(t, wrongTerminal.FinishedAt.Add(time.Second)),
		).Continue(t.Context(), wrongParentCommand)
		require.Nil(t, wrongResult)
		require.False(t, wrongChanged)
		require.ErrorIs(t, wrongErr, goldenusecase.ErrGoldenContinuationAuthorityConflict)
		require.Zero(t, wrongParentRepository.commitCount())

		chainRepository.advanceTerminal(*first, secondTerminal, secondPositions)
		secondCommand := task049ContinuationCommand(
			secondTerminal,
			chainState,
			secondPositions,
			chainSentinel,
			first,
			13300,
		)
		for _, sourceAlias := range []struct {
			name   string
			mutate func(*goldenusecase.GoldenContinuationCommand)
		}{
			{name: "terminal", mutate: func(command *goldenusecase.GoldenContinuationCommand) {
				command.CommandID = command.ExpectedTerminalID
			}},
			{name: "parent", mutate: func(command *goldenusecase.GoldenContinuationCommand) {
				command.ContinuationID = command.ExpectedParentID
			}},
		} {
			t.Run("rejects "+sourceAlias.name+" receipt identity reuse", func(t *testing.T) {
				aliased := secondCommand
				sourceAlias.mutate(&aliased)
				result, changed, aliasErr := goldenusecase.NewGoldenContinuationUseCase(
					chainRepository,
					continuationNewGoldenClock(t, secondTerminal.FinishedAt.Add(time.Second)),
				).Continue(t.Context(), aliased)
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, aliasErr, goldenusecase.ErrInvalidGoldenContinuation)
				require.Equal(t, 1, chainRepository.commitCount())
			})
		}

		staleParent := secondCommand
		staleParent.CommandID = continuationTask049ID(13400)
		staleParent.ContinuationID = continuationTask049ID(13401)
		staleParent.ExpectedParentDigest = sha256.Sum256([]byte("stale parent receipt"))
		staleResult, staleChanged, staleErr := goldenusecase.NewGoldenContinuationUseCase(
			chainRepository,
			continuationNewGoldenClock(t, secondTerminal.FinishedAt.Add(time.Second)),
		).Continue(t.Context(), staleParent)
		require.Nil(t, staleResult)
		require.False(t, staleChanged)
		require.ErrorIs(t, staleErr, goldenusecase.ErrGoldenContinuationAuthorityConflict)
		require.Equal(t, 1, chainRepository.commitCount())

		second, secondChanged, secondErr := goldenusecase.NewGoldenContinuationUseCase(
			chainRepository,
			continuationNewGoldenClock(t, secondTerminal.FinishedAt.Add(time.Second)),
		).Continue(t.Context(), secondCommand)
		require.NoError(t, secondErr)
		require.True(t, secondChanged)
		require.Equal(t, first.ID, second.SourceParentID)
		require.Equal(t, first.PayloadDigest, second.SourceParentDigest)
		require.True(t, second.ExpectedState.Equal(chainState.Expectation()))
		require.Equal(t, chainState, chainRepository.state)
		require.Equal(t, chainState.ExactPlan.Groups[1].Edges[2].ID, second.Assignment.EdgeID)
		require.Equal(t, secondTerminal.Attempt.ID, *second.Attempt.PreviousAttemptID)
		require.Equal(t, secondPositions, second.Positions)
		require.Equal(t, firstPositions.Positions, second.Positions.Positions)
		require.Len(t, second.Positions.Attempts, 2)
		require.Equal(t, task049Unresolved(secondTerminal), second.UnresolvedParticipantIDs)
		require.Equal(t, 2, chainRepository.commitCount())

		chainRepository.archiveCurrent()
		chainRepository.loadError = errors.New("mutable continuation heads must not be loaded on parent replay")
		beforeReplay := chainRepository.snapshotState()
		replayed, replayChanged, replayErr := goldenusecase.NewGoldenContinuationUseCase(
			chainRepository,
			continuationNewGoldenClock(t, secondTerminal.FinishedAt.Add(time.Hour)),
		).Continue(t.Context(), secondCommand)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, second.PayloadDigest, replayed.PayloadDigest)
		require.Equal(t, beforeReplay, chainRepository.snapshotState())

		chainRepository.loadError = nil
		thirdTerminal, thirdPositions := task049NextTerminalFixture(t, secondTerminal, *second, 0, 13600)
		chainRepository.advanceTerminal(*second, thirdTerminal, thirdPositions)
		thirdCommand := task049ContinuationCommand(
			thirdTerminal,
			chainState,
			thirdPositions,
			chainSentinel,
			second,
			13700,
		)
		thirdResult, thirdChanged, thirdErr := goldenusecase.NewGoldenContinuationUseCase(
			chainRepository,
			continuationNewGoldenClock(t, thirdTerminal.FinishedAt.Add(time.Second)),
		).Continue(t.Context(), thirdCommand)
		require.Nil(t, thirdResult)
		require.False(t, thirdChanged)
		require.ErrorIs(t, thirdErr, goldenusecase.ErrGoldenContinuationReservesExhausted)
		require.Equal(t, 2, chainRepository.commitCount())
	})
}
