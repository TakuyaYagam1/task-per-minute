package state_test

import (
	"testing"
	"time"

	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func goldenFallbackCommand(state goldenusecase.GoldenState, base int) goldenusecase.GoldenFallbackCommand {
	return goldenusecase.GoldenFallbackCommand{
		Scope: state.Scope, CommandID: stateTask047ID(base), AllocationID: stateTask047ID(base + 1),
		ExpectedState: state.Expectation(), NextStateRevisionID: stateTask047ID(base + 2),
	}
}

func goldenReadyAndResolve(
	t *testing.T,
	repository *stateGoldenStateRepositoryHarness,
	state goldenusecase.GoldenState,
	readyIDs []uuid.UUID,
	openedAt time.Time,
	base int,
) goldenusecase.GoldenState {
	t.Helper()
	current := state
	for index, participantID := range readyIDs {
		current = stateGoldenAcceptReady(t, repository, current, participantID, openedAt.Add(time.Duration(index+1)*time.Second), base+index*10)
	}
	return stateGoldenResolveNoShow(t, repository, current, openedAt, base+len(readyIDs)*10)
}

func stateGoldenAcceptReady(
	t *testing.T,
	repository *stateGoldenStateRepositoryHarness,
	state goldenusecase.GoldenState,
	participantID uuid.UUID,
	now time.Time,
	base int,
) goldenusecase.GoldenState {
	t.Helper()
	window := state.Windows[0]
	ready, changed, err := goldenusecase.NewGoldenParticipationUseCase(
		repository,
		stateNewGoldenClock(t, now),
	).AcceptReady(t.Context(), goldenusecase.GoldenReadyCommand{
		Scope: state.Scope, CommandID: stateTask047ID(base),
		ActorParticipantID: participantID, ParticipantID: participantID,
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: state.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID: stateTask047ID(base + 1), NextWindowRevisionID: stateTask047ID(base + 2),
		NextReadinessRevisionID: stateTask047ID(base + 3),
	})
	require.NoError(t, err)
	require.True(t, changed)
	return *ready
}

func stateGoldenResolveNoShow(
	t *testing.T,
	repository *stateGoldenStateRepositoryHarness,
	state goldenusecase.GoldenState,
	openedAt time.Time,
	base int,
) goldenusecase.GoldenState {
	t.Helper()
	window := state.Windows[0]
	resolved, changed, err := goldenusecase.NewGoldenNoShowUseCase(
		repository,
		stateNewGoldenClock(t, window.Deadline.Add(time.Nanosecond)),
	).Resolve(t.Context(), goldenusecase.GoldenNoShowCommand{
		Scope: state.Scope, CommandID: stateTask047ID(base), AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: state.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID: stateTask047ID(base + 1), NextWindowRevisionID: stateTask047ID(base + 2),
		NextMembershipRevisionID: stateTask047ID(base + 3),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, resolved.NoShows[0].ResolvedAt.After(openedAt))
	return *resolved
}
