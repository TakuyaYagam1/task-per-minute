package golden_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/attempt"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/attempt/mocks"
	goldensubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type attemptTask049CommitHarness struct {
	*goldenmocks.MockAttemptRepository

	mu             sync.Mutex
	submissionRepo *attemptTask049SubmissionHarness
	execution      goldenusecase.GoldenWaveExecution
	submissions    goldensubmission.GoldenSubmissionLedger
	positions      goldenusecase.GoldenPositionLedger
	swissPoints    goldenusecase.GoldenSwissPointLedgerSentinel
	current        *goldenusecase.GoldenAttemptCommitRecord
	archived       *goldenusecase.GoldenAttemptCommitRecord
	replays        map[uuid.UUID]goldenusecase.GoldenAttemptCommitRecord
	loadError      error
	commitError    error
	commits        int
	returnRecord   *goldenusecase.GoldenAttemptCommitRecord
	returnChanged  bool
	beforeCommit   func()
	insideCommit   func()
}

type task049CommitHarnessState struct {
	submission      attemptTask049SubmissionHarnessState
	execution       goldenusecase.GoldenWaveExecution
	submissions     goldensubmission.GoldenSubmissionLedger
	positions       goldenusecase.GoldenPositionLedger
	swissPoints     goldenusecase.GoldenSwissPointLedgerSentinel
	current         *goldenusecase.GoldenAttemptCommitRecord
	archived        *goldenusecase.GoldenAttemptCommitRecord
	replays         map[uuid.UUID]goldenusecase.GoldenAttemptCommitRecord
	loadError       error
	commitError     error
	commits         int
	returnRecord    *goldenusecase.GoldenAttemptCommitRecord
	returnChanged   bool
	hasBeforeCommit bool
	hasInsideCommit bool
}

func attemptNewTask049CommitHarness(
	t *testing.T,
	submissionRepo *attemptTask049SubmissionHarness,
	submissions goldensubmission.GoldenSubmissionLedger,
	positions goldenusecase.GoldenPositionLedger,
	sentinel goldenusecase.GoldenSwissPointLedgerSentinel,
) *attemptTask049CommitHarness {
	t.Helper()
	harness := &attemptTask049CommitHarness{
		submissionRepo: submissionRepo, execution: submissionRepo.execution.Snapshot(),
		submissions: submissions.Snapshot(), positions: positions.Snapshot(), swissPoints: sentinel,
		replays: make(map[uuid.UUID]goldenusecase.GoldenAttemptCommitRecord),
	}
	repository := goldenmocks.NewMockAttemptRepository(t)
	repository.EXPECT().FindGoldenAttemptCommit(
		mock.Anything, mock.Anything, mock.Anything,
	).RunAndReturn(harness.findCommit).Maybe()
	repository.EXPECT().LoadGoldenAttemptCommitAuthority(
		mock.Anything, mock.Anything,
	).RunAndReturn(harness.loadAuthority).Maybe()
	repository.EXPECT().CommitGoldenAttempt(
		mock.Anything, mock.Anything,
	).RunAndReturn(harness.commitAttempt).Maybe()
	harness.MockAttemptRepository = repository
	return harness
}

func (r *attemptTask049CommitHarness) findCommit(
	_ context.Context,
	_ uuid.UUID,
	commandID uuid.UUID,
) (*goldenusecase.GoldenAttemptCommitRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if replay, found := r.replays[commandID]; found {
		clone := replay.Snapshot()
		return &clone, nil
	}
	return nil, nil
}

func (r *attemptTask049CommitHarness) loadAuthority(
	_ context.Context,
	scope goldensubmission.GoldenSubmissionScope,
) (goldenusecase.GoldenAttemptCommitAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loadError != nil {
		return goldenusecase.GoldenAttemptCommitAuthority{}, r.loadError
	}
	r.submissionRepo.mu.Lock()
	liveSubmissions := r.submissionRepo.ledger.Snapshot()
	r.submissionRepo.mu.Unlock()
	return goldenusecase.GoldenAttemptCommitAuthority{
		Scope: scope, Execution: r.execution.Snapshot(), Submissions: liveSubmissions,
		Positions: r.positions.Snapshot(), SwissPoints: r.swissPoints,
	}, nil
}

func (r *attemptTask049CommitHarness) commitAttempt(
	_ context.Context,
	record goldenusecase.GoldenAttemptCommitRecord,
) (*goldenusecase.GoldenAttemptCommitRecord, bool, error) {
	if r.beforeCommit != nil {
		r.beforeCommit()
	}
	r.submissionRepo.transactionMu.Lock()
	defer r.submissionRepo.transactionMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.insideCommit != nil {
		r.insideCommit()
	}
	if r.commitError != nil {
		return nil, false, r.commitError
	}
	if r.returnRecord != nil {
		result := r.returnRecord.Snapshot()
		return &result, r.returnChanged, nil
	}
	r.submissionRepo.mu.Lock()
	defer r.submissionRepo.mu.Unlock()
	liveSubmissions := r.submissionRepo.ledger.Snapshot()
	if r.current != nil || !r.execution.Expectation().Equal(record.ActiveExecution) ||
		r.submissionRepo.terminalID != uuid.Nil ||
		!r.submissionRepo.execution.Expectation().Equal(record.ActiveExecution) ||
		!liveSubmissions.Expectation().Equal(record.ExpectedSubmissions) ||
		!r.positions.Expectation().Equal(record.ExpectedPositions) || r.swissPoints != record.SwissPoints {
		return nil, false, domain.ErrConflict
	}
	clone := record.Snapshot()
	r.current = &clone
	r.submissions = liveSubmissions
	r.positions = record.Positions.Snapshot()
	r.replays[record.CommandID] = clone
	r.commits++
	r.submissionRepo.terminalID = record.ID
	r.submissionRepo.terminalDigest = record.PayloadDigest
	result := clone.Snapshot()
	return &result, true, nil
}

func (r *attemptTask049CommitHarness) archiveCurrent() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil {
		clone := r.current.Snapshot()
		r.archived = &clone
		r.current = nil
	}
}

func (r *attemptTask049CommitHarness) restoreCurrent() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.archived != nil {
		clone := r.archived.Snapshot()
		r.current = &clone
	}
}

func (r *attemptTask049CommitHarness) archivedSnapshot() goldenusecase.GoldenAttemptCommitRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.archived != nil {
		return r.archived.Snapshot()
	}
	return r.current.Snapshot()
}

func (r *attemptTask049CommitHarness) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *attemptTask049CommitHarness) stateSnapshot() task049CommitHarnessState {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := task049CommitHarnessState{
		submission: r.submissionRepo.stateSnapshot(),
		execution:  r.execution.Snapshot(), submissions: r.submissions.Snapshot(),
		positions: r.positions.Snapshot(), swissPoints: r.swissPoints,
		replays:   make(map[uuid.UUID]goldenusecase.GoldenAttemptCommitRecord, len(r.replays)),
		loadError: r.loadError, commitError: r.commitError, commits: r.commits,
		returnChanged:   r.returnChanged,
		hasBeforeCommit: r.beforeCommit != nil, hasInsideCommit: r.insideCommit != nil,
	}
	if r.current != nil {
		clone := r.current.Snapshot()
		state.current = &clone
	}
	if r.archived != nil {
		clone := r.archived.Snapshot()
		state.archived = &clone
	}
	for key, value := range r.replays {
		state.replays[key] = value.Snapshot()
	}
	if r.returnRecord != nil {
		clone := r.returnRecord.Snapshot()
		state.returnRecord = &clone
	}
	return state
}

func attemptTask049SwissPointSentinel(base int) goldenusecase.GoldenSwissPointLedgerSentinel {
	return goldenusecase.GoldenSwissPointLedgerSentinel{
		RevisionID: attemptTask049ID(base), Revision: 7,
		Digest: sha256.Sum256([]byte("swiss-points-unchanged")),
	}
}

func attemptTask049SealOrdering(t *testing.T, evidence *goldenusecase.GoldenAttemptOrderingEvidence) {
	t.Helper()
	evidence.PayloadDigest = attemptTask049GobDigest(t, struct {
		AttemptID      uuid.UUID
		AttemptNo      int
		SubmissionHead goldensubmission.GoldenSubmissionLedgerExpectation
		Order          []goldenusecase.GoldenPositionOrderEntry
	}{
		AttemptID: evidence.AttemptID, AttemptNo: evidence.AttemptNo,
		SubmissionHead: evidence.SubmissionHead, Order: evidence.Order,
	})
}

func attemptTask049SealAttemptRecord(t *testing.T, record *goldenusecase.GoldenAttemptCommitRecord) {
	t.Helper()
	record.PayloadDigest = attemptTask049GobDigest(t, struct {
		ID                  uuid.UUID
		CommandID           uuid.UUID
		CommandDigest       [sha256.Size]byte
		Scope               goldensubmission.GoldenSubmissionScope
		ActiveExecution     goldenusecase.GoldenWaveExecutionExpectation
		Assignment          goldenusecase.GoldenAttemptAssignmentEvidence
		ExpectedSubmissions goldensubmission.GoldenSubmissionLedgerExpectation
		ExpectedPositions   goldenusecase.GoldenPositionLedgerExpectation
		SwissPoints         goldenusecase.GoldenSwissPointLedgerSentinel
		Reason              goldenusecase.GoldenAttemptTerminalReason
		FinishedAt          time.Time
		Attempt             domain.GoldenAttempt
		Group               domain.GoldenGroupState
		Wave                domain.Wave
		Ordering            goldenusecase.GoldenAttemptOrderingEvidence
		PriorPositions      goldenusecase.GoldenPositionLedger
		Positions           goldenusecase.GoldenPositionLedger
	}{
		ID: record.ID, CommandID: record.CommandID, CommandDigest: record.CommandDigest,
		Scope: record.Scope, ActiveExecution: record.ActiveExecution, Assignment: record.Assignment,
		ExpectedSubmissions: record.ExpectedSubmissions, ExpectedPositions: record.ExpectedPositions,
		SwissPoints: record.SwissPoints, Reason: record.Reason, FinishedAt: record.FinishedAt,
		Attempt: record.Attempt, Group: record.Group, Wave: record.Wave,
		Ordering: record.Ordering, PriorPositions: record.PriorPositions, Positions: record.Positions,
	})
}

func attemptTask049SealAttemptAssignmentEvidence(t *testing.T, evidence *goldenusecase.GoldenAttemptAssignmentEvidence) {
	t.Helper()
	evidence.PayloadDigest = attemptTask049GobDigest(t, struct {
		ID                     uuid.UUID
		RevisionID             uuid.UUID
		Revision               int64
		Scope                  goldenusecase.GoldenStateScope
		AttemptID              uuid.UUID
		WaveID                 uuid.UUID
		MembershipID           uuid.UUID
		Plan                   goldenusecase.GoldenPlanStateBinding
		EdgeID                 uuid.UUID
		ReservationID          uuid.UUID
		SnapshotID             uuid.UUID
		TaskID                 uuid.UUID
		ContentDigest          [sha256.Size]byte
		Private                []goldenusecase.GoldenPrivateAssignment
		ExecutionPayloadDigest [sha256.Size]byte
	}{
		ID: evidence.ID, RevisionID: evidence.RevisionID, Revision: evidence.Revision,
		Scope: evidence.Scope, AttemptID: evidence.AttemptID, WaveID: evidence.WaveID,
		MembershipID: evidence.MembershipID, Plan: evidence.Plan, EdgeID: evidence.EdgeID,
		ReservationID: evidence.ReservationID, SnapshotID: evidence.SnapshotID,
		TaskID: evidence.TaskID, ContentDigest: evidence.ContentDigest, Private: evidence.Private,
		ExecutionPayloadDigest: evidence.ExecutionPayloadDigest,
	})
}

func attemptTask049SealPositionLedger(t *testing.T, ledger *goldenusecase.GoldenPositionLedger) {
	t.Helper()
	ledger.PayloadDigest = attemptTask049GobDigest(t, struct {
		Scope              goldenusecase.GoldenStateScope
		RevisionID         uuid.UUID
		Revision           int64
		PreviousRevisionID *uuid.UUID
		RevisionIDs        []uuid.UUID
		PositionFrom       int
		PositionTo         int
		Positions          []goldenusecase.GoldenCommittedPosition
		Attempts           []goldenusecase.GoldenAttemptOrderingEvidence
	}{
		Scope: ledger.Scope, RevisionID: ledger.RevisionID, Revision: ledger.Revision,
		PreviousRevisionID: ledger.PreviousRevisionID, RevisionIDs: ledger.RevisionIDs,
		PositionFrom: ledger.PositionFrom, PositionTo: ledger.PositionTo,
		Positions: ledger.Positions, Attempts: ledger.Attempts,
	})
}

func attemptTask049GobDigest(t *testing.T, value any) [sha256.Size]byte {
	t.Helper()
	var buffer bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buffer).Encode(value))
	return sha256.Sum256(buffer.Bytes())
}

func attemptTask049SealSubmissionLedger(t *testing.T, ledger *goldensubmission.GoldenSubmissionLedger) {
	t.Helper()
	ledger.PayloadDigest = attemptTask049GobDigest(t, struct {
		Scope              goldensubmission.GoldenSubmissionScope
		RevisionID         uuid.UUID
		Revision           int64
		PreviousRevisionID *uuid.UUID
		NextSubmissionID   uint64
		Submissions        []goldensubmission.GoldenSubmissionRecord
		Receipts           []goldensubmission.GoldenSubmissionReceipt
	}{
		Scope: ledger.Scope, RevisionID: ledger.RevisionID, Revision: ledger.Revision,
		PreviousRevisionID: ledger.PreviousRevisionID, NextSubmissionID: ledger.NextSubmissionID,
		Submissions: ledger.Submissions, Receipts: ledger.Receipts,
	})
}

func attemptTask049Encode(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

const attemptTask049WaitTimeout = 2 * time.Second

func attemptTask049Latch(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	ready := make(chan struct{})
	var once sync.Once
	signal := func() { once.Do(func() { close(ready) }) }
	t.Cleanup(signal)
	return ready, signal
}

func attemptTask049AwaitSignal(t *testing.T, signal <-chan struct{}, label string) bool {
	t.Helper()
	select {
	case <-signal:
		return true
	case <-time.After(attemptTask049WaitTimeout):
		t.Errorf("%s timed out", label)
		return false
	}
}

func attemptTask049AwaitValue[T any](t *testing.T, values <-chan T, label string) (T, bool) {
	t.Helper()
	select {
	case value := <-values:
		return value, true
	case <-time.After(attemptTask049WaitTimeout):
		t.Errorf("%s timed out", label)
		var zero T
		return zero, false
	}
}

func attemptNewGoldenClock(t *testing.T, now time.Time) *goldenmocks.MockAttemptClock {
	t.Helper()
	clock := goldenmocks.NewMockAttemptClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}
