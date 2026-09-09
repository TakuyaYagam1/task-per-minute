package golden_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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

func waveTask048GoldenState(t *testing.T, openedAt time.Time) goldenusecase.GoldenState {
	t.Helper()
	state := waveGoldenStateFixture(t, openedAt)
	state.Group.Attempts = nil
	state.Windows = nil
	state.Membership.PayloadDigest = [32]byte{}
	state.PayloadDigest = [32]byte{}
	built, err := goldenusecase.BuildGoldenState(state)
	require.NoError(t, err)
	return built
}

func task048SoloGoldenState(t *testing.T, openedAt time.Time) goldenusecase.GoldenState {
	t.Helper()
	state := waveGoldenStateFixture(t, openedAt)
	repository := waveNewGoldenStateRepository(t, state)
	ready := waveGoldenAcceptReady(t, repository, state, state.Group.Members[0].ParticipantID, openedAt.Add(time.Second), 9000)
	resolved, changed, err := goldenusecase.NewGoldenNoShowUseCase(
		repository,
		waveNewGoldenClock(t, openedAt.Add(31*time.Second)),
	).Resolve(t.Context(), waveGoldenNoShowCommand(ready, 9010))
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, resolved.ActiveParticipantIDs(), 1)
	return *resolved
}

func task048OpenCommand(state goldenusecase.GoldenState, base int) goldenusecase.OpenGoldenReadyWindowCommand {
	participants := state.ActiveParticipantIDs()
	private := make([]goldenusecase.GoldenPrivateAssignmentCommand, len(participants))
	for index, participantID := range participants {
		private[index] = goldenusecase.GoldenPrivateAssignmentCommand{
			ParticipantID: participantID,
			AssignmentID:  task048ID(base + 20 + index),
		}
	}
	return goldenusecase.OpenGoldenReadyWindowCommand{
		Scope: state.Scope, CommandID: task048ID(base), ExpectedState: state.Expectation(),
		AttemptID: task048ID(base + 1), WaveID: task048ID(base + 2),
		WaveRevisionID:       domain.WaveRevisionID(task048ID(base + 3)),
		WindowID:             task048ID(base + 4),
		WaveWindowRevisionID: domain.ReadyWindowRevisionID(task048ID(base + 5)),
		WindowRevisionID:     task048ID(base + 6), ReadinessRevisionID: task048ID(base + 7),
		PresenceRevisionID: task048ID(base + 8), MembershipID: task048ID(base + 9),
		MembershipRevisionID: task048ID(base + 10), AssignmentID: task048ID(base + 11),
		AssignmentRevisionID: task048ID(base + 12), ExecutionRevisionID: task048ID(base + 13),
		PrivateAssignments: private,
	}
}

type waveGoldenWaveRepositoryHarness struct {
	*goldenmocks.MockWaveRepository

	mu                        sync.Mutex
	state                     goldenusecase.GoldenState
	execution                 *goldenusecase.GoldenWaveExecution
	commits                   int
	stateLoadErr              error
	commitErr                 error
	authority                 *authoritydomain.Lease
	transactionTime           time.Time
	barrier                   *waveGoldenLoadBarrier
	reserved                  map[uuid.UUID]struct{}
	replayExecution           map[uuid.UUID]goldenusecase.GoldenWaveExecution
	hideReplayFind            int
	archiveBeforeLoad         bool
	postLoadWinner            *goldenusecase.GoldenWaveExecution
	authorityRevisionOverride *int64
	authorityDigestOverride   *[32]byte
}

type waveGoldenLoadBarrier struct {
	limit   int32
	calls   atomic.Int32
	arrived chan struct{}
	release chan struct{}
}

func waveNewGoldenLoadBarrier(limit int) *waveGoldenLoadBarrier {
	return &waveGoldenLoadBarrier{
		limit: int32(limit), arrived: make(chan struct{}, limit), release: make(chan struct{}),
	}
}

func (b *waveGoldenLoadBarrier) wait() {
	if b.calls.Add(1) > b.limit {
		return
	}
	b.arrived <- struct{}{}
	<-b.release
}

func waveNewGoldenClock(t *testing.T, now time.Time) *goldenmocks.MockWaveClock {
	t.Helper()

	clock := goldenmocks.NewMockWaveClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func waveNewGoldenWaveRepositoryHarness(
	t *testing.T,
	state goldenusecase.GoldenState,
) *waveGoldenWaveRepositoryHarness {
	t.Helper()

	harness := &waveGoldenWaveRepositoryHarness{state: state}
	repository := goldenmocks.NewMockWaveRepository(t)
	repository.EXPECT().
		FindGoldenWaveCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.findCommandReplay).
		Maybe()
	repository.EXPECT().
		LoadGoldenWaveExecution(mock.Anything, mock.Anything).
		RunAndReturn(harness.loadExecutionSnapshot).
		Maybe()
	repository.EXPECT().
		LoadGoldenState(mock.Anything, mock.Anything).
		RunAndReturn(harness.loadStateSnapshot).
		Maybe()
	repository.EXPECT().
		CommitGoldenWaveExecution(mock.Anything, mock.Anything).
		RunAndReturn(harness.commitExecutionState).
		Maybe()
	harness.MockWaveRepository = repository
	return harness
}

func (r *waveGoldenWaveRepositoryHarness) findCommandReplay(
	_ context.Context,
	_ uuid.UUID,
	commandID uuid.UUID,
) (*goldenusecase.GoldenWaveCommandReplay, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hideReplayFind > 0 {
		r.hideReplayFind--
		return nil, nil
	}
	if replay, found := r.replayExecution[commandID]; found {
		for _, receipt := range replay.Receipts {
			if receipt.CommandID == commandID {
				return &goldenusecase.GoldenWaveCommandReplay{
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
			return &goldenusecase.GoldenWaveCommandReplay{
				Receipt: receipt, Execution: r.execution.Snapshot(),
			}, nil
		}
	}
	return nil, nil
}

func (r *waveGoldenWaveRepositoryHarness) loadExecutionSnapshot(
	_ context.Context,
	_ goldenusecase.GoldenStateScope,
) (*goldenusecase.GoldenWaveExecution, error) {
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

func (r *waveGoldenWaveRepositoryHarness) loadStateSnapshot(
	_ context.Context,
	_ goldenusecase.GoldenStateScope,
) (goldenusecase.GoldenState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stateLoadErr != nil {
		return goldenusecase.GoldenState{}, r.stateLoadErr
	}
	return r.state.Snapshot(), nil
}

func (r *waveGoldenWaveRepositoryHarness) commitExecutionState(
	_ context.Context,
	commit goldenusecase.GoldenWaveExecutionCommit,
) (*goldenusecase.GoldenWaveExecution, bool, error) {
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
			return nil, false, goldenusecase.ErrGoldenWaveIdentityConflict
		}
	}
	if commit.Authority != nil {
		authorityRevision := int64(0)
		authorityDigest := [32]byte{}
		if r.authority != nil {
			authorityRevision = r.authority.Revision
			authorityDigest = goldenusecase.GoldenExecutionAuthorityDigest(*r.authority)
		}
		if r.authorityRevisionOverride != nil {
			authorityRevision = *r.authorityRevisionOverride
		}
		if r.authorityDigestOverride != nil {
			authorityDigest = *r.authorityDigestOverride
		}
		if r.authority == nil || !r.authority.Proves(commit.Authority.Identity, r.transactionTime) ||
			authorityRevision != commit.Authority.LeaseRevision || authorityDigest != commit.Authority.LeaseDigest {
			return nil, false, goldenusecase.ErrGoldenWaveAuthorityNotLive
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

func (r *waveGoldenWaveRepositoryHarness) retainReplayExecution(execution goldenusecase.GoldenWaveExecution) {
	if r.replayExecution == nil {
		r.replayExecution = make(map[uuid.UUID]goldenusecase.GoldenWaveExecution)
	}
	for _, receipt := range execution.Receipts {
		r.replayExecution[receipt.CommandID] = execution.Snapshot()
	}
}

func task048ExecutionExpectationMatches(
	current *goldenusecase.GoldenWaveExecution,
	expected *goldenusecase.GoldenWaveExecutionExpectation,
) bool {
	if current == nil || expected == nil {
		return current == nil && expected == nil
	}
	return current.Expectation().Equal(*expected)
}

func (r *waveGoldenWaveRepositoryHarness) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *waveGoldenWaveRepositoryHarness) setStateLoadError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stateLoadErr = err
}

func (r *waveGoldenWaveRepositoryHarness) setAuthority(
	lease authoritydomain.Lease,
	now time.Time,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := lease
	r.authority = &clone
	r.transactionTime = now
	r.authorityRevisionOverride = nil
	r.authorityDigestOverride = nil
}

func (r *waveGoldenWaveRepositoryHarness) setAuthorityRevisionOverride(revision int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authorityRevisionOverride = &revision
}

func (r *waveGoldenWaveRepositoryHarness) setAuthorityDigestOverride(digest [32]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authorityDigestOverride = &digest
}

func (r *waveGoldenWaveRepositoryHarness) setCommitError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commitErr = err
}

func (r *waveGoldenWaveRepositoryHarness) setExecutionBarrier(barrier *waveGoldenLoadBarrier) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.barrier = barrier
}

func (r *waveGoldenWaveRepositoryHarness) executionLoadBarrier() *waveGoldenLoadBarrier {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.barrier
}

func (r *waveGoldenWaveRepositoryHarness) archiveBeforeNextLoadAfterHiddenFind() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hideReplayFind = 1
	r.archiveBeforeLoad = true
}

func (r *waveGoldenWaveRepositoryHarness) setPostLoadWinnerArchive(winner goldenusecase.GoldenWaveExecution) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := winner.Snapshot()
	r.hideReplayFind = 1
	r.postLoadWinner = &clone
}

func (r *waveGoldenWaveRepositoryHarness) identityReserved(identity uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, exists := r.reserved[identity]
	return exists
}

func (r *waveGoldenWaveRepositoryHarness) reserveIdentity(identity uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reserved == nil {
		r.reserved = make(map[uuid.UUID]struct{})
	}
	r.reserved[identity] = struct{}{}
}

func (r *waveGoldenWaveRepositoryHarness) setExecution(execution goldenusecase.GoldenWaveExecution) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := execution.Snapshot()
	r.execution = &clone
}

func task048ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("48000000-0000-0000-0000-%012d", value))
}
