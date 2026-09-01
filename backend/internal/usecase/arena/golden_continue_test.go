package arena_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenPartialContinuation(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 9, 1, 17, 0, 0, 0, time.UTC)
	state, execution := task049StartedFixture(t, startedAt)
	terminal, positions, sentinel := task049TerminalFixture(t, execution, 1, 12000)
	require.Len(t, terminal.Group.Attempts, len(state.Group.Attempts)+1)
	require.Equal(t, state.Group.Members, terminal.Group.Members)
	require.True(t, terminal.Group.ParticipationEstablished)
	require.Equal(t, state.Group.Attempts, terminal.Group.Attempts[:len(state.Group.Attempts)])
	require.Equal(t, terminal.Attempt, terminal.Group.Attempts[len(terminal.Group.Attempts)-1])
	repository := newTask049ContinuationRepository(state, terminal, positions, sentinel)
	unresolved := task049Unresolved(terminal)
	require.GreaterOrEqual(t, len(unresolved), 2)
	private := make([]arena.GoldenPrivateAssignmentCommand, len(unresolved))
	for index, participantID := range unresolved {
		private[index] = arena.GoldenPrivateAssignmentCommand{
			ParticipantID: participantID, AssignmentID: task049ID(12120 + index),
		}
	}
	command := arena.GoldenContinuationCommand{
		Scope: terminal.Scope.State, CommandID: task049ID(12100), ContinuationID: task049ID(12101),
		ExpectedTerminalID: terminal.ID, ExpectedTerminalDigest: terminal.PayloadDigest,
		ExpectedState: state.Expectation(), ExpectedPlan: state.Plan,
		ExpectedPositions: positions.Expectation(), ExpectedSwissPoints: sentinel,
		NextAttemptID: task049ID(12102), NextAssignmentID: task049ID(12103),
		NextAssignmentRevisionID: task049ID(12104), PrivateAssignments: private,
	}
	record, changed, err := arena.NewGoldenContinuationUseCase(
		repository,
		fixedArenaClock{now: terminal.FinishedAt.Add(time.Second)},
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
	require.Equal(t, domain.ArenaGoldenAttemptStatePlanned, record.Attempt.State)
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

	t.Run("over-capacity continuation record validation is panic-free", func(t *testing.T) {
		overcapacity := record.Snapshot()
		overcapacity.Positions.PositionTo++
		ordering := overcapacity.Positions.Attempts[0].Snapshot()
		ordering.SubmissionHead.NextSubmissionID = 5
		for index := 0; index < 3; index++ {
			participantID := task049ID(12140 + index)
			evidenceDigest := sha256.Sum256([]byte{byte(index + 1)})
			ordering.Order = append(ordering.Order, arena.GoldenPositionOrderEntry{
				SubmissionID: uint64(index + 2), ParticipantID: participantID,
				CommittedAt:    ordering.Order[0].CommittedAt.Add(time.Duration(index+1) * time.Nanosecond),
				EvidenceDigest: evidenceDigest,
			})
			overcapacity.Positions.Positions = append(overcapacity.Positions.Positions, arena.GoldenCommittedPosition{
				Position: overcapacity.Positions.PositionFrom + index + 1, ParticipantID: participantID,
				AttemptID: ordering.AttemptID, AttemptNo: ordering.AttemptNo,
				SubmissionID: uint64(index + 2), EvidenceDigest: evidenceDigest,
				CommitID: overcapacity.SourceTerminalID,
			})
			overcapacity.ResolvedParticipantIDs = append(overcapacity.ResolvedParticipantIDs, participantID)
		}
		task049SealOrdering(t, &ordering)
		overcapacity.Positions.Attempts[0] = ordering
		task049SealPositionLedger(t, &overcapacity.Positions)
		overcapacity.ExpectedPositions = overcapacity.Positions.Expectation()
		overcapacity.RemainingPositions = nil
		task049SealContinuation(t, &overcapacity)
		require.NoError(t, overcapacity.Positions.Validate())

		var validationErr error
		require.NotPanics(t, func() { validationErr = overcapacity.Validate() })
		require.ErrorIs(t, validationErr, arena.ErrInvalidGoldenContinuation)
	})

	t.Run("replays from terminal receipt before mutable loads", func(t *testing.T) {
		repository.archiveCurrent()
		repository.loadError = errors.New("mutable continuation heads must not be loaded on replay")
		beforeReplay := repository.snapshotState()
		replayed, replayChanged, replayErr := arena.NewGoldenContinuationUseCase(
			repository,
			fixedArenaClock{now: terminal.FinishedAt.Add(time.Hour)},
		).Continue(t.Context(), command)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, record.PayloadDigest, replayed.PayloadDigest)
		require.Equal(t, beforeReplay, repository.snapshotState())
		repository.restoreCurrent()
		repository.loadError = nil
	})

	t.Run("duplicate and competing commands do not create another child", func(t *testing.T) {
		replayed, replayChanged, replayErr := arena.NewGoldenContinuationUseCase(
			repository,
			fixedArenaClock{now: terminal.FinishedAt.Add(time.Minute)},
		).Continue(t.Context(), command)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Len(t, replayed.Group.Attempts, len(terminal.Group.Attempts)+1)

		baseline := command
		baseline.PrivateAssignments = append([]arena.GoldenPrivateAssignmentCommand(nil), command.PrivateAssignments...)
		competing := command
		competing.PrivateAssignments = append(
			[]arena.GoldenPrivateAssignmentCommand(nil),
			command.PrivateAssignments...,
		)
		competing.CommandID, competing.ContinuationID = task049ID(12200), task049ID(12201)
		competing.NextAttemptID, competing.NextAssignmentID = task049ID(12202), task049ID(12203)
		competing.NextAssignmentRevisionID = task049ID(12204)
		for index := range competing.PrivateAssignments {
			competing.PrivateAssignments[index].AssignmentID = task049ID(12220 + index)
		}
		require.Equal(t, baseline, command)
		result, competingChanged, competingErr := arena.NewGoldenContinuationUseCase(
			repository,
			fixedArenaClock{now: terminal.FinishedAt.Add(time.Minute)},
		).Continue(t.Context(), competing)
		require.Nil(t, result)
		require.False(t, competingChanged)
		require.ErrorIs(t, competingErr, arena.ErrGoldenContinuationAlreadyCommitted)
		require.Equal(t, 1, repository.commitCount())
	})

	t.Run("competing CAS creates exactly one successor", func(t *testing.T) {
		local := newTask049ContinuationRepository(state, terminal, positions, sentinel)
		commands := [2]arena.GoldenContinuationCommand{command, command}
		commands[1].CommandID, commands[1].ContinuationID = task049ID(12240), task049ID(12241)
		commands[1].NextAttemptID, commands[1].NextAssignmentID = task049ID(12242), task049ID(12243)
		commands[1].NextAssignmentRevisionID = task049ID(12244)
		commands[1].PrivateAssignments = append(
			[]arena.GoldenPrivateAssignmentCommand(nil),
			command.PrivateAssignments...,
		)
		for index := range commands[1].PrivateAssignments {
			commands[1].PrivateAssignments[index].AssignmentID = task049ID(12250 + index)
		}
		beforeCommit, releaseBarrier := task049ContinuationBarrier(t)
		local.beforeCommit = beforeCommit
		defer releaseBarrier()
		type result struct {
			record  *arena.GoldenContinuationRecord
			changed bool
			err     error
		}
		results := make([]result, len(commands))
		completed := make(chan struct{}, len(commands))
		for index := range commands {
			go func(index int) {
				defer func() { completed <- struct{}{} }()
				results[index].record, results[index].changed, results[index].err =
					arena.NewGoldenContinuationUseCase(
						local,
						fixedArenaClock{now: terminal.FinishedAt.Add(time.Second)},
					).Continue(t.Context(), commands[index])
			}(index)
		}
		if !task049AwaitContinuationCompletions(t, completed, len(commands), releaseBarrier) {
			return
		}
		winners := 0
		losers := 0
		for _, result := range results {
			if result.err == nil {
				require.True(t, result.changed)
				require.NotNil(t, result.record)
				winners++
				continue
			}
			require.False(t, result.changed)
			require.Nil(t, result.record)
			require.ErrorIs(t, result.err, arena.ErrGoldenContinuationAlreadyCommitted)
			losers++
		}
		require.Equal(t, 1, winners)
		require.Equal(t, 1, losers)
		require.Equal(t, 1, local.commitCount())
	})

	t.Run("stale terminal position plan and Swiss heads write nothing", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*arena.GoldenContinuationCommand)
		}{
			{name: "terminal", mutate: func(value *arena.GoldenContinuationCommand) { value.ExpectedTerminalDigest[0] ^= 0xff }},
			{name: "positions", mutate: func(value *arena.GoldenContinuationCommand) { value.ExpectedPositions.Revision++ }},
			{name: "plan", mutate: func(value *arena.GoldenContinuationCommand) { value.ExpectedPlan.RevisionID = task049ID(12301) }},
			{name: "Swiss", mutate: func(value *arena.GoldenContinuationCommand) { value.ExpectedSwissPoints.Revision++ }},
		}
		for index, testCase := range tests {
			t.Run(testCase.name, func(t *testing.T) {
				local := newTask049ContinuationRepository(state, terminal, positions, sentinel)
				localCommand := command
				localCommand.CommandID = task049ID(12310 + index*20)
				localCommand.ContinuationID = task049ID(12311 + index*20)
				localCommand.NextAttemptID = task049ID(12312 + index*20)
				localCommand.NextAssignmentID = task049ID(12313 + index*20)
				localCommand.NextAssignmentRevisionID = task049ID(12314 + index*20)
				testCase.mutate(&localCommand)
				result, localChanged, continueErr := arena.NewGoldenContinuationUseCase(
					local,
					fixedArenaClock{now: terminal.FinishedAt.Add(time.Second)},
				).Continue(t.Context(), localCommand)
				require.Nil(t, result)
				require.False(t, localChanged)
				require.ErrorIs(t, continueErr, arena.ErrGoldenContinuationAuthorityConflict)
				require.Zero(t, local.commitCount())
			})
		}
	})

	t.Run("same plan head cannot substitute different reserve edges", func(t *testing.T) {
		planAuthority, planCommand := goldenExactPlanFixture(t)
		planCommand.GroupCommands[1].EdgeIDs[1] = task049ID(12480)
		planCommand.GroupCommands[1].ReservationIDs[1] = task049ID(12481)
		planCommand.GroupCommands[1].SnapshotIDs[1] = task049ID(12482)
		alternatePlan, buildErr := arena.BuildGoldenExactPlan(planCommand, planAuthority)
		require.NoError(t, buildErr)
		require.Equal(t, state.ExactPlan.PlanID, alternatePlan.PlanID)
		require.Equal(t, state.ExactPlan.PlanRevisionID, alternatePlan.PlanRevisionID)
		require.NotEqual(t, state.ExactPlan.Groups[1].Edges[1], alternatePlan.Groups[1].Edges[1])

		local := newTask049ContinuationRepository(state, terminal, positions, sentinel)
		local.planOverride = &alternatePlan
		result, localChanged, continueErr := arena.NewGoldenContinuationUseCase(
			local,
			fixedArenaClock{now: terminal.FinishedAt.Add(time.Second)},
		).Continue(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, continueErr, arena.ErrGoldenContinuationAuthorityConflict)
		require.Zero(t, local.commitCount())
	})

	t.Run("valid foreign unchanged result is rejected", func(t *testing.T) {
		local := newTask049ContinuationRepository(state, terminal, positions, sentinel)
		foreign := record.Snapshot()
		local.returnRecord = &foreign
		local.returnChanged = false
		baseline := command
		baseline.PrivateAssignments = append([]arena.GoldenPrivateAssignmentCommand(nil), command.PrivateAssignments...)
		localCommand := command
		localCommand.PrivateAssignments = append(
			[]arena.GoldenPrivateAssignmentCommand(nil),
			command.PrivateAssignments...,
		)
		localCommand.CommandID, localCommand.ContinuationID = task049ID(12500), task049ID(12501)
		localCommand.NextAttemptID, localCommand.NextAssignmentID = task049ID(12502), task049ID(12503)
		localCommand.NextAssignmentRevisionID = task049ID(12504)
		for index := range localCommand.PrivateAssignments {
			localCommand.PrivateAssignments[index].AssignmentID = task049ID(12520 + index)
		}
		require.Equal(t, baseline, command)

		result, localChanged, continueErr := arena.NewGoldenContinuationUseCase(
			local,
			fixedArenaClock{now: terminal.FinishedAt.Add(time.Second)},
		).Continue(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, continueErr, domain.ErrInternal)

		changedLocal := newTask049ContinuationRepository(state, terminal, positions, sentinel)
		changedTimestamp := record.Snapshot()
		changedTimestamp.CreatedAt = changedTimestamp.CreatedAt.Add(time.Nanosecond)
		task049SealContinuation(t, &changedTimestamp)
		require.NoError(t, changedTimestamp.Validate())
		changedLocal.returnRecord = &changedTimestamp
		changedLocal.returnChanged = true
		result, localChanged, continueErr = arena.NewGoldenContinuationUseCase(
			changedLocal,
			fixedArenaClock{now: terminal.FinishedAt.Add(time.Second)},
		).Continue(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, continueErr, domain.ErrInternal)
	})

	t.Run("same-command unchanged result cannot change a command head", func(t *testing.T) {
		local := newTask049ContinuationRepository(state, terminal, positions, sentinel)
		tampered := record.Snapshot()
		tampered.SwissPoints = task049SwissPointSentinel(12530)
		task049SealContinuation(t, &tampered)
		require.NoError(t, tampered.Validate())
		local.returnRecord = &tampered
		local.returnChanged = false

		result, localChanged, continueErr := arena.NewGoldenContinuationUseCase(
			local,
			fixedArenaClock{now: terminal.FinishedAt.Add(time.Second)},
		).Continue(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, continueErr, domain.ErrInternal)
	})

	t.Run("new identities cannot alias retained state or plan roles", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*arena.GoldenContinuationCommand)
		}{
			{name: "attempt aliases group", mutate: func(value *arena.GoldenContinuationCommand) {
				value.NextAttemptID = state.Scope.GroupID
			}},
			{name: "private assignment aliases edge", mutate: func(value *arena.GoldenContinuationCommand) {
				value.PrivateAssignments[0].AssignmentID = state.ExactPlan.Groups[1].Edges[0].ID
			}},
			{name: "continuation aliases active Wave", mutate: func(value *arena.GoldenContinuationCommand) {
				value.ContinuationID = terminal.Scope.WaveID
			}},
		}
		for index, testCase := range tests {
			t.Run(testCase.name, func(t *testing.T) {
				local := newTask049ContinuationRepository(state, terminal, positions, sentinel)
				localCommand := command
				localCommand.PrivateAssignments = append(
					[]arena.GoldenPrivateAssignmentCommand(nil),
					command.PrivateAssignments...,
				)
				localCommand.CommandID, localCommand.ContinuationID = task049ID(12540+index*20), task049ID(12541+index*20)
				localCommand.NextAttemptID, localCommand.NextAssignmentID = task049ID(12542+index*20), task049ID(12543+index*20)
				localCommand.NextAssignmentRevisionID = task049ID(12544 + index*20)
				for privateIndex := range localCommand.PrivateAssignments {
					localCommand.PrivateAssignments[privateIndex].AssignmentID = task049ID(12550 + index*20 + privateIndex)
				}
				testCase.mutate(&localCommand)

				result, localChanged, continueErr := arena.NewGoldenContinuationUseCase(
					local,
					fixedArenaClock{now: terminal.FinishedAt.Add(time.Second)},
				).Continue(t.Context(), localCommand)
				require.Nil(t, result)
				require.False(t, localChanged)
				require.ErrorIs(t, continueErr, arena.ErrInvalidGoldenContinuation)
				require.Zero(t, local.commitCount())
			})
		}
	})

	t.Run("sole or zero unresolved members route to fallback", func(t *testing.T) {
		for _, solved := range []int{len(execution.Membership.ParticipantIDs) - 1, len(execution.Membership.ParticipantIDs)} {
			localTerminal, localPositions, localSentinel := task049TerminalFixture(t, execution, solved, 12600+solved*100)
			local := newTask049ContinuationRepository(state, localTerminal, localPositions, localSentinel)
			localCommand := command
			localCommand.CommandID, localCommand.ContinuationID = uuid.New(), uuid.New()
			localCommand.ExpectedTerminalID = localTerminal.ID
			localCommand.ExpectedTerminalDigest = localTerminal.PayloadDigest
			localCommand.ExpectedPositions = localPositions.Expectation()
			localCommand.ExpectedSwissPoints = localSentinel
			remaining := task049Unresolved(localTerminal)
			localCommand.PrivateAssignments = make([]arena.GoldenPrivateAssignmentCommand, len(remaining))
			for index, participantID := range remaining {
				localCommand.PrivateAssignments[index] = arena.GoldenPrivateAssignmentCommand{
					ParticipantID: participantID, AssignmentID: uuid.New(),
				}
			}
			result, localChanged, continueErr := arena.NewGoldenContinuationUseCase(
				local,
				fixedArenaClock{now: localTerminal.FinishedAt.Add(time.Second)},
			).Continue(t.Context(), localCommand)
			require.Nil(t, result)
			require.False(t, localChanged)
			require.ErrorIs(t, continueErr, arena.ErrGoldenContinuationFallbackRequired)
			require.Zero(t, local.commitCount())
		}
	})

	t.Run("reserve exhaustion writes no successor", func(t *testing.T) {
		exhaustedOpenedAt := startedAt.Add(time.Hour)
		exhaustedState := task048GoldenState(t, exhaustedOpenedAt)
		participants := exhaustedState.ActiveParticipantIDs()
		firstAttemptID := task049ID(12800)
		secondAttemptID := task049ID(12801)
		firstFinishedAt := exhaustedOpenedAt.Add(-3 * time.Minute)
		secondFinishedAt := exhaustedOpenedAt.Add(-time.Minute)
		exhaustedState.Group.Attempts = []domain.ArenaGoldenAttempt{
			{
				ID: firstAttemptID, GroupID: exhaustedState.Scope.GroupID,
				GroupRevisionID: exhaustedState.Scope.GroupRevisionID, AttemptNo: 1,
				State: domain.ArenaGoldenAttemptStateCancelled, ParticipantIDs: participants,
				FinishedAt: &firstFinishedAt,
			},
			{
				ID: secondAttemptID, GroupID: exhaustedState.Scope.GroupID,
				GroupRevisionID: exhaustedState.Scope.GroupRevisionID, AttemptNo: 2,
				PreviousAttemptID: &firstAttemptID, State: domain.ArenaGoldenAttemptStateCancelled,
				ParticipantIDs: participants, FinishedAt: &secondFinishedAt,
			},
		}
		exhaustedState.PayloadDigest = [sha256.Size]byte{}
		exhaustedState, buildErr := arena.BuildGoldenState(exhaustedState)
		require.NoError(t, buildErr)
		waveRepository := &goldenWaveRepositoryFake{state: exhaustedState}
		exhaustedExecution := task048OpenGoldenExecution(t, waveRepository, exhaustedState, exhaustedOpenedAt, 30000)
		exhaustedExecution = task048ReadyAll(t, waveRepository, exhaustedExecution, exhaustedOpenedAt, 30100)
		exhaustedStartedAt := exhaustedOpenedAt.Add(20 * time.Second)
		authority := task048AuthorityLease(exhaustedStartedAt, exhaustedState.Scope.TournamentID)
		waveRepository.setAuthority(authority, exhaustedStartedAt)
		started, startedChanged, startErr := arena.NewGoldenStartUseCase(
			waveRepository,
			fixedArenaClock{now: exhaustedStartedAt},
		).Start(t.Context(), task048StartCommand(exhaustedExecution, authority, 30200))
		require.NoError(t, startErr)
		require.True(t, startedChanged)
		require.Equal(t, domain.ArenaAssignmentReserveCount+1, started.Attempt.AttemptNo)

		exhaustedTerminal, exhaustedPositions, exhaustedSentinel := task049TerminalFixture(t, *started, 1, 12810)
		local := newTask049ContinuationRepository(exhaustedState, exhaustedTerminal, exhaustedPositions, exhaustedSentinel)
		exhaustedUnresolved := task049Unresolved(exhaustedTerminal)
		exhaustedPrivate := make([]arena.GoldenPrivateAssignmentCommand, len(exhaustedUnresolved))
		for index, participantID := range exhaustedUnresolved {
			exhaustedPrivate[index] = arena.GoldenPrivateAssignmentCommand{
				ParticipantID: participantID, AssignmentID: task049ID(13420 + index),
			}
		}
		exhaustedCommand := arena.GoldenContinuationCommand{
			Scope: exhaustedTerminal.Scope.State, CommandID: task049ID(13400),
			ContinuationID: task049ID(13401), ExpectedTerminalID: exhaustedTerminal.ID,
			ExpectedTerminalDigest: exhaustedTerminal.PayloadDigest,
			ExpectedState:          exhaustedState.Expectation(), ExpectedPlan: exhaustedState.Plan,
			ExpectedPositions: exhaustedPositions.Expectation(), ExpectedSwissPoints: exhaustedSentinel,
			NextAttemptID: task049ID(13402), NextAssignmentID: task049ID(13403),
			NextAssignmentRevisionID: task049ID(13404), PrivateAssignments: exhaustedPrivate,
		}
		result, localChanged, continueErr := arena.NewGoldenContinuationUseCase(
			local,
			fixedArenaClock{now: exhaustedTerminal.FinishedAt.Add(time.Second)},
		).Continue(t.Context(), exhaustedCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, continueErr, arena.ErrGoldenContinuationReservesExhausted)
		require.Zero(t, local.commitCount())
	})

	t.Run("terminal membership cannot diverge from its source state", func(t *testing.T) {
		tamperedTerminal := terminal.Snapshot()
		tamperedTerminal.Group.Members[1].Excluded = true
		task049SealAttemptRecord(t, &tamperedTerminal)
		require.NoError(t, tamperedTerminal.Validate())
		local := newTask049ContinuationRepository(state, tamperedTerminal, positions, sentinel)
		localCommand := command
		localCommand.ExpectedTerminalDigest = tamperedTerminal.PayloadDigest
		result, localChanged, continueErr := arena.NewGoldenContinuationUseCase(
			local,
			fixedArenaClock{now: tamperedTerminal.FinishedAt.Add(time.Second)},
		).Continue(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, continueErr, arena.ErrGoldenContinuationAuthorityConflict)
		require.Zero(t, local.commitCount())
	})

	t.Run("excluded unresolved member is removed before successor creation", func(t *testing.T) {
		firstOpenedAt := startedAt.Add(time.Hour)
		initial := goldenStateFixture(t, firstOpenedAt)
		stateRepository := &goldenStateRepositoryFake{state: initial}
		localState := initial
		for index, member := range initial.Group.Members[:2] {
			localState = goldenAcceptReady(
				t, stateRepository, localState, member.ParticipantID,
				firstOpenedAt.Add(time.Duration(index+1)*time.Second), 12800+index*10,
			)
		}
		localState = goldenResolveNoShow(t, stateRepository, localState, firstOpenedAt, 12830)
		excludedID := initial.Group.Members[2].ParticipantID
		require.NotContains(t, localState.ActiveParticipantIDs(), excludedID)

		secondOpenedAt := localState.NoShows[0].ResolvedAt.Add(time.Second)
		waveRepository := &goldenWaveRepositoryFake{state: localState}
		localExecution := task048OpenGoldenExecution(t, waveRepository, localState, secondOpenedAt, 22000)
		localExecution = task048ReadyAll(t, waveRepository, localExecution, secondOpenedAt, 22100)
		localStartedAt := secondOpenedAt.Add(10 * time.Second)
		authority := task048AuthorityLease(localStartedAt, localState.Scope.TournamentID)
		waveRepository.setAuthority(authority, localStartedAt)
		started, startedChanged, startErr := arena.NewGoldenStartUseCase(
			waveRepository,
			fixedArenaClock{now: localStartedAt},
		).Start(t.Context(), task048StartCommand(localExecution, authority, 22200))
		require.NoError(t, startErr)
		require.True(t, startedChanged)
		localTerminal, localPositions, localSentinel := task049TerminalFixture(t, *started, 0, 12900)
		local := newTask049ContinuationRepository(localState, localTerminal, localPositions, localSentinel)
		localUnresolved := task049Unresolved(localTerminal)
		require.Len(t, localUnresolved, 2)
		require.NotContains(t, localUnresolved, excludedID)
		localPrivate := make([]arena.GoldenPrivateAssignmentCommand, len(localUnresolved))
		for index, participantID := range localUnresolved {
			localPrivate[index] = arena.GoldenPrivateAssignmentCommand{
				ParticipantID: participantID, AssignmentID: task049ID(13020 + index),
			}
		}
		localCommand := arena.GoldenContinuationCommand{
			Scope: localTerminal.Scope.State, CommandID: task049ID(13010), ContinuationID: task049ID(13011),
			ExpectedTerminalID: localTerminal.ID, ExpectedTerminalDigest: localTerminal.PayloadDigest,
			ExpectedState: localState.Expectation(), ExpectedPlan: localState.Plan,
			ExpectedPositions: localPositions.Expectation(), ExpectedSwissPoints: localSentinel,
			NextAttemptID: task049ID(13012), NextAssignmentID: task049ID(13013),
			NextAssignmentRevisionID: task049ID(13014), PrivateAssignments: localPrivate,
		}
		successor, localChanged, continueErr := arena.NewGoldenContinuationUseCase(
			local,
			fixedArenaClock{now: localTerminal.FinishedAt.Add(time.Second)},
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
		record.Positions.Positions[0].ParticipantID = task049ID(12900)
		record.ResolvedParticipantIDs[0] = task049ID(12901)
		after := repository.archivedSnapshot()
		require.Equal(t, before, after)
		tampered := after.Snapshot()
		tampered.Positions.Positions[0].ParticipantID = task049ID(12902)
		require.Error(t, tampered.Validate())
		require.NotContains(t, task049Encode(t, after), execution.Assignment.Snapshot.Flag)
	})

	t.Run("parent continuation advances the next exact reserve", func(t *testing.T) {
		chainState, chainExecution := task049StartedFixture(t, startedAt.Add(3*time.Hour))
		firstTerminal, firstPositions, chainSentinel := task049TerminalFixture(t, chainExecution, 1, 13000)
		chainRepository := newTask049ContinuationRepository(
			chainState,
			firstTerminal,
			firstPositions,
			chainSentinel,
		)
		firstCommand := task049ContinuationCommand(firstTerminal, chainState, firstPositions, chainSentinel, nil, 13100)
		first, firstChanged, firstErr := arena.NewGoldenContinuationUseCase(
			chainRepository,
			fixedArenaClock{now: firstTerminal.FinishedAt.Add(time.Second)},
		).Continue(t.Context(), firstCommand)
		require.NoError(t, firstErr)
		require.True(t, firstChanged)

		secondTerminal, secondPositions := task049NextTerminalFixture(t, firstTerminal, *first, 0, 13200)
		foreignParticipantID := task049ID(13450)
		replacedParticipantID := first.UnresolvedParticipantIDs[len(first.UnresolvedParticipantIDs)-1]
		forgedParent := first.Snapshot()
		task049ReplaceContinuationParticipant(&forgedParent, replacedParticipantID, foreignParticipantID)
		task049SealReserveAssignment(t, &forgedParent.Assignment)
		task049SealContinuation(t, &forgedParent)
		require.NoError(t, forgedParent.Assignment.Validate())
		require.NoError(t, forgedParent.Attempt.Validate())
		_, forgedGroupErr := domain.NewArenaGoldenGroup(forgedParent.Group)
		require.NoError(t, forgedGroupErr)
		require.Equal(t, forgedParent.Attempt, forgedParent.Group.Attempts[len(forgedParent.Group.Attempts)-1])
		require.NoError(t, forgedParent.Validate())
		forgedTerminal := secondTerminal.Snapshot()
		task049ReplaceTerminalParticipant(&forgedTerminal, replacedParticipantID, foreignParticipantID)
		task049SealAttemptAssignmentEvidence(t, &forgedTerminal.Assignment)
		task049SealAttemptRecord(t, &forgedTerminal)
		require.NoError(t, forgedTerminal.Validate())
		foreignRepository := newTask049ContinuationRepository(
			chainState,
			firstTerminal,
			firstPositions,
			chainSentinel,
		)
		foreignRepository.advanceTerminal(forgedParent, forgedTerminal, secondPositions)
		foreignCommand := task049ContinuationCommand(
			forgedTerminal,
			chainState,
			secondPositions,
			chainSentinel,
			&forgedParent,
			13500,
		)
		foreignResult, foreignChanged, foreignErr := arena.NewGoldenContinuationUseCase(
			foreignRepository,
			fixedArenaClock{now: forgedTerminal.FinishedAt.Add(time.Second)},
		).Continue(t.Context(), foreignCommand)
		require.Nil(t, foreignResult)
		require.False(t, foreignChanged)
		require.ErrorIs(t, foreignErr, arena.ErrGoldenContinuationAuthorityConflict)
		require.Zero(t, foreignRepository.commitCount())

		wrongTerminal := secondTerminal.Snapshot()
		wrongTerminal.Assignment.EdgeID = task049ID(13250)
		task049SealAttemptAssignmentEvidence(t, &wrongTerminal.Assignment)
		task049SealAttemptRecord(t, &wrongTerminal)
		require.NoError(t, wrongTerminal.Validate())
		wrongParentRepository := newTask049ContinuationRepository(
			chainState,
			firstTerminal,
			firstPositions,
			chainSentinel,
		)
		wrongParentRepository.advanceTerminal(*first, wrongTerminal, secondPositions)
		wrongParentCommand := task049ContinuationCommand(
			wrongTerminal,
			chainState,
			secondPositions,
			chainSentinel,
			first,
			13260,
		)
		wrongResult, wrongChanged, wrongErr := arena.NewGoldenContinuationUseCase(
			wrongParentRepository,
			fixedArenaClock{now: wrongTerminal.FinishedAt.Add(time.Second)},
		).Continue(t.Context(), wrongParentCommand)
		require.Nil(t, wrongResult)
		require.False(t, wrongChanged)
		require.ErrorIs(t, wrongErr, arena.ErrGoldenContinuationAuthorityConflict)
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
			mutate func(*arena.GoldenContinuationCommand)
		}{
			{name: "terminal", mutate: func(command *arena.GoldenContinuationCommand) {
				command.CommandID = command.ExpectedTerminalID
			}},
			{name: "parent", mutate: func(command *arena.GoldenContinuationCommand) {
				command.ContinuationID = command.ExpectedParentID
			}},
		} {
			t.Run("rejects "+sourceAlias.name+" receipt identity reuse", func(t *testing.T) {
				aliased := secondCommand
				sourceAlias.mutate(&aliased)
				result, changed, aliasErr := arena.NewGoldenContinuationUseCase(
					chainRepository,
					fixedArenaClock{now: secondTerminal.FinishedAt.Add(time.Second)},
				).Continue(t.Context(), aliased)
				require.Nil(t, result)
				require.False(t, changed)
				require.ErrorIs(t, aliasErr, arena.ErrInvalidGoldenContinuation)
				require.Equal(t, 1, chainRepository.commitCount())
			})
		}

		staleParent := secondCommand
		staleParent.CommandID = task049ID(13400)
		staleParent.ContinuationID = task049ID(13401)
		staleParent.ExpectedParentDigest = sha256.Sum256([]byte("stale parent receipt"))
		staleResult, staleChanged, staleErr := arena.NewGoldenContinuationUseCase(
			chainRepository,
			fixedArenaClock{now: secondTerminal.FinishedAt.Add(time.Second)},
		).Continue(t.Context(), staleParent)
		require.Nil(t, staleResult)
		require.False(t, staleChanged)
		require.ErrorIs(t, staleErr, arena.ErrGoldenContinuationAuthorityConflict)
		require.Equal(t, 1, chainRepository.commitCount())

		second, secondChanged, secondErr := arena.NewGoldenContinuationUseCase(
			chainRepository,
			fixedArenaClock{now: secondTerminal.FinishedAt.Add(time.Second)},
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
		replayed, replayChanged, replayErr := arena.NewGoldenContinuationUseCase(
			chainRepository,
			fixedArenaClock{now: secondTerminal.FinishedAt.Add(time.Hour)},
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
		thirdResult, thirdChanged, thirdErr := arena.NewGoldenContinuationUseCase(
			chainRepository,
			fixedArenaClock{now: thirdTerminal.FinishedAt.Add(time.Second)},
		).Continue(t.Context(), thirdCommand)
		require.Nil(t, thirdResult)
		require.False(t, thirdChanged)
		require.ErrorIs(t, thirdErr, arena.ErrGoldenContinuationReservesExhausted)
		require.Equal(t, 2, chainRepository.commitCount())
	})
}

type task049ContinuationRepositoryState struct {
	State       arena.GoldenState
	Terminal    arena.GoldenAttemptCommitRecord
	Positions   arena.GoldenPositionLedger
	SwissPoints arena.GoldenSwissPointLedgerSentinel
	Parent      *arena.GoldenContinuationRecord
	Current     *arena.GoldenContinuationRecord
	Archived    *arena.GoldenContinuationRecord
	Replays     map[uuid.UUID]arena.GoldenContinuationRecord
	Reserved    map[uuid.UUID]struct{}
	Consumed    map[uuid.UUID]struct{}
	Commits     int
}

type task049ContinuationRepository struct {
	mu            sync.Mutex
	state         arena.GoldenState
	terminal      arena.GoldenAttemptCommitRecord
	positions     arena.GoldenPositionLedger
	swissPoints   arena.GoldenSwissPointLedgerSentinel
	parent        *arena.GoldenContinuationRecord
	current       *arena.GoldenContinuationRecord
	archived      *arena.GoldenContinuationRecord
	replays       map[uuid.UUID]arena.GoldenContinuationRecord
	reserved      map[uuid.UUID]struct{}
	consumed      map[uuid.UUID]struct{}
	loadError     error
	commitError   error
	commits       int
	returnRecord  *arena.GoldenContinuationRecord
	returnChanged bool
	beforeCommit  func()
	planOverride  *arena.GoldenExactPlan
}

func newTask049ContinuationRepository(
	state arena.GoldenState,
	terminal arena.GoldenAttemptCommitRecord,
	positions arena.GoldenPositionLedger,
	sentinel arena.GoldenSwissPointLedgerSentinel,
) *task049ContinuationRepository {
	return &task049ContinuationRepository{
		state: state.Snapshot(), terminal: terminal.Snapshot(), positions: positions.Snapshot(),
		swissPoints: sentinel, replays: make(map[uuid.UUID]arena.GoldenContinuationRecord),
		reserved: make(map[uuid.UUID]struct{}), consumed: make(map[uuid.UUID]struct{}),
	}
}

func (r *task049ContinuationRepository) FindGoldenContinuation(
	_ context.Context,
	_ uuid.UUID,
	commandID uuid.UUID,
) (*arena.GoldenContinuationRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if replay, found := r.replays[commandID]; found {
		clone := replay.Snapshot()
		return &clone, nil
	}
	return nil, nil
}

func (r *task049ContinuationRepository) LoadGoldenContinuationAuthority(
	_ context.Context,
	scope arena.GoldenStateScope,
) (arena.GoldenContinuationAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loadError != nil {
		return arena.GoldenContinuationAuthority{}, r.loadError
	}
	authority := arena.GoldenContinuationAuthority{
		Scope: scope, State: r.state.Snapshot(), Plan: r.state.ExactPlan.Snapshot(),
		Terminal: r.terminal.Snapshot(), Positions: r.positions.Snapshot(), SwissPoints: r.swissPoints,
	}
	if r.parent != nil {
		clone := r.parent.Snapshot()
		authority.Parent = &clone
	}
	if r.planOverride != nil {
		authority.Plan = r.planOverride.Snapshot()
	}
	if r.current != nil {
		clone := r.current.Snapshot()
		authority.Current = &clone
	} else if r.archived != nil {
		clone := r.archived.Snapshot()
		authority.Current = &clone
	}
	return authority, nil
}

func (r *task049ContinuationRepository) CommitGoldenContinuation(
	_ context.Context,
	record arena.GoldenContinuationRecord,
) (*arena.GoldenContinuationRecord, bool, error) {
	if r.beforeCommit != nil {
		r.beforeCommit()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.commitError != nil {
		return nil, false, r.commitError
	}
	if r.returnRecord != nil {
		result := r.returnRecord.Snapshot()
		return &result, r.returnChanged, nil
	}
	if r.current != nil || r.archived != nil || r.terminal.ID != record.SourceTerminalID ||
		r.terminal.PayloadDigest != record.SourceTerminalDigest ||
		!task049ContinuationParentMatches(r.parent, record.SourceParentID, record.SourceParentDigest) ||
		!r.state.Expectation().Equal(record.ExpectedState) || r.state.Plan != record.ExpectedPlan ||
		!r.positions.Expectation().Equal(record.ExpectedPositions) || r.swissPoints != record.SwissPoints {
		return nil, false, domain.ErrConflict
	}
	if _, alreadyConsumed := r.consumed[record.SourceTerminalID]; alreadyConsumed {
		return nil, false, domain.ErrConflict
	}
	for _, identity := range record.NewIdentityIDs {
		if _, exists := r.reserved[identity]; exists {
			return nil, false, domain.ErrConflict
		}
	}
	clone := record.Snapshot()
	r.current = &clone
	r.replays[record.CommandID] = clone
	for _, identity := range record.NewIdentityIDs {
		r.reserved[identity] = struct{}{}
	}
	r.consumed[record.SourceTerminalID] = struct{}{}
	r.commits++
	result := clone.Snapshot()
	return &result, true, nil
}

func (r *task049ContinuationRepository) advanceTerminal(
	parent arena.GoldenContinuationRecord,
	terminal arena.GoldenAttemptCommitRecord,
	positions arena.GoldenPositionLedger,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	parentClone := parent.Snapshot()
	r.parent = &parentClone
	r.terminal = terminal.Snapshot()
	r.positions = positions.Snapshot()
	r.current = nil
	r.archived = nil
}

func task049ContinuationParentMatches(
	parent *arena.GoldenContinuationRecord,
	expectedID uuid.UUID,
	expectedDigest [sha256.Size]byte,
) bool {
	if parent == nil {
		return expectedID == uuid.Nil && expectedDigest == [sha256.Size]byte{}
	}
	return parent.ID == expectedID && parent.PayloadDigest == expectedDigest
}

func (r *task049ContinuationRepository) archiveCurrent() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		clone := r.current.Snapshot()
		r.archived = &clone
		r.current = nil
	}
}

func (r *task049ContinuationRepository) restoreCurrent() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.archived != nil {
		clone := r.archived.Snapshot()
		r.current = &clone
	}
}

func (r *task049ContinuationRepository) archivedSnapshot() arena.GoldenContinuationRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.archived != nil {
		return r.archived.Snapshot()
	}
	return r.current.Snapshot()
}

func (r *task049ContinuationRepository) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *task049ContinuationRepository) snapshotState() task049ContinuationRepositoryState {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := task049ContinuationRepositoryState{
		State: r.state.Snapshot(), Terminal: r.terminal.Snapshot(), Positions: r.positions.Snapshot(),
		SwissPoints: r.swissPoints, Replays: make(map[uuid.UUID]arena.GoldenContinuationRecord, len(r.replays)),
		Reserved: make(map[uuid.UUID]struct{}, len(r.reserved)),
		Consumed: make(map[uuid.UUID]struct{}, len(r.consumed)), Commits: r.commits,
	}
	if r.parent != nil {
		clone := r.parent.Snapshot()
		state.Parent = &clone
	}
	if r.current != nil {
		clone := r.current.Snapshot()
		state.Current = &clone
	}
	if r.archived != nil {
		clone := r.archived.Snapshot()
		state.Archived = &clone
	}
	for commandID, replay := range r.replays {
		state.Replays[commandID] = replay.Snapshot()
	}
	for identity := range r.reserved {
		state.Reserved[identity] = struct{}{}
	}
	for identity := range r.consumed {
		state.Consumed[identity] = struct{}{}
	}
	return state
}

func task049ContinuationBarrier(t *testing.T) (func(), func()) {
	t.Helper()
	var mu sync.Mutex
	var releaseOnce sync.Once
	arrived := 0
	release := make(chan struct{})
	forceRelease := func() {
		releaseOnce.Do(func() { close(release) })
	}
	t.Cleanup(forceRelease)
	wait := func() {
		mu.Lock()
		arrived++
		if arrived == 2 {
			forceRelease()
		}
		mu.Unlock()
		select {
		case <-release:
		case <-time.After(task049WaitTimeout):
			t.Errorf("continuation repository barrier timed out")
			forceRelease()
		}
	}
	return wait, forceRelease
}

func task049AwaitContinuationCompletions(
	t *testing.T,
	completed <-chan struct{},
	count int,
	release func(),
) bool {
	t.Helper()
	timer := time.NewTimer(2 * task049WaitTimeout)
	defer timer.Stop()
	for received := 0; received < count; received++ {
		select {
		case <-completed:
		case <-timer.C:
			release()
			t.Errorf("competing continuation commands timed out after %d of %d completions", received, count)
			return false
		}
	}
	return true
}

func task049TerminalFixture(
	t *testing.T,
	execution arena.GoldenWaveExecution,
	solved int,
	base int,
) (arena.GoldenAttemptCommitRecord, arena.GoldenPositionLedger, arena.GoldenSwissPointLedgerSentinel) {
	t.Helper()
	scope := task049SubmissionScope(execution)
	ledger, err := arena.NewGoldenSubmissionLedger(scope, task049ID(base))
	require.NoError(t, err)
	submissionRepository := newTask049SubmissionRepository(execution, ledger, execution.Start.StartedAt.Add(time.Second))
	current := ledger
	for index, participantID := range execution.Membership.ParticipantIDs[:solved] {
		verification := task049Verification(scope, execution, participantID, base+10+index*10)
		submissionRepository.verifications[verification.ID] = verification
		command := arena.GoldenSubmissionCommand{
			Scope: scope, CommandID: task049ID(base + 40 + index*10),
			ActorParticipantID: participantID, ParticipantID: participantID,
			VerificationID: verification.ID, ExpectedExecution: execution.Expectation(),
			NextLedgerRevisionID: task049ID(base + 41 + index*10),
		}
		updated, changed, submitErr := arena.NewGoldenSubmissionUseCase(submissionRepository).Submit(t.Context(), command)
		require.NoError(t, submitErr)
		require.True(t, changed)
		current = *updated
	}
	positions, err := arena.NewGoldenPositionLedger(
		execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, task049ID(base+70),
	)
	require.NoError(t, err)
	sentinel := task049SwissPointSentinel(base + 71)
	repository := newTask049CommitRepository(submissionRepository, current, positions, sentinel)
	command := arena.GoldenAttemptCommitCommand{
		Scope: scope, CommandID: task049ID(base + 80), CommitID: task049ID(base + 81),
		ExpectedExecution: execution.Expectation(), ExpectedSubmissions: current.Expectation(),
		ExpectedPositions: positions.Expectation(), ExpectedSwissPoints: sentinel,
		NextPositionRevisionID: task049ID(base + 82), Reason: arena.GoldenAttemptTerminalDeadline,
	}
	record, changed, err := arena.NewGoldenAttemptCommitUseCase(
		repository,
		fixedArenaClock{now: execution.Start.Deadline},
	).CommitAttempt(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	return *record, record.Positions.Snapshot(), sentinel
}

func task049ContinuationCommand(
	terminal arena.GoldenAttemptCommitRecord,
	state arena.GoldenState,
	positions arena.GoldenPositionLedger,
	sentinel arena.GoldenSwissPointLedgerSentinel,
	parent *arena.GoldenContinuationRecord,
	base int,
) arena.GoldenContinuationCommand {
	unresolved := task049Unresolved(terminal)
	private := make([]arena.GoldenPrivateAssignmentCommand, len(unresolved))
	for index, participantID := range unresolved {
		private[index] = arena.GoldenPrivateAssignmentCommand{
			ParticipantID: participantID,
			AssignmentID:  task049ID(base + 20 + index),
		}
	}
	command := arena.GoldenContinuationCommand{
		Scope: terminal.Scope.State, CommandID: task049ID(base), ContinuationID: task049ID(base + 1),
		ExpectedTerminalID: terminal.ID, ExpectedTerminalDigest: terminal.PayloadDigest,
		ExpectedState: state.Expectation(), ExpectedPlan: state.Plan,
		ExpectedPositions: positions.Expectation(), ExpectedSwissPoints: sentinel,
		NextAttemptID: task049ID(base + 2), NextAssignmentID: task049ID(base + 3),
		NextAssignmentRevisionID: task049ID(base + 4), PrivateAssignments: private,
	}
	if parent != nil {
		command.ExpectedParentID = parent.ID
		command.ExpectedParentDigest = parent.PayloadDigest
	}
	return command
}

func task049ReplaceContinuationParticipant(
	record *arena.GoldenContinuationRecord,
	from uuid.UUID,
	to uuid.UUID,
) {
	for index := range record.Group.Members {
		if record.Group.Members[index].ParticipantID == from {
			record.Group.Members[index].ParticipantID = to
		}
	}
	for index := range record.Group.Attempts {
		task049ReplaceID(record.Group.Attempts[index].ParticipantIDs, from, to)
	}
	task049ReplaceID(record.Attempt.ParticipantIDs, from, to)
	task049ReplaceID(record.UnresolvedParticipantIDs, from, to)
	for index := range record.Assignment.Private {
		if record.Assignment.Private[index].ParticipantID == from {
			record.Assignment.Private[index].ParticipantID = to
		}
	}
}

func task049ReplaceTerminalParticipant(
	record *arena.GoldenAttemptCommitRecord,
	from uuid.UUID,
	to uuid.UUID,
) {
	for index := range record.Group.Members {
		if record.Group.Members[index].ParticipantID == from {
			record.Group.Members[index].ParticipantID = to
		}
	}
	for index := range record.Group.Attempts {
		task049ReplaceID(record.Group.Attempts[index].ParticipantIDs, from, to)
	}
	task049ReplaceID(record.Attempt.ParticipantIDs, from, to)
	for index := range record.Wave.Members {
		if record.Wave.Members[index].ParticipantID == from {
			record.Wave.Members[index].ParticipantID = to
		}
	}
	for index := range record.Assignment.Private {
		if record.Assignment.Private[index].ParticipantID == from {
			record.Assignment.Private[index].ParticipantID = to
		}
	}
}

func task049ReplaceID(values []uuid.UUID, from uuid.UUID, to uuid.UUID) {
	for index := range values {
		if values[index] == from {
			values[index] = to
		}
	}
}

func task049NextTerminalFixture(
	t *testing.T,
	previous arena.GoldenAttemptCommitRecord,
	parent arena.GoldenContinuationRecord,
	solved int,
	base int,
) (arena.GoldenAttemptCommitRecord, arena.GoldenPositionLedger) {
	t.Helper()
	require.GreaterOrEqual(t, len(parent.UnresolvedParticipantIDs), solved)
	startedAt := previous.FinishedAt.Add(time.Second)
	finishedAt := startedAt.Add(time.Minute)
	scope := arena.GoldenSubmissionScope{
		State: parent.Scope, AttemptID: parent.Attempt.ID, WaveID: task049ID(base),
		AssignmentID: parent.Assignment.ID, SnapshotID: parent.Assignment.SnapshotID,
		TaskID: parent.Assignment.TaskID,
	}
	submissionHead := arena.GoldenSubmissionLedgerExpectation{
		Scope: scope, RevisionID: task049ID(base + 1), Revision: 1,
		NextSubmissionID: uint64(solved + 1), PayloadDigest: sha256.Sum256([]byte("second attempt submissions")),
	}
	ordering := arena.GoldenAttemptOrderingEvidence{
		AttemptID: parent.Attempt.ID, AttemptNo: parent.Attempt.AttemptNo,
		SubmissionHead: submissionHead,
	}
	for index := 0; index < solved; index++ {
		ordering.Order = append(ordering.Order, arena.GoldenPositionOrderEntry{
			SubmissionID: uint64(index + 1), ParticipantID: parent.UnresolvedParticipantIDs[index],
			CommittedAt:    startedAt.Add(time.Duration(index+1) * time.Second),
			EvidenceDigest: sha256.Sum256([]byte{byte(base), byte(index)}),
		})
	}
	task049SealOrdering(t, &ordering)

	positions := parent.Positions.Snapshot()
	previousRevisionID := positions.RevisionID
	positions.PreviousRevisionID = &previousRevisionID
	positions.RevisionID = task049ID(base + 2)
	positions.Revision++
	positions.RevisionIDs = append(positions.RevisionIDs, positions.RevisionID)
	positions.Attempts = append(positions.Attempts, ordering.Snapshot())
	for _, item := range ordering.Order {
		positions.Positions = append(positions.Positions, arena.GoldenCommittedPosition{
			Position: positions.PositionFrom + len(positions.Positions), ParticipantID: item.ParticipantID,
			AttemptID: ordering.AttemptID, AttemptNo: ordering.AttemptNo,
			SubmissionID: item.SubmissionID, EvidenceDigest: item.EvidenceDigest, CommitID: task049ID(base + 3),
		})
	}
	task049SealPositionLedger(t, &positions)

	attempt := parent.Attempt
	attempt.State = domain.ArenaGoldenAttemptStateCompleted
	attempt.StartedAt = &startedAt
	attempt.FinishedAt = &finishedAt
	group := parent.Group
	group.Attempts = append([]domain.ArenaGoldenAttempt(nil), parent.Group.Attempts...)
	group.Attempts[len(group.Attempts)-1] = attempt
	members := make([]domain.ArenaWaveMember, len(attempt.ParticipantIDs))
	for index, participantID := range attempt.ParticipantIDs {
		members[index] = domain.ArenaWaveMember{ParticipantID: participantID, Ready: true}
	}
	openedAt := startedAt.Add(-time.Minute)
	windowDeadline := startedAt.Add(time.Minute)
	wave := domain.ArenaWave{
		ID: scope.WaveID, TournamentID: scope.State.TournamentID,
		RevisionID: domain.ArenaWaveRevisionID(task049ID(base + 4)), State: domain.ArenaWaveStateCompleted,
		Members: members, StartedAt: &startedAt,
		ReadyWindow: &domain.ArenaReadyWindow{
			ID: task049ID(base + 5), WaveID: scope.WaveID,
			RevisionID: domain.ArenaReadyWindowRevisionID(task049ID(base + 6)),
			State:      domain.ArenaReadyWindowStateConsumed, OpenedAt: openedAt,
			Deadline: windowDeadline, ConsumedAt: &startedAt,
		},
	}
	require.NoError(t, wave.Validate())
	active := previous.ActiveExecution
	active.RevisionID = task049ID(base + 7)
	active.Revision++
	active.PayloadDigest = sha256.Sum256([]byte("second attempt execution"))
	active.AttemptID = attempt.ID
	active.WaveID = wave.ID
	active.WaveRevisionID = wave.RevisionID
	active.AssignmentID = parent.Assignment.ID
	active.AssignmentRevisionID = parent.Assignment.RevisionID
	active.AssignmentRevision = 1
	active.AssignmentDigest = sha256.Sum256([]byte("live execution assignment uses its own schema"))
	require.NotEqual(t, parent.Assignment.PayloadDigest, active.AssignmentDigest)
	active.Started = true
	assignment := arena.GoldenAttemptAssignmentEvidence{
		ID: parent.Assignment.ID, RevisionID: parent.Assignment.RevisionID, Revision: 1,
		Scope: parent.Scope, AttemptID: parent.Attempt.ID, WaveID: wave.ID,
		MembershipID: active.MembershipID, Plan: parent.ExpectedPlan,
		EdgeID: parent.Assignment.EdgeID, ReservationID: parent.Assignment.ReservationID,
		SnapshotID: parent.Assignment.SnapshotID, TaskID: parent.Assignment.TaskID,
		ContentDigest:          parent.Assignment.ContentDigest,
		Private:                append([]arena.GoldenPrivateAssignment(nil), parent.Assignment.Private...),
		ExecutionPayloadDigest: active.AssignmentDigest,
	}
	task049SealAttemptAssignmentEvidence(t, &assignment)
	require.NoError(t, assignment.Validate())
	record := arena.GoldenAttemptCommitRecord{
		ID: task049ID(base + 3), CommandID: task049ID(base + 8),
		CommandDigest: sha256.Sum256([]byte("second terminal command")), Scope: scope,
		ActiveExecution: active, Assignment: assignment, ExpectedSubmissions: submissionHead,
		ExpectedPositions: parent.Positions.Expectation(), SwissPoints: parent.SwissPoints,
		Reason: arena.GoldenAttemptTerminalDeadline, FinishedAt: finishedAt,
		Attempt: attempt, Group: group, Wave: wave, Ordering: ordering,
		PriorPositions: parent.Positions.Snapshot(), Positions: positions,
	}
	task049SealAttemptRecord(t, &record)
	require.NoError(t, record.Validate())
	return record, positions
}

func task049Unresolved(terminal arena.GoldenAttemptCommitRecord) []uuid.UUID {
	resolved := make(map[uuid.UUID]struct{}, len(terminal.Positions.Positions))
	for _, position := range terminal.Positions.Positions {
		resolved[position.ParticipantID] = struct{}{}
	}
	result := make([]uuid.UUID, 0, len(terminal.Group.Members))
	for _, member := range terminal.Group.Members {
		if _, solved := resolved[member.ParticipantID]; !member.Excluded && !solved {
			result = append(result, member.ParticipantID)
		}
	}
	return result
}

func task049RemainingPositions(terminal arena.GoldenAttemptCommitRecord) []int {
	result := make([]int, 0, terminal.Group.PositionTo-terminal.Group.PositionFrom+1-len(terminal.Positions.Positions))
	for position := terminal.Group.PositionFrom + len(terminal.Positions.Positions); position <= terminal.Group.PositionTo; position++ {
		result = append(result, position)
	}
	return result
}

func task049Contains(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func task049SealContinuation(t *testing.T, record *arena.GoldenContinuationRecord) {
	t.Helper()
	record.PayloadDigest = task049GobDigest(t, struct {
		ID                       uuid.UUID
		CommandID                uuid.UUID
		CommandDigest            [sha256.Size]byte
		Scope                    arena.GoldenStateScope
		SourceTerminalID         uuid.UUID
		SourceTerminalDigest     [sha256.Size]byte
		SourceParentID           uuid.UUID
		SourceParentDigest       [sha256.Size]byte
		ExpectedState            arena.GoldenStateExpectation
		ExpectedPlan             arena.GoldenPlanStateBinding
		ExpectedPositions        arena.GoldenPositionLedgerExpectation
		SwissPoints              arena.GoldenSwissPointLedgerSentinel
		NewIdentityIDs           []uuid.UUID
		ResolvedParticipantIDs   []uuid.UUID
		UnresolvedParticipantIDs []uuid.UUID
		RemainingPositions       []int
		Group                    domain.ArenaGoldenGroupState
		Attempt                  domain.ArenaGoldenAttempt
		Assignment               arena.GoldenReserveAttemptAssignment
		CreatedAt                time.Time
		Positions                arena.GoldenPositionLedger
	}{
		ID: record.ID, CommandID: record.CommandID, CommandDigest: record.CommandDigest,
		Scope: record.Scope, SourceTerminalID: record.SourceTerminalID,
		SourceTerminalDigest: record.SourceTerminalDigest, SourceParentID: record.SourceParentID,
		SourceParentDigest: record.SourceParentDigest, ExpectedState: record.ExpectedState,
		ExpectedPlan: record.ExpectedPlan, ExpectedPositions: record.ExpectedPositions,
		SwissPoints: record.SwissPoints, NewIdentityIDs: record.NewIdentityIDs,
		ResolvedParticipantIDs: record.ResolvedParticipantIDs, UnresolvedParticipantIDs: record.UnresolvedParticipantIDs,
		RemainingPositions: record.RemainingPositions, Group: record.Group, Attempt: record.Attempt,
		Assignment: record.Assignment, CreatedAt: record.CreatedAt, Positions: record.Positions,
	})
}

func task049SealReserveAssignment(t *testing.T, assignment *arena.GoldenReserveAttemptAssignment) {
	t.Helper()
	assignment.PayloadDigest = task049GobDigest(t, struct {
		ID            uuid.UUID
		RevisionID    uuid.UUID
		Scope         arena.GoldenStateScope
		AttemptID     uuid.UUID
		EdgeID        uuid.UUID
		EdgePosition  int
		ReservationID uuid.UUID
		SnapshotID    uuid.UUID
		TaskID        uuid.UUID
		ContentDigest [sha256.Size]byte
		Private       []arena.GoldenPrivateAssignment
	}{
		ID: assignment.ID, RevisionID: assignment.RevisionID, Scope: assignment.Scope,
		AttemptID: assignment.AttemptID, EdgeID: assignment.EdgeID, EdgePosition: assignment.EdgePosition,
		ReservationID: assignment.ReservationID, SnapshotID: assignment.SnapshotID,
		TaskID: assignment.TaskID, ContentDigest: assignment.ContentDigest, Private: assignment.Private,
	})
}

var _ arena.GoldenContinuationRepository = (*task049ContinuationRepository)(nil)
