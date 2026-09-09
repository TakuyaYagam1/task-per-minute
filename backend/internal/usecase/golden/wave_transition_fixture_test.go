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
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/mocks"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type continuationGoldenStateRepositoryHarness struct {
	*goldenmocks.MockStateRepository

	mu    sync.Mutex
	state goldenusecase.GoldenState
}

func continuationNewGoldenStateRepository(t *testing.T, initial goldenusecase.GoldenState) *continuationGoldenStateRepositoryHarness {
	t.Helper()

	harness := &continuationGoldenStateRepositoryHarness{state: initial.Snapshot()}
	repository := goldenmocks.NewMockStateRepository(t)
	repository.EXPECT().LoadGoldenState(mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, goldenusecase.GoldenStateScope) (goldenusecase.GoldenState, error) {
			harness.mu.Lock()
			defer harness.mu.Unlock()
			return harness.state.Snapshot(), nil
		},
	).Maybe()
	repository.EXPECT().CommitGoldenState(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, commit goldenusecase.GoldenStateCommit) (*goldenusecase.GoldenState, bool, error) {
			harness.mu.Lock()
			defer harness.mu.Unlock()
			if !commit.Expected.Equal(harness.state.Expectation()) {
				return nil, false, domain.ErrConflict
			}
			harness.state = commit.Next.Snapshot()
			result := harness.state.Snapshot()
			return &result, true, nil
		},
	).Maybe()
	harness.MockStateRepository = repository
	return harness
}

func continuationNewGoldenStateClock(t *testing.T, now time.Time) *goldenmocks.MockStateClock {
	t.Helper()

	clock := goldenmocks.NewMockStateClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func continuationGoldenAcceptReady(
	t *testing.T,
	repository *continuationGoldenStateRepositoryHarness,
	state goldenusecase.GoldenState,
	participantID uuid.UUID,
	now time.Time,
	base int,
) goldenusecase.GoldenState {
	t.Helper()
	window := state.Windows[0]
	ready, changed, err := goldenusecase.NewGoldenParticipationUseCase(
		repository,
		continuationNewGoldenStateClock(t, now),
	).AcceptReady(t.Context(), goldenusecase.GoldenReadyCommand{
		Scope: state.Scope, CommandID: continuationGoldenWaveFixtureID(base),
		ActorParticipantID: participantID, ParticipantID: participantID,
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: state.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID: continuationGoldenWaveFixtureID(base + 1), NextWindowRevisionID: continuationGoldenWaveFixtureID(base + 2),
		NextReadinessRevisionID: continuationGoldenWaveFixtureID(base + 3),
	})
	require.NoError(t, err)
	require.True(t, changed)
	return *ready
}

type continuationGoldenWaveRepositoryHarness struct {
	*goldenmocks.MockWaveRepository

	mu        sync.Mutex
	state     goldenusecase.GoldenState
	execution *goldenusecase.GoldenWaveExecution
	replays   map[uuid.UUID]goldenusecase.GoldenWaveCommandReplay
}

func continuationNewGoldenWaveRepositoryHarness(
	t *testing.T,
	state goldenusecase.GoldenState,
) *continuationGoldenWaveRepositoryHarness {
	t.Helper()

	harness := &continuationGoldenWaveRepositoryHarness{
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

func (r *continuationGoldenWaveRepositoryHarness) setAuthority(authoritydomain.Lease, time.Time) {}

func continuationTask048GoldenState(t *testing.T, openedAt time.Time) goldenusecase.GoldenState {
	t.Helper()
	state := continuationGoldenStateFixture(t, openedAt)
	state.Group.Attempts = nil
	state.Windows = nil
	state.Membership.PayloadDigest = [sha256.Size]byte{}
	state.PayloadDigest = [sha256.Size]byte{}
	built, err := goldenusecase.BuildGoldenState(state)
	require.NoError(t, err)
	return built
}

func continuationTask048OpenGoldenExecution(
	t *testing.T,
	repository *continuationGoldenWaveRepositoryHarness,
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
			AssignmentID:  continuationGoldenWaveFixtureID(base + 20 + index),
		}
	}
	command := goldenusecase.OpenGoldenReadyWindowCommand{
		Scope: state.Scope, CommandID: continuationGoldenWaveFixtureID(base), ExpectedState: state.Expectation(),
		AttemptID: continuationGoldenWaveFixtureID(base + 1), WaveID: continuationGoldenWaveFixtureID(base + 2),
		WaveRevisionID:       domain.WaveRevisionID(continuationGoldenWaveFixtureID(base + 3)),
		WindowID:             continuationGoldenWaveFixtureID(base + 4),
		WaveWindowRevisionID: domain.ReadyWindowRevisionID(continuationGoldenWaveFixtureID(base + 5)),
		WindowRevisionID:     continuationGoldenWaveFixtureID(base + 6),
		ReadinessRevisionID:  continuationGoldenWaveFixtureID(base + 7),
		PresenceRevisionID:   continuationGoldenWaveFixtureID(base + 8),
		MembershipID:         continuationGoldenWaveFixtureID(base + 9),
		MembershipRevisionID: continuationGoldenWaveFixtureID(base + 10),
		AssignmentID:         continuationGoldenWaveFixtureID(base + 11),
		AssignmentRevisionID: continuationGoldenWaveFixtureID(base + 12),
		ExecutionRevisionID:  continuationGoldenWaveFixtureID(base + 13),
		PrivateAssignments:   privateAssignments,
	}
	execution, changed, err := goldenusecase.NewGoldenReadyWindowUseCase(
		repository,
		continuationNewWaveFixtureClock(t, openedAt),
	).Open(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	return *execution
}

func continuationTask048ReadyAll(
	t *testing.T,
	repository *continuationGoldenWaveRepositoryHarness,
	execution goldenusecase.GoldenWaveExecution,
	openedAt time.Time,
	base int,
) goldenusecase.GoldenWaveExecution {
	t.Helper()
	current := execution
	for index, participantID := range execution.Membership.ParticipantIDs {
		command := goldenusecase.GoldenMarkReadyCommand{
			Scope: current.Scope, CommandID: continuationGoldenWaveFixtureID(base + index*10),
			ActorParticipantID: participantID, ParticipantID: participantID,
			AttemptID: current.Attempt.ID, WaveID: current.Wave.ID, WindowID: current.Window.ID,
			ExpectedState: current.Source, ExpectedExecution: current.Expectation(),
			NextExecutionRevisionID: continuationGoldenWaveFixtureID(base + index*10 + 1),
			NextWindowRevisionID:    continuationGoldenWaveFixtureID(base + index*10 + 2),
			NextReadinessRevisionID: continuationGoldenWaveFixtureID(base + index*10 + 3),
		}
		ready, changed, err := goldenusecase.NewGoldenReadinessUseCase(
			repository,
			continuationNewWaveFixtureClock(t, openedAt.Add(time.Duration(index+1)*time.Second)),
		).MarkReady(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		current = *ready
	}
	return current
}

func continuationTask048AuthorityLease(now time.Time, tournamentID uuid.UUID) authoritydomain.Lease {
	return authoritydomain.Lease{
		TournamentID: tournamentID,
		HolderID:     continuationGoldenWaveFixtureID(38001), LeaseID: continuationGoldenWaveFixtureID(38002),
		Epoch: 2, ProcessKind: authoritydomain.ProcessAuthority, Revision: 2,
		CommandID:  continuationGoldenWaveFixtureID(38003),
		Previous:   &authoritydomain.Stamp{LeaseID: continuationGoldenWaveFixtureID(38004), Epoch: 1},
		AcquiredAt: now.Add(-time.Minute), RenewedAt: now.Add(-time.Second),
		ExpiresAt: now.Add(time.Minute),
	}
}

func continuationTask048StartCommand(
	execution goldenusecase.GoldenWaveExecution,
	authority authoritydomain.Lease,
	base int,
) goldenusecase.GoldenStartCommand {
	return goldenusecase.GoldenStartCommand{
		Scope: execution.Scope, CommandID: continuationGoldenWaveFixtureID(base),
		AttemptID: execution.Attempt.ID, WaveID: execution.Wave.ID, WindowID: execution.Window.ID,
		ExpectedState: execution.Source, ExpectedExecution: execution.Expectation(),
		NextExecutionRevisionID: continuationGoldenWaveFixtureID(base + 1),
		NextWindowRevisionID:    continuationGoldenWaveFixtureID(base + 2),
		Authority:               authority,
	}
}

func continuationGoldenResolveNoShow(
	t *testing.T,
	repository *continuationGoldenStateRepositoryHarness,
	state goldenusecase.GoldenState,
	openedAt time.Time,
	base int,
) goldenusecase.GoldenState {
	t.Helper()
	window := state.Windows[0]
	resolved, changed, err := goldenusecase.NewGoldenNoShowUseCase(
		repository,
		continuationNewGoldenClock(t, window.Deadline.Add(time.Nanosecond)),
	).Resolve(t.Context(), goldenusecase.GoldenNoShowCommand{
		Scope: state.Scope, CommandID: continuationGoldenWaveFixtureID(base),
		AttemptID: window.AttemptID, WindowID: window.ID,
		ExpectedState: state.Expectation(), ExpectedWindow: window.Expectation(),
		NextStateRevisionID:      continuationGoldenWaveFixtureID(base + 1),
		NextWindowRevisionID:     continuationGoldenWaveFixtureID(base + 2),
		NextMembershipRevisionID: continuationGoldenWaveFixtureID(base + 3),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, resolved.NoShows[0].ResolvedAt.After(openedAt))
	return *resolved
}
