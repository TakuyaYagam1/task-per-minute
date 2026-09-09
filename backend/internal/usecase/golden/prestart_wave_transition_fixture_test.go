package golden_test

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type prestartGoldenWaveRepositoryHarness struct {
	*goldenmocks.MockWaveRepository

	mu        sync.Mutex
	state     goldenusecase.GoldenState
	execution *goldenusecase.GoldenWaveExecution
	replays   map[uuid.UUID]goldenusecase.GoldenWaveCommandReplay
}

func prestartNewGoldenWaveRepositoryHarness(
	t *testing.T,
	state goldenusecase.GoldenState,
) *prestartGoldenWaveRepositoryHarness {
	t.Helper()

	harness := &prestartGoldenWaveRepositoryHarness{
		state:   state.Snapshot(),
		replays: make(map[uuid.UUID]goldenusecase.GoldenWaveCommandReplay),
	}
	repository := goldenmocks.NewMockWaveRepository(t)
	repository.EXPECT().
		LoadGoldenState(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, goldenusecase.GoldenStateScope) (goldenusecase.GoldenState, error) {
			harness.mu.Lock()
			defer harness.mu.Unlock()
			return harness.state.Snapshot(), nil
		}).
		Maybe()
	repository.EXPECT().
		FindGoldenWaveCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ uuid.UUID, commandID uuid.UUID) (*goldenusecase.GoldenWaveCommandReplay, error) {
			harness.mu.Lock()
			defer harness.mu.Unlock()
			replay, found := harness.replays[commandID]
			if !found {
				return nil, nil
			}
			clone := replay
			clone.Execution = replay.Execution.Snapshot()
			return &clone, nil
		}).
		Maybe()
	repository.EXPECT().
		LoadGoldenWaveExecution(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, goldenusecase.GoldenStateScope) (*goldenusecase.GoldenWaveExecution, error) {
			harness.mu.Lock()
			defer harness.mu.Unlock()
			if harness.execution == nil {
				return nil, nil
			}
			clone := harness.execution.Snapshot()
			return &clone, nil
		}).
		Maybe()
	repository.EXPECT().
		CommitGoldenWaveExecution(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, commit goldenusecase.GoldenWaveExecutionCommit) (*goldenusecase.GoldenWaveExecution, bool, error) {
			harness.mu.Lock()
			defer harness.mu.Unlock()
			if !harness.state.Expectation().Equal(commit.ExpectedState) {
				return nil, false, domain.ErrConflict
			}
			if commit.ExpectedExecution == nil {
				if harness.execution != nil {
					return nil, false, domain.ErrConflict
				}
			} else if harness.execution == nil || !harness.execution.Expectation().Equal(*commit.ExpectedExecution) {
				return nil, false, domain.ErrConflict
			}
			next := commit.Next.Snapshot()
			harness.execution = &next
			receipt := next.Receipts[len(next.Receipts)-1]
			harness.replays[receipt.CommandID] = goldenusecase.GoldenWaveCommandReplay{
				Receipt: receipt, Execution: next.Snapshot(),
			}
			result := next.Snapshot()
			return &result, true, nil
		}).
		Maybe()
	harness.MockWaveRepository = repository
	return harness
}

func prestartTask048GoldenState(t *testing.T, openedAt time.Time) goldenusecase.GoldenState {
	t.Helper()
	state := prestartGoldenStateFixture(t, openedAt)
	state.Group.Attempts = nil
	state.Windows = nil
	state.Membership.PayloadDigest = [sha256.Size]byte{}
	state.PayloadDigest = [sha256.Size]byte{}
	built, err := goldenusecase.BuildGoldenState(state)
	require.NoError(t, err)
	return built
}

func prestartTask048OpenGoldenExecution(
	t *testing.T,
	repository *prestartGoldenWaveRepositoryHarness,
	state goldenusecase.GoldenState,
	openedAt time.Time,
	base int,
) goldenusecase.GoldenWaveExecution {
	t.Helper()
	participants := state.ActiveParticipantIDs()
	privateAssignments := make([]goldenusecase.GoldenPrivateAssignmentCommand, len(participants))
	for index, participantID := range participants {
		privateAssignments[index] = goldenusecase.GoldenPrivateAssignmentCommand{
			ParticipantID: participantID,
			AssignmentID:  prestartGoldenWaveFixtureID(base + 20 + index),
		}
	}
	command := goldenusecase.OpenGoldenReadyWindowCommand{
		Scope: state.Scope, CommandID: prestartGoldenWaveFixtureID(base), ExpectedState: state.Expectation(),
		AttemptID: prestartGoldenWaveFixtureID(base + 1), WaveID: prestartGoldenWaveFixtureID(base + 2),
		WaveRevisionID:       domain.WaveRevisionID(prestartGoldenWaveFixtureID(base + 3)),
		WindowID:             prestartGoldenWaveFixtureID(base + 4),
		WaveWindowRevisionID: domain.ReadyWindowRevisionID(prestartGoldenWaveFixtureID(base + 5)),
		WindowRevisionID:     prestartGoldenWaveFixtureID(base + 6),
		ReadinessRevisionID:  prestartGoldenWaveFixtureID(base + 7),
		PresenceRevisionID:   prestartGoldenWaveFixtureID(base + 8),
		MembershipID:         prestartGoldenWaveFixtureID(base + 9),
		MembershipRevisionID: prestartGoldenWaveFixtureID(base + 10),
		AssignmentID:         prestartGoldenWaveFixtureID(base + 11),
		AssignmentRevisionID: prestartGoldenWaveFixtureID(base + 12),
		ExecutionRevisionID:  prestartGoldenWaveFixtureID(base + 13),
		PrivateAssignments:   privateAssignments,
	}
	execution, changed, err := goldenusecase.NewGoldenReadyWindowUseCase(
		repository,
		prestartNewWaveFixtureClock(t, openedAt),
	).Open(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	return *execution
}

func prestartTask048ReadyCommand(
	execution goldenusecase.GoldenWaveExecution,
	participantID uuid.UUID,
	base int,
) goldenusecase.GoldenMarkReadyCommand {
	return goldenusecase.GoldenMarkReadyCommand{
		Scope: execution.Scope, CommandID: prestartGoldenWaveFixtureID(base),
		ActorParticipantID: participantID, ParticipantID: participantID,
		AttemptID: execution.Attempt.ID, WaveID: execution.Wave.ID, WindowID: execution.Window.ID,
		ExpectedState: execution.Source, ExpectedExecution: execution.Expectation(),
		NextExecutionRevisionID: prestartGoldenWaveFixtureID(base + 1),
		NextWindowRevisionID:    prestartGoldenWaveFixtureID(base + 2),
		NextReadinessRevisionID: prestartGoldenWaveFixtureID(base + 3),
	}
}
