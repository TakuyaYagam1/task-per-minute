package arena_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenReadyWindow(t *testing.T) {
	t.Parallel()

	openedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	state := task048GoldenState(t, openedAt)
	repository := &goldenWaveRepositoryFake{state: state}
	command := task048OpenCommand(state, 100)

	execution, changed, err := arena.NewGoldenReadyWindowUseCase(
		repository,
		fixedArenaClock{now: openedAt},
	).Open(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, openedAt, execution.OpenedAt)
	require.Equal(t, openedAt.Add(30*time.Second), execution.Deadline)
	require.Equal(t, command.AttemptID, execution.Attempt.ID)
	require.Equal(t, domain.ArenaGoldenAttemptStateWaitingReady, execution.Attempt.State)
	require.Equal(t, command.WaveID, execution.Wave.ID)
	require.Equal(t, domain.ArenaWaveStateReadyWindowOpen, execution.Wave.State)
	require.Equal(t, command.WindowID, execution.Window.ID)
	require.Equal(t, arena.GoldenReadyWindowOpen, execution.Window.State)
	require.Equal(t, state.ActiveParticipantIDs(), execution.Attempt.ParticipantIDs)
	require.Equal(t, state.ActiveParticipantIDs(), execution.Membership.ParticipantIDs)
	require.Equal(t, state.ActiveParticipantIDs(), execution.Window.PresentParticipantIDs)
	require.Empty(t, execution.Window.ReadyParticipantIDs)
	require.Len(t, execution.Group.Attempts, 1)
	require.Equal(t, command.AttemptID, execution.Group.Attempts[0].ID)
	require.Equal(t, state.Plan, execution.Assignment.Plan)
	require.Equal(t, state.ExactPlan.Groups[1].Edges[0].ID, execution.Assignment.EdgeID)
	require.Equal(t, state.ExactPlan.Groups[1].Edges[0].ContentDigest, execution.Assignment.ContentDigest)
	require.Len(t, execution.Assignment.Private, len(state.ActiveParticipantIDs()))
	for index, private := range execution.Assignment.Private {
		require.Equal(t, command.PrivateAssignments[index].ParticipantID, private.ParticipantID)
		require.Equal(t, command.PrivateAssignments[index].AssignmentID, private.ID)
		require.Equal(t, execution.Assignment.Snapshot.SnapshotID, private.SnapshotID)
		require.Equal(t, execution.Assignment.ContentDigest, private.ContentDigest)
	}
	require.Len(t, execution.Receipts, 1)
	require.Equal(t, arena.GoldenWaveCommandOpened, execution.Receipts[0].Kind)
	require.Equal(t, 1, repository.commitCount())

	execution.Group.Members[0].Excluded = true
	execution.Assignment.Snapshot.Title = "tampered"
	execution.Window.PresentParticipantIDs[0] = task048ID(9990)
	reloaded, loadErr := repository.LoadGoldenWaveExecution(t.Context(), state.Scope)
	require.NoError(t, loadErr)
	require.False(t, reloaded.Group.Members[0].Excluded)
	require.NotEqual(t, "tampered", reloaded.Assignment.Snapshot.Title)
	require.NotEqual(t, task048ID(9990), reloaded.Window.PresentParticipantIDs[0])

	t.Run("replays before source authority drift", func(t *testing.T) {
		repository.setStateLoadError(errors.New("source drift must not be read on replay"))
		replayed, replayChanged, replayErr := arena.NewGoldenReadyWindowUseCase(
			repository,
			fixedArenaClock{now: openedAt.Add(time.Minute)},
		).Open(t.Context(), command)
		require.NoError(t, replayErr)
		require.False(t, replayChanged)
		require.Equal(t, command.AttemptID, replayed.Attempt.ID)
		require.Equal(t, 1, repository.commitCount())
		repository.setStateLoadError(nil)

		reused := command
		reused.AttemptID = task048ID(9980)
		result, reusedChanged, reusedErr := arena.NewGoldenReadyWindowUseCase(
			repository,
			fixedArenaClock{now: openedAt},
		).Open(t.Context(), reused)
		require.Nil(t, result)
		require.False(t, reusedChanged)
		require.ErrorIs(t, reusedErr, arena.ErrGoldenWaveCommandReuse)
	})

	t.Run("reconciles an archived winner after the initial replay lookup", func(t *testing.T) {
		localState := task048GoldenState(t, openedAt)
		localRepository := &goldenWaveRepositoryFake{state: localState}
		localCommand := task048OpenCommand(localState, 450)
		opened, localChanged, openErr := arena.NewGoldenReadyWindowUseCase(
			localRepository,
			fixedArenaClock{now: openedAt},
		).Open(t.Context(), localCommand)
		require.NoError(t, openErr)
		require.True(t, localChanged)
		localRepository.archiveBeforeNextLoadAfterHiddenFind()
		replayed, localChanged, replayErr := arena.NewGoldenReadyWindowUseCase(
			localRepository,
			fixedArenaClock{now: openedAt.Add(time.Second)},
		).Open(t.Context(), localCommand)
		require.NoError(t, replayErr)
		require.False(t, localChanged)
		require.Equal(t, opened.Expectation(), replayed.Expectation())
	})

	t.Run("requires two active unresolved members and exact revisions", func(t *testing.T) {
		oneActive := task048SoloGoldenState(t, openedAt)
		localRepository := &goldenWaveRepositoryFake{state: oneActive}
		result, localChanged, openErr := arena.NewGoldenReadyWindowUseCase(
			localRepository,
			fixedArenaClock{now: openedAt},
		).Open(t.Context(), task048OpenCommand(oneActive, 500))
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, openErr, arena.ErrGoldenReadyWindowIneligible)
		require.Zero(t, localRepository.commitCount())

		stale := task048OpenCommand(state, 600)
		stale.ExpectedState.Membership.Revision++
		staleRepository := &goldenWaveRepositoryFake{state: state}
		result, localChanged, openErr = arena.NewGoldenReadyWindowUseCase(
			staleRepository,
			fixedArenaClock{now: openedAt},
		).Open(t.Context(), stale)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, openErr, arena.ErrGoldenWaveAuthorityConflict)
		require.Zero(t, staleRepository.commitCount())

		readyBase := goldenStateFixture(t, openedAt.Add(-5*time.Second))
		readyRepository := &goldenStateRepositoryFake{state: readyBase}
		resolved := goldenAcceptReady(
			t,
			readyRepository,
			readyBase,
			readyBase.Group.Members[0].ParticipantID,
			openedAt.Add(-4*time.Second),
			640,
		)
		participants := resolved.ActiveParticipantIDs()
		firstID := resolved.Group.Attempts[0].ID
		started := openedAt.Add(-2 * time.Minute)
		finished := openedAt.Add(-time.Minute)
		resolved.Group.Attempts = []domain.ArenaGoldenAttempt{
			{
				ID: firstID, GroupID: resolved.Group.ID, GroupRevisionID: resolved.Group.RevisionID,
				AttemptNo: 1, State: domain.ArenaGoldenAttemptStateCompleted,
				ParticipantIDs: append([]uuid.UUID(nil), participants...),
				StartedAt:      &started, FinishedAt: &finished,
			},
			{
				ID: task048ID(651), GroupID: resolved.Group.ID, GroupRevisionID: resolved.Group.RevisionID,
				AttemptNo: 2, PreviousAttemptID: &firstID, State: domain.ArenaGoldenAttemptStateCancelled,
				ParticipantIDs: append([]uuid.UUID(nil), participants...), FinishedAt: &finished,
			},
		}
		resolved.PayloadDigest = [32]byte{}
		resolved, buildErr := arena.BuildGoldenState(resolved)
		require.NoError(t, buildErr)
		resolvedRepository := &goldenWaveRepositoryFake{state: resolved}
		result, localChanged, openErr = arena.NewGoldenReadyWindowUseCase(
			resolvedRepository,
			fixedArenaClock{now: openedAt},
		).Open(t.Context(), task048OpenCommand(resolved, 660))
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, openErr, arena.ErrGoldenReadyWindowIneligible)
		require.Zero(t, resolvedRepository.commitCount())
	})

	t.Run("rejects existing unresolved attempts and identity aliases", func(t *testing.T) {
		withAttempt := goldenStateFixture(t, openedAt)
		attemptRepository := &goldenWaveRepositoryFake{state: withAttempt}
		result, localChanged, openErr := arena.NewGoldenReadyWindowUseCase(
			attemptRepository,
			fixedArenaClock{now: openedAt},
		).Open(t.Context(), task048OpenCommand(withAttempt, 700))
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, openErr, arena.ErrGoldenReadyWindowIneligible)

		aliased := task048OpenCommand(state, 800)
		aliased.AssignmentID = state.Plan.PlanID
		aliasRepository := &goldenWaveRepositoryFake{state: state}
		result, localChanged, openErr = arena.NewGoldenReadyWindowUseCase(
			aliasRepository,
			fixedArenaClock{now: openedAt},
		).Open(t.Context(), aliased)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, openErr, arena.ErrInvalidGoldenWaveExecution)
		require.Zero(t, aliasRepository.commitCount())
	})

	t.Run("rejects an identity reserved by an archived execution", func(t *testing.T) {
		localState := task048GoldenState(t, openedAt)
		localCommand := task048OpenCommand(localState, 850)
		localRepository := &goldenWaveRepositoryFake{
			state: localState,
			reserved: map[uuid.UUID]struct{}{
				localCommand.WaveID: {},
			},
		}
		result, localChanged, openErr := arena.NewGoldenReadyWindowUseCase(
			localRepository,
			fixedArenaClock{now: openedAt},
		).Open(t.Context(), localCommand)
		require.Nil(t, result)
		require.False(t, localChanged)
		require.ErrorIs(t, openErr, arena.ErrGoldenWaveIdentityConflict)
		require.Zero(t, localRepository.commitCount())
	})
}

func task048GoldenState(t *testing.T, openedAt time.Time) arena.GoldenState {
	t.Helper()
	state := goldenStateFixture(t, openedAt)
	state.Group.Attempts = nil
	state.Windows = nil
	state.Membership.PayloadDigest = [32]byte{}
	state.PayloadDigest = [32]byte{}
	built, err := arena.BuildGoldenState(state)
	require.NoError(t, err)
	return built
}

func task048SoloGoldenState(t *testing.T, openedAt time.Time) arena.GoldenState {
	t.Helper()
	state := goldenStateFixture(t, openedAt)
	repository := &goldenStateRepositoryFake{state: state}
	ready := goldenAcceptReady(t, repository, state, state.Group.Members[0].ParticipantID, openedAt.Add(time.Second), 9000)
	resolved, changed, err := arena.NewGoldenNoShowUseCase(
		repository,
		fixedArenaClock{now: openedAt.Add(31 * time.Second)},
	).Resolve(t.Context(), goldenNoShowCommand(ready, 9010))
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, resolved.ActiveParticipantIDs(), 1)
	return *resolved
}

func task048OpenCommand(state arena.GoldenState, base int) arena.OpenGoldenReadyWindowCommand {
	participants := state.ActiveParticipantIDs()
	private := make([]arena.GoldenPrivateAssignmentCommand, len(participants))
	for index, participantID := range participants {
		private[index] = arena.GoldenPrivateAssignmentCommand{
			ParticipantID: participantID,
			AssignmentID:  task048ID(base + 20 + index),
		}
	}
	return arena.OpenGoldenReadyWindowCommand{
		Scope: state.Scope, CommandID: task048ID(base), ExpectedState: state.Expectation(),
		AttemptID: task048ID(base + 1), WaveID: task048ID(base + 2),
		WaveRevisionID:       domain.ArenaWaveRevisionID(task048ID(base + 3)),
		WindowID:             task048ID(base + 4),
		WaveWindowRevisionID: domain.ArenaReadyWindowRevisionID(task048ID(base + 5)),
		WindowRevisionID:     task048ID(base + 6), ReadinessRevisionID: task048ID(base + 7),
		PresenceRevisionID: task048ID(base + 8), MembershipID: task048ID(base + 9),
		MembershipRevisionID: task048ID(base + 10), AssignmentID: task048ID(base + 11),
		AssignmentRevisionID: task048ID(base + 12), ExecutionRevisionID: task048ID(base + 13),
		PrivateAssignments: private,
	}
}

type goldenWaveRepositoryFake struct {
	mu                        sync.Mutex
	state                     arena.GoldenState
	execution                 *arena.GoldenWaveExecution
	commits                   int
	stateLoadErr              error
	commitErr                 error
	authority                 *arena.ExecutionAuthorityLease
	transactionTime           time.Time
	barrier                   *goldenLoadBarrier
	reserved                  map[uuid.UUID]struct{}
	replayExecution           map[uuid.UUID]arena.GoldenWaveExecution
	hideReplayFind            int
	archiveBeforeLoad         bool
	postLoadWinner            *arena.GoldenWaveExecution
	authorityRevisionOverride *int64
	authorityDigestOverride   *[32]byte
}

func (r *goldenWaveRepositoryFake) FindGoldenWaveCommand(
	_ context.Context,
	_ uuid.UUID,
	commandID uuid.UUID,
) (*arena.GoldenWaveCommandReplay, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hideReplayFind > 0 {
		r.hideReplayFind--
		return nil, nil
	}
	if replay, found := r.replayExecution[commandID]; found {
		for _, receipt := range replay.Receipts {
			if receipt.CommandID == commandID {
				return &arena.GoldenWaveCommandReplay{
					Receipt: receipt, Execution: replay.Snapshot(),
				}, nil
			}
		}
		return nil, domain.ErrInternal
	}
	if r.execution == nil {
		return nil, nil
	}
	for _, receipt := range r.execution.Receipts {
		if receipt.CommandID == commandID {
			return &arena.GoldenWaveCommandReplay{
				Receipt: receipt, Execution: r.execution.Snapshot(),
			}, nil
		}
	}
	return nil, nil
}

func (r *goldenWaveRepositoryFake) LoadGoldenWaveExecution(
	_ context.Context,
	_ arena.GoldenStateScope,
) (*arena.GoldenWaveExecution, error) {
	r.mu.Lock()
	barrier := r.barrier
	if r.archiveBeforeLoad {
		r.archiveBeforeLoad = false
		r.execution = nil
		r.mu.Unlock()
		return nil, nil
	}
	if r.execution == nil {
		r.mu.Unlock()
		return nil, nil
	}
	clone := r.execution.Snapshot()
	if r.postLoadWinner != nil {
		winner := r.postLoadWinner.Snapshot()
		r.retainReplayExecution(winner)
		r.execution = nil
		r.postLoadWinner = nil
	}
	r.mu.Unlock()
	if barrier != nil {
		barrier.wait()
	}
	return &clone, nil
}

func (r *goldenWaveRepositoryFake) LoadGoldenState(
	_ context.Context,
	_ arena.GoldenStateScope,
) (arena.GoldenState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stateLoadErr != nil {
		return arena.GoldenState{}, r.stateLoadErr
	}
	return r.state.Snapshot(), nil
}

func (r *goldenWaveRepositoryFake) CommitGoldenWaveExecution(
	_ context.Context,
	commit arena.GoldenWaveExecutionCommit,
) (*arena.GoldenWaveExecution, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.commitErr != nil {
		return nil, false, r.commitErr
	}
	if !commit.ExpectedState.Equal(r.state.Expectation()) ||
		!task048ExecutionExpectationMatches(r.execution, commit.ExpectedExecution) {
		return nil, false, domain.ErrConflict
	}
	for _, identity := range commit.NewIdentityIDs {
		if _, exists := r.reserved[identity]; exists {
			return nil, false, arena.ErrGoldenWaveIdentityConflict
		}
	}
	if commit.Authority != nil {
		authorityRevision := int64(0)
		authorityDigest := [32]byte{}
		if r.authority != nil {
			authorityRevision = r.authority.Revision
			authorityDigest = arena.GoldenExecutionAuthorityDigest(*r.authority)
		}
		if r.authorityRevisionOverride != nil {
			authorityRevision = *r.authorityRevisionOverride
		}
		if r.authorityDigestOverride != nil {
			authorityDigest = *r.authorityDigestOverride
		}
		if r.authority == nil || !r.authority.Proves(commit.Authority.Identity, r.transactionTime) ||
			authorityRevision != commit.Authority.LeaseRevision || authorityDigest != commit.Authority.LeaseDigest {
			return nil, false, arena.ErrGoldenWaveAuthorityNotLive
		}
	}
	r.commits++
	if r.reserved == nil {
		r.reserved = make(map[uuid.UUID]struct{})
	}
	for _, identity := range commit.NewIdentityIDs {
		r.reserved[identity] = struct{}{}
	}
	clone := commit.Next.Snapshot()
	r.execution = &clone
	r.retainReplayExecution(clone)
	result := r.execution.Snapshot()
	return &result, true, nil
}

func (r *goldenWaveRepositoryFake) retainReplayExecution(execution arena.GoldenWaveExecution) {
	if r.replayExecution == nil {
		r.replayExecution = make(map[uuid.UUID]arena.GoldenWaveExecution)
	}
	for _, receipt := range execution.Receipts {
		r.replayExecution[receipt.CommandID] = execution.Snapshot()
	}
}

func task048ExecutionExpectationMatches(
	current *arena.GoldenWaveExecution,
	expected *arena.GoldenWaveExecutionExpectation,
) bool {
	if current == nil || expected == nil {
		return current == nil && expected == nil
	}
	return current.Expectation().Equal(*expected)
}

func (r *goldenWaveRepositoryFake) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *goldenWaveRepositoryFake) setStateLoadError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stateLoadErr = err
}

func (r *goldenWaveRepositoryFake) setAuthority(lease arena.ExecutionAuthorityLease, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := lease
	r.authority = &clone
	r.transactionTime = now
	r.authorityRevisionOverride = nil
	r.authorityDigestOverride = nil
}

func (r *goldenWaveRepositoryFake) setAuthorityRevisionOverride(revision int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authorityRevisionOverride = &revision
}

func (r *goldenWaveRepositoryFake) setAuthorityDigestOverride(digest [32]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authorityDigestOverride = &digest
}

func (r *goldenWaveRepositoryFake) setCommitError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitErr = err
}

func (r *goldenWaveRepositoryFake) setExecutionBarrier(barrier *goldenLoadBarrier) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.barrier = barrier
}

func (r *goldenWaveRepositoryFake) executionLoadBarrier() *goldenLoadBarrier {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.barrier
}

func (r *goldenWaveRepositoryFake) archiveBeforeNextLoadAfterHiddenFind() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hideReplayFind = 1
	r.archiveBeforeLoad = true
}

func (r *goldenWaveRepositoryFake) setPostLoadWinnerArchive(winner arena.GoldenWaveExecution) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := winner.Snapshot()
	r.hideReplayFind = 1
	r.postLoadWinner = &clone
}

func (r *goldenWaveRepositoryFake) identityReserved(identity uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.reserved[identity]
	return exists
}

func (r *goldenWaveRepositoryFake) setExecution(execution arena.GoldenWaveExecution) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := execution.Snapshot()
	r.execution = &clone
}

func task048ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("48000000-0000-0000-0000-%012d", value))
}

var _ arena.GoldenWaveRepository = (*goldenWaveRepositoryFake)(nil)
