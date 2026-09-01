package arena_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenReserveExhaustion(t *testing.T) {
	t.Parallel()

	failedAt := time.Date(2026, 9, 1, 20, 0, 0, 0, time.UTC)
	authority := task050GoldenFailureAuthority(t, failedAt, true)
	require.True(t, authority.Classification.Exhausted)
	require.Nil(t, authority.Classification.NextEdge)
	repository := newTask050FailureRepository(authority)
	unrelatedBefore := repository.unrelatedSnapshot()
	command := arena.GoldenReserveExhaustionCommand{
		Scope: authority.Active.Scope, CommandID: task049ID(23000), FailureID: task049ID(23001),
		Expected:               authority.Expectation(),
		ClosedWaveRevisionID:   domain.ArenaWaveRevisionID(task049ID(23002)),
		RequiredOperatorAction: "replace the Golden task chain and resume this group",
	}

	record, changed, err := arena.NewGoldenReserveExhaustionUseCase(
		repository,
		fixedArenaClock{now: failedAt},
	).Pause(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, record.Validate())
	require.Equal(t, arena.GoldenFailureRouteExhausted, record.Route)
	require.Equal(t, domain.ArenaGoldenAttemptStateVoid, record.Attempt.State)
	require.Equal(t, domain.ArenaWaveStateCompleted, record.OldWave.State)
	require.Equal(t, authority.Positions, record.Positions)
	require.Equal(t, authority.Positions, record.PriorPositions)
	require.Nil(t, record.Replacement)
	require.NotNil(t, record.TechnicalPause)
	require.Equal(t, arena.GoldenGroupStateTechnicalPause, record.TechnicalPause.State)
	require.Equal(t, authority.Active.Scope.State.GroupID, record.TechnicalPause.GroupID)
	require.Equal(t, command.RequiredOperatorAction, record.TechnicalPause.RequiredOperatorAction)
	require.Equal(t, arena.GoldenFailureClassificationExhausted, record.Classification.Kind)
	require.Equal(t, unrelatedBefore, repository.unrelatedSnapshot())
	for _, unrelated := range repository.unrelatedSnapshot() {
		require.NotEqual(t, record.TechnicalPause.GroupID, unrelated.GroupID)
	}

	replayed, replayChanged, replayErr := arena.NewGoldenReserveExhaustionUseCase(
		repository,
		fixedArenaClock{now: failedAt.Add(time.Hour)},
	).Pause(t.Context(), command)
	require.NoError(t, replayErr)
	require.False(t, replayChanged)
	require.Equal(t, record.PayloadDigest, replayed.PayloadDigest)

	falseAuthority := task050GoldenFailureAuthority(t, failedAt, false)
	falseRepository := newTask050FailureRepository(falseAuthority)
	falseCommand := command
	falseCommand.Scope = falseAuthority.Active.Scope
	falseCommand.CommandID = task049ID(23010)
	falseCommand.FailureID = task049ID(23011)
	falseCommand.Expected = falseAuthority.Expectation()
	falseCommand.ClosedWaveRevisionID = domain.ArenaWaveRevisionID(task049ID(23012))
	result, falseChanged, falseErr := arena.NewGoldenReserveExhaustionUseCase(
		falseRepository,
		fixedArenaClock{now: failedAt},
	).Pause(t.Context(), falseCommand)
	require.Nil(t, result)
	require.False(t, falseChanged)
	require.ErrorIs(t, falseErr, arena.ErrGoldenFailureRouteConflict)
	require.Equal(t, 0, falseRepository.writeCount())
}

func task050ExhaustedActiveExecution(
	t *testing.T,
	state arena.GoldenState,
	active arena.GoldenFailureActiveExecution,
	startedAt time.Time,
) arena.GoldenFailureActiveExecution {
	t.Helper()
	all := append([]uuid.UUID(nil), active.ParticipantIDs...)
	require.GreaterOrEqual(t, len(all), 3)
	participants := append([]uuid.UUID(nil), all[1:]...)
	firstFinished := startedAt.Add(-2 * time.Minute)
	secondStarted := startedAt.Add(-90 * time.Second)
	secondFinished := startedAt.Add(-time.Minute)
	first := active.Attempt
	first.ID = task049ID(23100)
	first.AttemptNo = 1
	first.PreviousAttemptID = nil
	first.State = domain.ArenaGoldenAttemptStateCompleted
	first.StartedAt = &firstFinished
	first.FinishedAt = &secondStarted
	first.ParticipantIDs = all
	second := active.Attempt
	second.ID = task049ID(23101)
	second.AttemptNo = 2
	second.PreviousAttemptID = &first.ID
	second.State = domain.ArenaGoldenAttemptStateVoid
	second.StartedAt = &secondStarted
	second.FinishedAt = &secondFinished
	second.ParticipantIDs = participants
	third := active.Attempt
	third.ID = task049ID(23102)
	third.AttemptNo = 3
	third.PreviousAttemptID = &second.ID
	third.State = domain.ArenaGoldenAttemptStateActive
	third.StartedAt = &startedAt
	third.FinishedAt = nil
	third.ParticipantIDs = participants
	active.Attempt = third
	active.Group.Attempts = []domain.ArenaGoldenAttempt{first, second, third}
	active.Scope.AttemptID = third.ID
	active.Expectation.AttemptID = third.ID
	edge := state.ExactPlan.Groups[1].Edges[2]
	active.Scope.SnapshotID = edge.Snapshot.SnapshotID
	active.Scope.TaskID = edge.Snapshot.TaskID
	active.Assignment.AttemptID = third.ID
	active.Assignment.EdgeID = edge.ID
	active.Assignment.ReservationID = edge.ReservationID
	active.Assignment.SnapshotID = edge.Snapshot.SnapshotID
	active.Assignment.TaskID = edge.Snapshot.TaskID
	active.Assignment.ContentDigest = edge.ContentDigest
	active.Assignment.ExecutionPayloadDigest = task049GobDigest(t, "third execution")
	active.Assignment.Private = append([]arena.GoldenPrivateAssignment(nil), active.Assignment.Private[1:]...)
	for index := range active.Assignment.Private {
		active.Assignment.Private[index].SnapshotID = edge.Snapshot.SnapshotID
		active.Assignment.Private[index].ContentDigest = edge.ContentDigest
	}
	task049SealAttemptAssignmentEvidence(t, &active.Assignment)
	active.Expectation.AssignmentDigest = active.Assignment.ExecutionPayloadDigest
	active.Expectation.AssignmentID = active.Assignment.ID
	active.Expectation.AssignmentRevisionID = active.Assignment.RevisionID
	active.Expectation.PayloadDigest = task049GobDigest(t, "third active head")
	active.Expectation.MembershipDigest = task049GobDigest(t, participants)
	active.Expectation.Window.ReadinessDigest = task049GobDigest(t, participants)
	active.Expectation.Window.PresenceDigest = task049GobDigest(t, participants)
	active.Wave.Members = make([]domain.ArenaWaveMember, len(participants))
	for index, participantID := range participants {
		active.Wave.Members[index] = domain.ArenaWaveMember{ParticipantID: participantID, Ready: true}
	}
	active.StartedAt = startedAt
	active.Deadline = startedAt.Add(time.Minute)
	active.ParticipantIDs = participants
	require.NoError(t, active.Validate())
	return active
}
