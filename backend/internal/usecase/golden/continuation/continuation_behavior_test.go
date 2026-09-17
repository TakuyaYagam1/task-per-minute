package golden_test

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenattempt "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/attempt"
	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/continuation"
	goldenwave "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/wave"

	"github.com/stretchr/testify/require"
)

func TestGoldenPartialContinuation(t *testing.T) {
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
	private := make([]goldenwave.GoldenPrivateAssignmentCommand, len(unresolved))
	for index, participantID := range unresolved {
		private[index] = goldenwave.GoldenPrivateAssignmentCommand{
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

	t.Run("over-capacity continuation record validation is panic-free", func(t *testing.T) {
		overcapacity := record.Snapshot()
		overcapacity.Positions.PositionTo++
		ordering := overcapacity.Positions.Attempts[0].Snapshot()
		ordering.SubmissionHead.NextSubmissionID = 5
		for index := 0; index < 3; index++ {
			participantID := continuationTask049ID(12140 + index)
			evidenceDigest := sha256.Sum256([]byte{byte(index + 1)})
			ordering.Order = append(ordering.Order, goldenattempt.GoldenPositionOrderEntry{
				SubmissionID: uint64(index + 2), ParticipantID: participantID,
				CommittedAt:    ordering.Order[0].CommittedAt.Add(time.Duration(index+1) * time.Nanosecond),
				EvidenceDigest: evidenceDigest,
			})
		overcapacity.Positions.Positions = append(overcapacity.Positions.Positions, goldenattempt.GoldenCommittedPosition{
				Position: overcapacity.Positions.PositionFrom + index + 1, ParticipantID: participantID,
				AttemptID: ordering.AttemptID, AttemptNo: ordering.AttemptNo,
				SubmissionID: uint64(index + 2), EvidenceDigest: evidenceDigest,
				CommitID: overcapacity.SourceTerminalID,
			})
			overcapacity.ResolvedParticipantIDs = append(overcapacity.ResolvedParticipantIDs, participantID)
		}
		continuationTask049SealOrdering(t, &ordering)
		overcapacity.Positions.Attempts[0] = ordering
		continuationTask049SealPositionLedger(t, &overcapacity.Positions)
		overcapacity.ExpectedPositions = overcapacity.Positions.Expectation()
		overcapacity.RemainingPositions = nil
		task049SealContinuation(t, &overcapacity)
		require.NoError(t, overcapacity.Positions.Validate())

		var validationErr error
		require.NotPanics(t, func() { validationErr = overcapacity.Validate() })
		require.ErrorIs(t, validationErr, goldenusecase.ErrInvalidGoldenContinuation)
	})

	t.Run("replays from terminal receipt before mutable loads", func(t *testing.T) {
		repository.archiveCurrent()
		repository.loadError = errors.New("mutable continuation heads must not be loaded on replay")
		beforeReplay := repository.snapshotState()
		replayed, replayChanged, replayErr := goldenusecase.NewGoldenContinuationUseCase(
			repository,
			continuationNewGoldenClock(t, terminal.FinishedAt.Add(time.Hour)),
		).Continue(t.Context(), command)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, record.PayloadDigest, replayed.PayloadDigest)
		require.Equal(t, beforeReplay, repository.snapshotState())
		repository.restoreCurrent()
		repository.loadError = nil
	})

	t.Run("duplicate and competing commands do not create another child", func(t *testing.T) {
		replayed, replayChanged, replayErr := goldenusecase.NewGoldenContinuationUseCase(
			repository,
			continuationNewGoldenClock(t, terminal.FinishedAt.Add(time.Minute)),
		).Continue(t.Context(), command)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Len(t, replayed.Group.Attempts, len(terminal.Group.Attempts)+1)

		baseline := command
		baseline.PrivateAssignments = append([]goldenwave.GoldenPrivateAssignmentCommand(nil), command.PrivateAssignments...)
		competing := command
		competing.PrivateAssignments = append(
			[]goldenwave.GoldenPrivateAssignmentCommand(nil),
			command.PrivateAssignments...,
		)
		competing.CommandID, competing.ContinuationID = continuationTask049ID(12200), continuationTask049ID(12201)
		competing.NextAttemptID, competing.NextAssignmentID = continuationTask049ID(12202), continuationTask049ID(12203)
		competing.NextAssignmentRevisionID = continuationTask049ID(12204)
		for index := range competing.PrivateAssignments {
			competing.PrivateAssignments[index].AssignmentID = continuationTask049ID(12220 + index)
		}
		require.Equal(t, baseline, command)
		result, competingChanged, competingErr := goldenusecase.NewGoldenContinuationUseCase(
			repository,
			continuationNewGoldenClock(t, terminal.FinishedAt.Add(time.Minute)),
		).Continue(t.Context(), competing)
		require.Nil(t, result)
		require.False(t, competingChanged)
		require.ErrorIs(t, competingErr, goldenusecase.ErrGoldenContinuationAlreadyCommitted)
		require.Equal(t, 1, repository.commitCount())
	})

	t.Run("competing CAS creates exactly one successor", func(t *testing.T) {
		local := newTask049ContinuationHarness(t, state, terminal, positions, sentinel)
		commands := [2]goldenusecase.GoldenContinuationCommand{command, command}
		commands[1].CommandID, commands[1].ContinuationID = continuationTask049ID(12240), continuationTask049ID(12241)
		commands[1].NextAttemptID, commands[1].NextAssignmentID = continuationTask049ID(12242), continuationTask049ID(12243)
		commands[1].NextAssignmentRevisionID = continuationTask049ID(12244)
		commands[1].PrivateAssignments = append(
			[]goldenwave.GoldenPrivateAssignmentCommand(nil),
			command.PrivateAssignments...,
		)
		for index := range commands[1].PrivateAssignments {
			commands[1].PrivateAssignments[index].AssignmentID = continuationTask049ID(12250 + index)
		}
		beforeCommit, releaseBarrier := task049ContinuationBarrier(t)
		local.beforeCommit = beforeCommit
		defer releaseBarrier()
		type result struct {
			record  *goldenusecase.GoldenContinuationRecord
			changed bool
			err     error
		}
		results := make([]result, len(commands))
		completed := make(chan struct{}, len(commands))
		for index := range commands {
			go func(index int) {
				defer func() { completed <- struct{}{} }()
				results[index].record, results[index].changed, results[index].err =
					goldenusecase.NewGoldenContinuationUseCase(
						local,
						continuationNewGoldenClock(t, terminal.FinishedAt.Add(time.Second)),
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
			require.ErrorIs(t, result.err, goldenusecase.ErrGoldenContinuationAlreadyCommitted)
			losers++
		}
		require.Equal(t, 1, winners)
		require.Equal(t, 1, losers)
		require.Equal(t, 1, local.commitCount())
	})

	t.Run("stale terminal position plan and Swiss heads write nothing", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*goldenusecase.GoldenContinuationCommand)
		}{
			{name: "terminal", mutate: func(value *goldenusecase.GoldenContinuationCommand) { value.ExpectedTerminalDigest[0] ^= 0xff }},
			{name: "positions", mutate: func(value *goldenusecase.GoldenContinuationCommand) { value.ExpectedPositions.Revision++ }},
			{name: "plan", mutate: func(value *goldenusecase.GoldenContinuationCommand) {
				value.ExpectedPlan.RevisionID = continuationTask049ID(12301)
			}},
			{name: "Swiss", mutate: func(value *goldenusecase.GoldenContinuationCommand) { value.ExpectedSwissPoints.Revision++ }},
		}
		for index, testCase := range tests {
			t.Run(testCase.name, func(t *testing.T) {
				local := newTask049ContinuationHarness(t, state, terminal, positions, sentinel)
				localCommand := command
				localCommand.CommandID = continuationTask049ID(12310 + index*20)
				localCommand.ContinuationID = continuationTask049ID(12311 + index*20)
				localCommand.NextAttemptID = continuationTask049ID(12312 + index*20)
				localCommand.NextAssignmentID = continuationTask049ID(12313 + index*20)
				localCommand.NextAssignmentRevisionID = continuationTask049ID(12314 + index*20)
				testCase.mutate(&localCommand)
				result, localChanged, continueErr := goldenusecase.NewGoldenContinuationUseCase(
					local,
					continuationNewGoldenClock(t, terminal.FinishedAt.Add(time.Second)),
				).Continue(t.Context(), localCommand)
				require.Nil(t, result)
				require.False(t, localChanged)
				require.ErrorIs(t, continueErr, goldenusecase.ErrGoldenContinuationAuthorityConflict)
				require.Zero(t, local.commitCount())
			})
		}
	})

	t.Run("same plan head cannot substitute different reserve edges", func(t *testing.T) {
		planAuthority, planCommand := continuationGoldenPlanExact(t)
		planCommand.GroupCommands[1].EdgeIDs[1] = continuationTask049ID(12480)
		planCommand.GroupCommands[1].ReservationIDs[1] = continuationTask049ID(12481)
		planCommand.GroupCommands[1].SnapshotIDs[1] = continuationTask049ID(12482)
		alternatePlan, buildErr := goldenplan.BuildExactPlan(planCommand, planAuthority)
		require.NoError(t, buildErr)
		require.Equal(t, state.ExactPlan.PlanID, alternatePlan.PlanID)
		require.Equal(t, state.ExactPlan.PlanRevisionID, alternatePlan.PlanRevisionID)
		require.NotEqual(t, state.ExactPlan.Groups[1].Edges[1], alternatePlan.Groups[1].Edges[1])

		local := newTask049ContinuationHarness(t, state, terminal, positions, sentinel)
		local.planOverride = &alternatePlan
		result, localChanged, continueErr := goldenusecase.NewGoldenContinuationUseCase(
			local,
			continuationNewGoldenClock(t, terminal.FinishedAt.Add(time.Second)),
		).Continue(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, continueErr, goldenusecase.ErrGoldenContinuationAuthorityConflict)
		require.Zero(t, local.commitCount())
	})

	t.Run("valid foreign unchanged result is rejected", func(t *testing.T) {
		local := newTask049ContinuationHarness(t, state, terminal, positions, sentinel)
		foreign := record.Snapshot()
		local.returnRecord = &foreign
		local.returnChanged = false
		baseline := command
		baseline.PrivateAssignments = append([]goldenwave.GoldenPrivateAssignmentCommand(nil), command.PrivateAssignments...)
		localCommand := command
		localCommand.PrivateAssignments = append(
			[]goldenwave.GoldenPrivateAssignmentCommand(nil),
			command.PrivateAssignments...,
		)
		localCommand.CommandID, localCommand.ContinuationID = continuationTask049ID(12500), continuationTask049ID(12501)
		localCommand.NextAttemptID, localCommand.NextAssignmentID = continuationTask049ID(12502), continuationTask049ID(12503)
		localCommand.NextAssignmentRevisionID = continuationTask049ID(12504)
		for index := range localCommand.PrivateAssignments {
			localCommand.PrivateAssignments[index].AssignmentID = continuationTask049ID(12520 + index)
		}
		require.Equal(t, baseline, command)

		result, localChanged, continueErr := goldenusecase.NewGoldenContinuationUseCase(
			local,
			continuationNewGoldenClock(t, terminal.FinishedAt.Add(time.Second)),
		).Continue(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, continueErr, domain.ErrInternal)

		changedLocal := newTask049ContinuationHarness(t, state, terminal, positions, sentinel)
		changedTimestamp := record.Snapshot()
		changedTimestamp.CreatedAt = changedTimestamp.CreatedAt.Add(time.Nanosecond)
		task049SealContinuation(t, &changedTimestamp)
		require.NoError(t, changedTimestamp.Validate())
		changedLocal.returnRecord = &changedTimestamp
		changedLocal.returnChanged = true
		result, localChanged, continueErr = goldenusecase.NewGoldenContinuationUseCase(
			changedLocal,
			continuationNewGoldenClock(t, terminal.FinishedAt.Add(time.Second)),
		).Continue(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, continueErr, domain.ErrInternal)
	})

	t.Run("same-command unchanged result cannot change a command head", func(t *testing.T) {
		local := newTask049ContinuationHarness(t, state, terminal, positions, sentinel)
		tampered := record.Snapshot()
		tampered.SwissPoints = continuationTask049SwissPointSentinel(12530)
		task049SealContinuation(t, &tampered)
		require.NoError(t, tampered.Validate())
		local.returnRecord = &tampered
		local.returnChanged = false

		result, localChanged, continueErr := goldenusecase.NewGoldenContinuationUseCase(
			local,
			continuationNewGoldenClock(t, terminal.FinishedAt.Add(time.Second)),
		).Continue(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, continueErr, domain.ErrInternal)
	})

	t.Run("new identities cannot alias retained state or plan roles", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*goldenusecase.GoldenContinuationCommand)
		}{
			{name: "attempt aliases group", mutate: func(value *goldenusecase.GoldenContinuationCommand) {
				value.NextAttemptID = state.Scope.GroupID
			}},
			{name: "private assignment aliases edge", mutate: func(value *goldenusecase.GoldenContinuationCommand) {
				value.PrivateAssignments[0].AssignmentID = state.ExactPlan.Groups[1].Edges[0].ID
			}},
			{name: "continuation aliases active Wave", mutate: func(value *goldenusecase.GoldenContinuationCommand) {
				value.ContinuationID = terminal.Scope.WaveID
			}},
		}
		for index, testCase := range tests {
			t.Run(testCase.name, func(t *testing.T) {
				local := newTask049ContinuationHarness(t, state, terminal, positions, sentinel)
				localCommand := command
				localCommand.PrivateAssignments = append(
					[]goldenwave.GoldenPrivateAssignmentCommand(nil),
					command.PrivateAssignments...,
				)
				localCommand.CommandID, localCommand.ContinuationID = continuationTask049ID(12540+index*20), continuationTask049ID(12541+index*20)
				localCommand.NextAttemptID, localCommand.NextAssignmentID = continuationTask049ID(12542+index*20), continuationTask049ID(12543+index*20)
				localCommand.NextAssignmentRevisionID = continuationTask049ID(12544 + index*20)
				for privateIndex := range localCommand.PrivateAssignments {
					localCommand.PrivateAssignments[privateIndex].AssignmentID = continuationTask049ID(12550 + index*20 + privateIndex)
				}
				testCase.mutate(&localCommand)

				result, localChanged, continueErr := goldenusecase.NewGoldenContinuationUseCase(
					local,
					continuationNewGoldenClock(t, terminal.FinishedAt.Add(time.Second)),
				).Continue(t.Context(), localCommand)
				require.Nil(t, result)
				require.False(t, localChanged)
				require.ErrorIs(t, continueErr, goldenusecase.ErrInvalidGoldenContinuation)
				require.Zero(t, local.commitCount())
			})
		}
	})
}
