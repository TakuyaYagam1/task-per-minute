package arena_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenReadyDisconnect(t *testing.T) {
	t.Parallel()

	openedAt := time.Date(2026, 9, 1, 13, 0, 0, 0, time.UTC)
	state := task048GoldenState(t, openedAt)
	repository := &goldenWaveRepositoryFake{state: state}
	execution := task048OpenGoldenExecution(t, repository, state, openedAt, 1000)
	participants := execution.Membership.ParticipantIDs
	readyCommands := make([]arena.GoldenMarkReadyCommand, len(participants))
	for index, participantID := range participants {
		readyCommands[index] = task048ReadyCommand(execution, participantID, 1100+index*10)
		ready, changed, err := arena.NewGoldenReadinessUseCase(
			repository,
			fixedArenaClock{now: openedAt.Add(time.Duration(index+1) * time.Second)},
		).MarkReady(t.Context(), readyCommands[index])
		require.NoError(t, err)
		require.True(t, changed)
		execution = *ready
	}
	require.Equal(t, domain.ArenaWaveStateReady, execution.Wave.State)
	require.Equal(t, participants, execution.Window.ReadyParticipantIDs)

	disconnectedID := participants[0]
	groupBefore := execution.Group
	attemptBefore := execution.Attempt
	assignmentBefore := execution.Assignment
	windowID := execution.Window.ID
	disconnect := task048DisconnectCommand(execution, disconnectedID, 1300)
	disconnected, changed, err := arena.NewGoldenReadinessUseCase(
		repository,
		fixedArenaClock{now: openedAt.Add(10 * time.Second)},
	).Disconnect(t.Context(), disconnect)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.ArenaWaveStateReadyWindowOpen, disconnected.Wave.State)
	require.NotContains(t, disconnected.Window.ReadyParticipantIDs, disconnectedID)
	require.NotContains(t, disconnected.Window.PresentParticipantIDs, disconnectedID)
	for _, participantID := range participants[1:] {
		require.Contains(t, disconnected.Window.ReadyParticipantIDs, participantID)
		require.Contains(t, disconnected.Window.PresentParticipantIDs, participantID)
	}
	require.Equal(t, groupBefore, disconnected.Group)
	require.Equal(t, attemptBefore, disconnected.Attempt)
	require.Equal(t, assignmentBefore, disconnected.Assignment)
	require.Equal(t, windowID, disconnected.Window.ID)

	absentReady := task048ReadyCommand(*disconnected, disconnectedID, 1400)
	result, absentChanged, absentErr := arena.NewGoldenReadinessUseCase(
		repository,
		fixedArenaClock{now: openedAt.Add(11 * time.Second)},
	).MarkReady(t.Context(), absentReady)
	require.Nil(t, result)
	require.False(t, absentChanged)
	require.ErrorIs(t, absentErr, arena.ErrGoldenWaveAuthorityConflict)

	reconnect := task048ReconnectCommand(*disconnected, disconnectedID, 1500)
	reconnected, changed, err := arena.NewGoldenReadinessUseCase(
		repository,
		fixedArenaClock{now: openedAt.Add(12 * time.Second)},
	).Reconnect(t.Context(), reconnect)
	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, reconnected.Window.PresentParticipantIDs, disconnectedID)
	require.NotContains(t, reconnected.Window.ReadyParticipantIDs, disconnectedID)
	require.Equal(t, domain.ArenaWaveStateReadyWindowOpen, reconnected.Wave.State)

	replayed, replayChanged, replayErr := arena.NewGoldenReadinessUseCase(
		repository,
		fixedArenaClock{now: openedAt.Add(13 * time.Second)},
	).MarkReady(t.Context(), readyCommands[0])
	require.NoError(t, replayErr)
	require.False(t, replayChanged)
	require.NotContains(t, replayed.Window.ReadyParticipantIDs, disconnectedID)

	freshReady := task048ReadyCommand(*reconnected, disconnectedID, 1600)
	readyAgain, changed, err := arena.NewGoldenReadinessUseCase(
		repository,
		fixedArenaClock{now: openedAt.Add(14 * time.Second)},
	).MarkReady(t.Context(), freshReady)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.ArenaWaveStateReady, readyAgain.Wave.State)
	require.Equal(t, participants, readyAgain.Window.ReadyParticipantIDs)

	replayedDisconnect, replayChanged, replayErr := arena.NewGoldenReadinessUseCase(
		repository,
		fixedArenaClock{now: openedAt.Add(15 * time.Second)},
	).Disconnect(t.Context(), disconnect)
	require.NoError(t, replayErr)
	require.False(t, replayChanged)
	require.Equal(t, readyAgain.Expectation(), replayedDisconnect.Expectation())

	t.Run("rejects foreign actors stale revisions and expired windows", func(t *testing.T) {
		localState := task048GoldenState(t, openedAt)
		localRepository := &goldenWaveRepositoryFake{state: localState}
		local := task048OpenGoldenExecution(t, localRepository, localState, openedAt, 2000)
		command := task048ReadyCommand(local, local.Membership.ParticipantIDs[0], 2100)
		command.ActorParticipantID = local.Membership.ParticipantIDs[1]
		result, localChanged, applyErr := arena.NewGoldenReadinessUseCase(
			localRepository,
			fixedArenaClock{now: openedAt.Add(time.Second)},
		).MarkReady(t.Context(), command)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, applyErr, domain.ErrArenaAssignmentParticipant)

		stale := task048ReadyCommand(local, local.Membership.ParticipantIDs[0], 2200)
		stale.ExpectedExecution.Window.PresenceRevision++
		result, localChanged, applyErr = arena.NewGoldenReadinessUseCase(
			localRepository,
			fixedArenaClock{now: openedAt.Add(time.Second)},
		).MarkReady(t.Context(), stale)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, applyErr, arena.ErrGoldenWaveAuthorityConflict)

		expired := task048ReadyCommand(local, local.Membership.ParticipantIDs[0], 2300)
		result, localChanged, applyErr = arena.NewGoldenReadinessUseCase(
			localRepository,
			fixedArenaClock{now: openedAt.Add(30*time.Second + time.Nanosecond)},
		).MarkReady(t.Context(), expired)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, applyErr, arena.ErrGoldenReadyWindowClosed)
	})

	t.Run("retains presence on non-ready disconnect and reserves unused identities", func(t *testing.T) {
		localState := task048GoldenState(t, openedAt)
		localRepository := &goldenWaveRepositoryFake{state: localState}
		local := task048OpenGoldenExecution(t, localRepository, localState, openedAt, 2400)
		participantID := local.Membership.ParticipantIDs[0]
		beforeWindow := local.Window.Expectation()
		command := task048DisconnectCommand(local, participantID, 2500)
		disconnected, localChanged, applyErr := arena.NewGoldenReadinessUseCase(
			localRepository,
			fixedArenaClock{now: openedAt.Add(time.Second)},
		).Disconnect(t.Context(), command)
		require.NoError(t, applyErr)
		require.True(t, localChanged, "durable no-op must retain its receipt")
		require.Equal(t, beforeWindow, disconnected.Window.Expectation())
		require.Contains(t, disconnected.Window.PresentParticipantIDs, participantID)
		require.NotContains(t, disconnected.Window.ReadyParticipantIDs, participantID)
		require.Len(t, disconnected.Receipts[len(disconnected.Receipts)-1].UnusedIdentityIDs, 3)

		reusedCommand := task048ReadyCommand(*disconnected, participantID, 2600)
		reusedCommand.CommandID = command.CommandID
		result, reusedChanged, reusedErr := arena.NewGoldenReadinessUseCase(
			localRepository,
			fixedArenaClock{now: openedAt.Add(2 * time.Second)},
		).MarkReady(t.Context(), reusedCommand)
		require.Nil(t, result)
		require.False(t, reusedChanged)
		require.ErrorIs(t, reusedErr, arena.ErrGoldenWaveCommandReuse)

		identityReuse := task048ReadyCommand(*disconnected, participantID, 2700)
		identityReuse.NextWindowRevisionID = command.NextWindowRevisionID
		result, reusedChanged, reusedErr = arena.NewGoldenReadinessUseCase(
			localRepository,
			fixedArenaClock{now: openedAt.Add(2 * time.Second)},
		).MarkReady(t.Context(), identityReuse)
		require.Nil(t, result)
		require.False(t, reusedChanged)
		require.Error(t, reusedErr)
	})

	t.Run("reconciles a readiness winner archived after load", func(t *testing.T) {
		localState := task048GoldenState(t, openedAt)
		localRepository := &goldenWaveRepositoryFake{state: localState}
		local := task048OpenGoldenExecution(t, localRepository, localState, openedAt, 2800)
		participantID := local.Membership.ParticipantIDs[0]
		command := task048ReadyCommand(local, participantID, 2900)

		winnerExecution := local.Snapshot()
		winnerRepository := &goldenWaveRepositoryFake{state: localState, execution: &winnerExecution}
		winner, winnerChanged, winnerErr := arena.NewGoldenReadinessUseCase(
			winnerRepository,
			fixedArenaClock{now: openedAt.Add(time.Second)},
		).MarkReady(t.Context(), command)
		require.NoError(t, winnerErr)
		require.True(t, winnerChanged)

		beforeCommits := localRepository.commitCount()
		localRepository.setStateLoadError(errors.New("readiness replay must stay source-free"))
		localRepository.setPostLoadWinnerArchive(*winner)
		replayed, replayChanged, replayErr := arena.NewGoldenReadinessUseCase(
			localRepository,
			fixedArenaClock{now: openedAt.Add(2 * time.Second)},
		).MarkReady(t.Context(), command)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, winner.Expectation(), replayed.Expectation())
		require.Equal(t, beforeCommits, localRepository.commitCount())
	})

	t.Run("reserves every unused no-op identity", func(t *testing.T) {
		testCases := []struct {
			name      string
			operation string
			field     string
		}{
			{name: "ready window", operation: "ready", field: "window"},
			{name: "ready readiness", operation: "ready", field: "readiness"},
			{name: "disconnect window", operation: "disconnect", field: "window"},
			{name: "disconnect readiness", operation: "disconnect", field: "readiness"},
			{name: "disconnect presence", operation: "disconnect", field: "presence"},
			{name: "reconnect window", operation: "reconnect", field: "window"},
			{name: "reconnect presence", operation: "reconnect", field: "presence"},
		}
		for index, testCase := range testCases {
			t.Run(testCase.name, func(t *testing.T) {
				localState := task048GoldenState(t, openedAt)
				localRepository := &goldenWaveRepositoryFake{state: localState}
				base := 3000 + index*200
				current := task048OpenGoldenExecution(t, localRepository, localState, openedAt, base)
				participantID := current.Membership.ParticipantIDs[0]
				clock := fixedArenaClock{now: openedAt.Add(2 * time.Second)}
				var reservedID uuid.UUID
				var noOp *arena.GoldenWaveExecution
				var noOpChanged bool
				var noOpErr error

				switch testCase.operation {
				case "ready":
					ready, changed, readyErr := arena.NewGoldenReadinessUseCase(
						localRepository,
						fixedArenaClock{now: openedAt.Add(time.Second)},
					).MarkReady(t.Context(), task048ReadyCommand(current, participantID, base+40))
					require.NoError(t, readyErr)
					require.True(t, changed)
					command := task048ReadyCommand(*ready, participantID, base+60)
					if testCase.field == "window" {
						reservedID = command.NextWindowRevisionID
					} else {
						reservedID = command.NextReadinessRevisionID
					}
					noOp, noOpChanged, noOpErr = arena.NewGoldenReadinessUseCase(
						localRepository, clock,
					).MarkReady(t.Context(), command)
				case "disconnect":
					command := task048DisconnectCommand(current, participantID, base+60)
					switch testCase.field {
					case "window":
						reservedID = command.NextWindowRevisionID
					case "readiness":
						reservedID = command.NextReadinessRevisionID
					case "presence":
						reservedID = command.NextPresenceRevisionID
					}
					noOp, noOpChanged, noOpErr = arena.NewGoldenReadinessUseCase(
						localRepository, clock,
					).Disconnect(t.Context(), command)
				case "reconnect":
					command := task048ReconnectCommand(current, participantID, base+60)
					if testCase.field == "window" {
						reservedID = command.NextWindowRevisionID
					} else {
						reservedID = command.NextPresenceRevisionID
					}
					noOp, noOpChanged, noOpErr = arena.NewGoldenReadinessUseCase(
						localRepository, clock,
					).Reconnect(t.Context(), command)
				}
				require.NoError(t, noOpErr)
				require.True(t, noOpChanged)
				require.True(t, localRepository.identityReserved(reservedID))
				beforeCommits := localRepository.commitCount()

				var reuseErr error
				switch testCase.operation {
				case "ready":
					reuse := task048ReadyCommand(*noOp, participantID, base+100)
					if testCase.field == "window" {
						reuse.NextWindowRevisionID = reservedID
					} else {
						reuse.NextReadinessRevisionID = reservedID
					}
					_, _, reuseErr = arena.NewGoldenReadinessUseCase(localRepository, clock).
						MarkReady(t.Context(), reuse)
				case "disconnect":
					reuse := task048DisconnectCommand(*noOp, participantID, base+100)
					switch testCase.field {
					case "window":
						reuse.NextWindowRevisionID = reservedID
					case "readiness":
						reuse.NextReadinessRevisionID = reservedID
					case "presence":
						reuse.NextPresenceRevisionID = reservedID
					}
					_, _, reuseErr = arena.NewGoldenReadinessUseCase(localRepository, clock).
						Disconnect(t.Context(), reuse)
				case "reconnect":
					reuse := task048ReconnectCommand(*noOp, participantID, base+100)
					if testCase.field == "window" {
						reuse.NextWindowRevisionID = reservedID
					} else {
						reuse.NextPresenceRevisionID = reservedID
					}
					_, _, reuseErr = arena.NewGoldenReadinessUseCase(localRepository, clock).
						Reconnect(t.Context(), reuse)
				}
				require.Error(t, reuseErr)
				require.Equal(t, beforeCommits, localRepository.commitCount())
			})
		}
	})

	t.Run("fails closed on a retained expired execution window", func(t *testing.T) {
		localState := task048GoldenState(t, openedAt)
		localRepository := &goldenWaveRepositoryFake{state: localState}
		local := task048OpenGoldenExecution(t, localRepository, localState, openedAt, 4500)
		expired := local.Snapshot()
		expired.Window.State = arena.GoldenReadyWindowExpired
		localRepository.setExecution(expired)
		beforeCommits := localRepository.commitCount()
		result, localChanged, applyErr := arena.NewGoldenReadinessUseCase(
			localRepository,
			fixedArenaClock{now: openedAt.Add(time.Second)},
		).MarkReady(t.Context(), task048ReadyCommand(expired, expired.Membership.ParticipantIDs[0], 4600))
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, applyErr, domain.ErrInternal)
		require.Equal(t, beforeCommits, localRepository.commitCount())
	})
}

func task048OpenGoldenExecution(
	t *testing.T,
	repository *goldenWaveRepositoryFake,
	state arena.GoldenState,
	openedAt time.Time,
	base int,
) arena.GoldenWaveExecution {
	t.Helper()
	execution, changed, err := arena.NewGoldenReadyWindowUseCase(
		repository,
		fixedArenaClock{now: openedAt},
	).Open(t.Context(), task048OpenCommand(state, base))
	require.NoError(t, err)
	require.True(t, changed)
	return *execution
}

func task048ReadyCommand(
	execution arena.GoldenWaveExecution,
	participantID uuid.UUID,
	base int,
) arena.GoldenMarkReadyCommand {
	return arena.GoldenMarkReadyCommand{
		Scope: execution.Scope, CommandID: task048ID(base),
		ActorParticipantID: participantID, ParticipantID: participantID,
		AttemptID: execution.Attempt.ID, WaveID: execution.Wave.ID, WindowID: execution.Window.ID,
		ExpectedState: execution.Source, ExpectedExecution: execution.Expectation(),
		NextExecutionRevisionID: task048ID(base + 1), NextWindowRevisionID: task048ID(base + 2),
		NextReadinessRevisionID: task048ID(base + 3),
	}
}

func task048DisconnectCommand(
	execution arena.GoldenWaveExecution,
	participantID uuid.UUID,
	base int,
) arena.GoldenReadyDisconnectCommand {
	return arena.GoldenReadyDisconnectCommand{
		Scope: execution.Scope, CommandID: task048ID(base), ParticipantID: participantID,
		AttemptID: execution.Attempt.ID, WaveID: execution.Wave.ID, WindowID: execution.Window.ID,
		ExpectedState: execution.Source, ExpectedExecution: execution.Expectation(),
		NextExecutionRevisionID: task048ID(base + 1), NextWindowRevisionID: task048ID(base + 2),
		NextReadinessRevisionID: task048ID(base + 3), NextPresenceRevisionID: task048ID(base + 4),
	}
}

func task048ReconnectCommand(
	execution arena.GoldenWaveExecution,
	participantID uuid.UUID,
	base int,
) arena.GoldenReconnectCommand {
	return arena.GoldenReconnectCommand{
		Scope: execution.Scope, CommandID: task048ID(base),
		ActorParticipantID: participantID, ParticipantID: participantID,
		AttemptID: execution.Attempt.ID, WaveID: execution.Wave.ID, WindowID: execution.Window.ID,
		ExpectedState: execution.Source, ExpectedExecution: execution.Expectation(),
		NextExecutionRevisionID: task048ID(base + 1), NextWindowRevisionID: task048ID(base + 2),
		NextPresenceRevisionID: task048ID(base + 3),
	}
}
