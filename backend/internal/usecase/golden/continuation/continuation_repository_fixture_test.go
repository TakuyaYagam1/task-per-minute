package golden_test

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenattempt "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/attempt"
	goldencontinuation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/continuation"
	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
	goldensubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"
	goldenwave "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/wave"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/continuation/mocks"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type task049ContinuationHarnessState struct {
	State       goldenstate.GoldenState
	Terminal    goldenattempt.GoldenAttemptCommitRecord
	Positions   goldenattempt.GoldenPositionLedger
	SwissPoints goldenattempt.GoldenSwissPointLedgerSentinel
	Parent      *goldencontinuation.GoldenContinuationRecord
	Current     *goldencontinuation.GoldenContinuationRecord
	Archived    *goldencontinuation.GoldenContinuationRecord
	Replays     map[uuid.UUID]goldencontinuation.GoldenContinuationRecord
	Reserved    map[uuid.UUID]struct{}
	Consumed    map[uuid.UUID]struct{}
	Commits     int
}

type task049ContinuationHarness struct {
	*goldenmocks.MockContinuationRepository

	mu            sync.Mutex
	state         goldenstate.GoldenState
	terminal      goldenattempt.GoldenAttemptCommitRecord
	positions     goldenattempt.GoldenPositionLedger
	swissPoints   goldenattempt.GoldenSwissPointLedgerSentinel
	parent        *goldencontinuation.GoldenContinuationRecord
	current       *goldencontinuation.GoldenContinuationRecord
	archived      *goldencontinuation.GoldenContinuationRecord
	replays       map[uuid.UUID]goldencontinuation.GoldenContinuationRecord
	reserved      map[uuid.UUID]struct{}
	consumed      map[uuid.UUID]struct{}
	loadError     error
	commitError   error
	commits       int
	returnRecord  *goldencontinuation.GoldenContinuationRecord
	returnChanged bool
	beforeCommit  func()
	planOverride  *goldenplan.ExactPlan
}

func newTask049ContinuationHarness(
	t *testing.T,
	state goldenstate.GoldenState,
	terminal goldenattempt.GoldenAttemptCommitRecord,
	positions goldenattempt.GoldenPositionLedger,
	sentinel goldenattempt.GoldenSwissPointLedgerSentinel,
) *task049ContinuationHarness {
	t.Helper()
	harness := &task049ContinuationHarness{
		state: state.Snapshot(), terminal: terminal.Snapshot(), positions: positions.Snapshot(),
		swissPoints: sentinel, replays: make(map[uuid.UUID]goldencontinuation.GoldenContinuationRecord),
		reserved: make(map[uuid.UUID]struct{}), consumed: make(map[uuid.UUID]struct{}),
	}
	repository := goldenmocks.NewMockContinuationRepository(t)
	repository.EXPECT().FindGoldenContinuation(
		mock.Anything, mock.Anything, mock.Anything,
	).RunAndReturn(harness.findContinuation).Maybe()
	repository.EXPECT().LoadGoldenContinuationAuthority(
		mock.Anything, mock.Anything,
	).RunAndReturn(harness.loadAuthority).Maybe()
	repository.EXPECT().CommitGoldenContinuation(
		mock.Anything, mock.Anything,
	).RunAndReturn(harness.commitContinuation).Maybe()
	harness.MockContinuationRepository = repository
	return harness
}

func (r *task049ContinuationHarness) findContinuation(
	_ context.Context,
	_ uuid.UUID,
	commandID uuid.UUID,
) (*goldencontinuation.GoldenContinuationRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if replay, found := r.replays[commandID]; found {
		clone := replay.Snapshot()
		return &clone, nil
	}
	return nil, nil
}

func (r *task049ContinuationHarness) loadAuthority(
	_ context.Context,
	scope goldenstate.GoldenStateScope,
) (goldencontinuation.GoldenContinuationAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loadError != nil {
		return goldencontinuation.GoldenContinuationAuthority{}, r.loadError
	}
	authority := goldencontinuation.GoldenContinuationAuthority{
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

func (r *task049ContinuationHarness) commitContinuation(
	_ context.Context,
	record goldencontinuation.GoldenContinuationRecord,
) (*goldencontinuation.GoldenContinuationRecord, bool, error) {
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

func (r *task049ContinuationHarness) advanceTerminal(
	parent goldencontinuation.GoldenContinuationRecord,
	terminal goldenattempt.GoldenAttemptCommitRecord,
	positions goldenattempt.GoldenPositionLedger,
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
	parent *goldencontinuation.GoldenContinuationRecord,
	expectedID uuid.UUID,
	expectedDigest [sha256.Size]byte,
) bool {
	if parent == nil {
		return expectedID == uuid.Nil && expectedDigest == [sha256.Size]byte{}
	}
	return parent.ID == expectedID && parent.PayloadDigest == expectedDigest
}

func (r *task049ContinuationHarness) archiveCurrent() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		clone := r.current.Snapshot()
		r.archived = &clone
		r.current = nil
	}
}

func (r *task049ContinuationHarness) restoreCurrent() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.archived != nil {
		clone := r.archived.Snapshot()
		r.current = &clone
	}
}

func (r *task049ContinuationHarness) archivedSnapshot() goldencontinuation.GoldenContinuationRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.archived != nil {
		return r.archived.Snapshot()
	}
	return r.current.Snapshot()
}

func (r *task049ContinuationHarness) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *task049ContinuationHarness) snapshotState() task049ContinuationHarnessState {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := task049ContinuationHarnessState{
		State: r.state.Snapshot(), Terminal: r.terminal.Snapshot(), Positions: r.positions.Snapshot(),
		SwissPoints: r.swissPoints, Replays: make(map[uuid.UUID]goldencontinuation.GoldenContinuationRecord, len(r.replays)),
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
		case <-time.After(continuationTask049WaitTimeout):
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
	timer := time.NewTimer(2 * continuationTask049WaitTimeout)
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
	execution goldenwave.GoldenWaveExecution,
	solved int,
	base int,
) (goldenattempt.GoldenAttemptCommitRecord, goldenattempt.GoldenPositionLedger, goldenattempt.GoldenSwissPointLedgerSentinel) {
	t.Helper()
	scope := continuationTask049SubmissionScope(execution)
	ledger, err := goldensubmission.NewGoldenSubmissionLedger(scope, continuationTask049ID(base))
	require.NoError(t, err)
	submissionRepository := continuationNewTask049SubmissionHarness(t, execution, ledger, execution.Start.StartedAt.Add(time.Second))
	current := ledger
	for index, participantID := range execution.Membership.ParticipantIDs[:solved] {
		verification := continuationTask049Verification(scope, execution, participantID, base+10+index*10)
		submissionRepository.verifications[verification.ID] = verification
		command := goldensubmission.GoldenSubmissionCommand{
			Scope: scope, CommandID: continuationTask049ID(base + 40 + index*10),
			ActorParticipantID: participantID, ParticipantID: participantID,
			VerificationID: verification.ID, ExpectedExecution: execution.Expectation(),
			NextLedgerRevisionID: continuationTask049ID(base + 41 + index*10),
		}
		updated, changed, submitErr := goldensubmission.NewGoldenSubmissionUseCase(submissionRepository).Submit(t.Context(), command)
		require.NoError(t, submitErr)
		require.True(t, changed)
		current = *updated
	}
	positions, err := goldenattempt.NewGoldenPositionLedger(
		execution.Scope, execution.Group.PositionFrom, execution.Group.PositionTo, continuationTask049ID(base+70),
	)
	require.NoError(t, err)
	sentinel := continuationTask049SwissPointSentinel(base + 71)
	repository := continuationNewTask049CommitHarness(t, submissionRepository, current, positions, sentinel)
	command := goldenattempt.GoldenAttemptCommitCommand{
		Scope: scope, CommandID: continuationTask049ID(base + 80), CommitID: continuationTask049ID(base + 81),
		ExpectedExecution: execution.Expectation(), ExpectedSubmissions: current.Expectation(),
		ExpectedPositions: positions.Expectation(), ExpectedSwissPoints: sentinel,
		NextPositionRevisionID: continuationTask049ID(base + 82), Reason: goldenattempt.GoldenAttemptTerminalDeadline,
	}
	record, changed, err := goldenattempt.NewGoldenAttemptCommitUseCase(
		repository,
		continuationNewGoldenClock(t, execution.Start.Deadline),
	).CommitAttempt(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	return *record, record.Positions.Snapshot(), sentinel
}

func task049ContinuationCommand(
	terminal goldenattempt.GoldenAttemptCommitRecord,
	state goldenstate.GoldenState,
	positions goldenattempt.GoldenPositionLedger,
	sentinel goldenattempt.GoldenSwissPointLedgerSentinel,
	parent *goldencontinuation.GoldenContinuationRecord,
	base int,
) goldencontinuation.GoldenContinuationCommand {
	unresolved := task049Unresolved(terminal)
	private := make([]goldenwave.GoldenPrivateAssignmentCommand, len(unresolved))
	for index, participantID := range unresolved {
		private[index] = goldenwave.GoldenPrivateAssignmentCommand{
			ParticipantID: participantID,
			AssignmentID:  continuationTask049ID(base + 20 + index),
		}
	}
	command := goldencontinuation.GoldenContinuationCommand{
		Scope: terminal.Scope.State, CommandID: continuationTask049ID(base), ContinuationID: continuationTask049ID(base + 1),
		ExpectedTerminalID: terminal.ID, ExpectedTerminalDigest: terminal.PayloadDigest,
		ExpectedState: state.Expectation(), ExpectedPlan: state.Plan,
		ExpectedPositions: positions.Expectation(), ExpectedSwissPoints: sentinel,
		NextAttemptID: continuationTask049ID(base + 2), NextAssignmentID: continuationTask049ID(base + 3),
		NextAssignmentRevisionID: continuationTask049ID(base + 4), PrivateAssignments: private,
	}
	if parent != nil {
		command.ExpectedParentID = parent.ID
		command.ExpectedParentDigest = parent.PayloadDigest
	}
	return command
}

func task049ReplaceContinuationParticipant(
	record *goldencontinuation.GoldenContinuationRecord,
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
	record *goldenattempt.GoldenAttemptCommitRecord,
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
