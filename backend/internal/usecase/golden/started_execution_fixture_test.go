package golden_test

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/wave/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type continuationStartedWaveFixtureState struct {
	mu        sync.Mutex
	state     goldenusecase.GoldenState
	execution *goldenusecase.GoldenWaveExecution
	replays   map[uuid.UUID]goldenusecase.GoldenWaveCommandReplay
}

func continuationNewStartedGoldenFixture(
	t *testing.T,
	startedAt time.Time,
) (goldenusecase.GoldenState, goldenusecase.GoldenWaveExecution) {
	t.Helper()

	openedAt := startedAt.Add(-20 * time.Second)
	state := continuationGoldenStateFixture(t, openedAt)
	state.Group.Attempts = nil
	state.Windows = nil
	state.Membership.PayloadDigest = [sha256.Size]byte{}
	state.PayloadDigest = [sha256.Size]byte{}
	state, err := goldenusecase.BuildGoldenState(state)
	require.NoError(t, err)

	fixture := &continuationStartedWaveFixtureState{
		state:   state.Snapshot(),
		replays: make(map[uuid.UUID]goldenusecase.GoldenWaveCommandReplay),
	}
	repository := goldenmocks.NewMockWaveRepository(t)
	repository.EXPECT().
		LoadGoldenState(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, goldenusecase.GoldenStateScope) (goldenusecase.GoldenState, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			return fixture.state.Snapshot(), nil
		}).
		Maybe()
	repository.EXPECT().
		FindGoldenWaveCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ uuid.UUID, commandID uuid.UUID) (*goldenusecase.GoldenWaveCommandReplay, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			replay, found := fixture.replays[commandID]
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
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.execution == nil {
				return nil, nil
			}
			clone := fixture.execution.Snapshot()
			return &clone, nil
		}).
		Maybe()
	repository.EXPECT().
		CommitGoldenWaveExecution(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, commit goldenusecase.GoldenWaveExecutionCommit) (*goldenusecase.GoldenWaveExecution, bool, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if !fixture.state.Expectation().Equal(commit.ExpectedState) {
				return nil, false, domain.ErrConflict
			}
			if commit.ExpectedExecution == nil {
				if fixture.execution != nil {
					return nil, false, domain.ErrConflict
				}
			} else if fixture.execution == nil || !fixture.execution.Expectation().Equal(*commit.ExpectedExecution) {
				return nil, false, domain.ErrConflict
			}
			next := commit.Next.Snapshot()
			fixture.execution = &next
			receipt := next.Receipts[len(next.Receipts)-1]
			fixture.replays[receipt.CommandID] = goldenusecase.GoldenWaveCommandReplay{
				Receipt: receipt, Execution: next.Snapshot(),
			}
			result := next.Snapshot()
			return &result, true, nil
		}).
		Maybe()

	participants := state.ActiveParticipantIDs()
	privateAssignments := make([]goldenusecase.GoldenPrivateAssignmentCommand, len(participants))
	for index, participantID := range participants {
		privateAssignments[index] = goldenusecase.GoldenPrivateAssignmentCommand{
			ParticipantID: participantID,
			AssignmentID:  continuationGoldenWaveFixtureID(20020 + index),
		}
	}
	openCommand := goldenusecase.OpenGoldenReadyWindowCommand{
		Scope: state.Scope, CommandID: continuationGoldenWaveFixtureID(20000), ExpectedState: state.Expectation(),
		AttemptID: continuationGoldenWaveFixtureID(20001), WaveID: continuationGoldenWaveFixtureID(20002),
		WaveRevisionID:       domain.WaveRevisionID(continuationGoldenWaveFixtureID(20003)),
		WindowID:             continuationGoldenWaveFixtureID(20004),
		WaveWindowRevisionID: domain.ReadyWindowRevisionID(continuationGoldenWaveFixtureID(20005)),
		WindowRevisionID:     continuationGoldenWaveFixtureID(20006), ReadinessRevisionID: continuationGoldenWaveFixtureID(20007),
		PresenceRevisionID: continuationGoldenWaveFixtureID(20008), MembershipID: continuationGoldenWaveFixtureID(20009),
		MembershipRevisionID: continuationGoldenWaveFixtureID(20010), AssignmentID: continuationGoldenWaveFixtureID(20011),
		AssignmentRevisionID: continuationGoldenWaveFixtureID(20012), ExecutionRevisionID: continuationGoldenWaveFixtureID(20013),
		PrivateAssignments: privateAssignments,
	}
	opened, changed, err := goldenusecase.NewGoldenReadyWindowUseCase(
		repository,
		continuationNewWaveFixtureClock(t, openedAt),
	).Open(t.Context(), openCommand)
	require.NoError(t, err)
	require.True(t, changed)

	current := *opened
	for index, participantID := range current.Membership.ParticipantIDs {
		readyCommand := goldenusecase.GoldenMarkReadyCommand{
			Scope: current.Scope, CommandID: continuationGoldenWaveFixtureID(20100 + index*10),
			ActorParticipantID: participantID, ParticipantID: participantID,
			AttemptID: current.Attempt.ID, WaveID: current.Wave.ID, WindowID: current.Window.ID,
			ExpectedState: current.Source, ExpectedExecution: current.Expectation(),
			NextExecutionRevisionID: continuationGoldenWaveFixtureID(20101 + index*10),
			NextWindowRevisionID:    continuationGoldenWaveFixtureID(20102 + index*10),
			NextReadinessRevisionID: continuationGoldenWaveFixtureID(20103 + index*10),
		}
		ready, readyChanged, readyErr := goldenusecase.NewGoldenReadinessUseCase(
			repository,
			continuationNewWaveFixtureClock(t, openedAt.Add(time.Duration(index+1)*time.Second)),
		).MarkReady(t.Context(), readyCommand)
		require.NoError(t, readyErr)
		require.True(t, readyChanged)
		current = *ready
	}

	authority := authoritydomain.Lease{
		TournamentID: state.Scope.TournamentID,
		HolderID:     continuationGoldenWaveFixtureID(28001), LeaseID: continuationGoldenWaveFixtureID(28002),
		Epoch: 2, ProcessKind: authoritydomain.ProcessAuthority, Revision: 2,
		CommandID:  continuationGoldenWaveFixtureID(28003),
		Previous:   &authoritydomain.Stamp{LeaseID: continuationGoldenWaveFixtureID(28004), Epoch: 1},
		AcquiredAt: startedAt.Add(-time.Minute), RenewedAt: startedAt.Add(-time.Second),
		ExpiresAt: startedAt.Add(time.Minute),
	}
	started, startChanged, err := goldenusecase.NewGoldenStartUseCase(
		repository,
		continuationNewWaveFixtureClock(t, startedAt),
	).Start(t.Context(), goldenusecase.GoldenStartCommand{
		Scope: current.Scope, CommandID: continuationGoldenWaveFixtureID(20200),
		AttemptID: current.Attempt.ID, WaveID: current.Wave.ID, WindowID: current.Window.ID,
		ExpectedState: current.Source, ExpectedExecution: current.Expectation(),
		NextExecutionRevisionID: continuationGoldenWaveFixtureID(20201), NextWindowRevisionID: continuationGoldenWaveFixtureID(20202),
		Authority: authority,
	})
	require.NoError(t, err)
	require.True(t, startChanged)
	return state, *started
}

func continuationNewWaveFixtureClock(t *testing.T, now time.Time) *goldenmocks.MockWaveClock {
	t.Helper()
	clock := goldenmocks.NewMockWaveClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func continuationTask049StartedFixture(t *testing.T, startedAt time.Time) (goldenusecase.GoldenState, goldenusecase.GoldenWaveExecution) {
	t.Helper()
	return continuationNewStartedGoldenFixture(t, startedAt)
}

func continuationGoldenWaveFixtureID(value int) uuid.UUID {
	return uuid.MustParse("48000000-0000-0000-0000-" + continuationGoldenWaveFixtureSuffix(value))
}

func continuationGoldenWaveFixtureSuffix(value int) string {
	const digits = "000000000000"
	encoded := []byte(digits)
	for index := len(encoded) - 1; value > 0; index-- {
		encoded[index] = byte('0' + value%10)
		value /= 10
	}
	return string(encoded)
}

func continuationTask049ID(value int) uuid.UUID {
	return uuid.MustParse("49000000-0000-0000-0000-" + continuationTask049Suffix(value))
}

func continuationTask049Suffix(value int) string { return continuationGoldenWaveFixtureSuffix(value) }
