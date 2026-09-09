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

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/mocks"
)

type continuationTask049CommitHarness struct {
	*goldenmocks.MockAttemptRepository

	mu             sync.Mutex
	submissionRepo *continuationTask049SubmissionHarness
	execution      goldenusecase.GoldenWaveExecution
	submissions    goldenusecase.GoldenSubmissionLedger
	positions      goldenusecase.GoldenPositionLedger
	swissPoints    goldenusecase.GoldenSwissPointLedgerSentinel
	current        *goldenusecase.GoldenAttemptCommitRecord
	replays        map[uuid.UUID]goldenusecase.GoldenAttemptCommitRecord
	loadError      error
	commitError    error
	commits        int
	returnRecord   *goldenusecase.GoldenAttemptCommitRecord
	returnChanged  bool
	beforeCommit   func()
	insideCommit   func()
}

func continuationNewTask049CommitHarness(
	t *testing.T,
	submissionRepo *continuationTask049SubmissionHarness,
	submissions goldenusecase.GoldenSubmissionLedger,
	positions goldenusecase.GoldenPositionLedger,
	sentinel goldenusecase.GoldenSwissPointLedgerSentinel,
) *continuationTask049CommitHarness {
	t.Helper()
	harness := &continuationTask049CommitHarness{
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

func (r *continuationTask049CommitHarness) findCommit(
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

func (r *continuationTask049CommitHarness) loadAuthority(
	_ context.Context,
	scope goldenusecase.GoldenSubmissionScope,
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

func (r *continuationTask049CommitHarness) commitAttempt(
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

func continuationTask049SwissPointSentinel(base int) goldenusecase.GoldenSwissPointLedgerSentinel {
	return goldenusecase.GoldenSwissPointLedgerSentinel{
		RevisionID: continuationTask049ID(base), Revision: 7,
		Digest: sha256.Sum256([]byte("swiss-points-unchanged")),
	}
}

func continuationTask049SealOrdering(t *testing.T, evidence *goldenusecase.GoldenAttemptOrderingEvidence) {
	t.Helper()
	evidence.PayloadDigest = continuationTask049GobDigest(t, struct {
		AttemptID      uuid.UUID
		AttemptNo      int
		SubmissionHead goldenusecase.GoldenSubmissionLedgerExpectation
		Order          []goldenusecase.GoldenPositionOrderEntry
	}{
		AttemptID: evidence.AttemptID, AttemptNo: evidence.AttemptNo,
		SubmissionHead: evidence.SubmissionHead, Order: evidence.Order,
	})
}

func continuationTask049SealAttemptRecord(t *testing.T, record *goldenusecase.GoldenAttemptCommitRecord) {
	t.Helper()
	record.PayloadDigest = continuationTask049GobDigest(t, struct {
		ID                  uuid.UUID
		CommandID           uuid.UUID
		CommandDigest       [sha256.Size]byte
		Scope               goldenusecase.GoldenSubmissionScope
		ActiveExecution     goldenusecase.GoldenWaveExecutionExpectation
		Assignment          goldenusecase.GoldenAttemptAssignmentEvidence
		ExpectedSubmissions goldenusecase.GoldenSubmissionLedgerExpectation
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

func continuationTask049SealAttemptAssignmentEvidence(t *testing.T, evidence *goldenusecase.GoldenAttemptAssignmentEvidence) {
	t.Helper()
	evidence.PayloadDigest = continuationTask049GobDigest(t, struct {
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

func continuationTask049SealPositionLedger(t *testing.T, ledger *goldenusecase.GoldenPositionLedger) {
	t.Helper()
	ledger.PayloadDigest = continuationTask049GobDigest(t, struct {
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

func continuationTask049GobDigest(t *testing.T, value any) [sha256.Size]byte {
	t.Helper()
	var buffer bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buffer).Encode(value))
	return sha256.Sum256(buffer.Bytes())
}

func continuationTask049Encode(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

const continuationTask049WaitTimeout = 2 * time.Second
