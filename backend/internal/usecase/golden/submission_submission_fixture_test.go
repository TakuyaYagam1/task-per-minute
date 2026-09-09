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
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/mocks"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type submissionTask049SubmissionHarness struct {
	*goldenmocks.MockSubmissionRepository

	transactionMu        *sync.Mutex
	mu                   sync.Mutex
	execution            goldenusecase.GoldenWaveExecution
	ledger               goldenusecase.GoldenSubmissionLedger
	archived             goldenusecase.GoldenSubmissionLedger
	verifications        map[uuid.UUID]goldenusecase.GoldenSubmissionVerification
	replays              map[uuid.UUID]goldenusecase.GoldenSubmissionReceipt
	committedAt          time.Time
	committedAtByCommand map[uuid.UUID]time.Time
	nextID               uint64
	commits              int
	terminalID           uuid.UUID
	terminalDigest       [sha256.Size]byte
	beforeCommit         func()
	returnLedger         *goldenusecase.GoldenSubmissionLedger
	returnChanged        bool
}

type submissionTask049SubmissionHarnessState struct {
	execution            goldenusecase.GoldenWaveExecution
	ledger               goldenusecase.GoldenSubmissionLedger
	archived             goldenusecase.GoldenSubmissionLedger
	verifications        map[uuid.UUID]goldenusecase.GoldenSubmissionVerification
	replays              map[uuid.UUID]goldenusecase.GoldenSubmissionReceipt
	committedAt          time.Time
	committedAtByCommand map[uuid.UUID]time.Time
	nextID               uint64
	commits              int
	terminalID           uuid.UUID
	terminalDigest       [sha256.Size]byte
	returnLedger         *goldenusecase.GoldenSubmissionLedger
	returnChanged        bool
	hasBeforeCommit      bool
}

func submissionNewTask049SubmissionHarness(
	t *testing.T,
	execution goldenusecase.GoldenWaveExecution,
	ledger goldenusecase.GoldenSubmissionLedger,
	committedAt time.Time,
) *submissionTask049SubmissionHarness {
	t.Helper()
	harness := &submissionTask049SubmissionHarness{
		transactionMu: &sync.Mutex{},
		execution:     execution.Snapshot(), ledger: ledger.Snapshot(),
		verifications:        make(map[uuid.UUID]goldenusecase.GoldenSubmissionVerification),
		replays:              make(map[uuid.UUID]goldenusecase.GoldenSubmissionReceipt),
		committedAtByCommand: make(map[uuid.UUID]time.Time),
		committedAt:          committedAt, nextID: ledger.NextSubmissionID,
	}
	repository := goldenmocks.NewMockSubmissionRepository(t)
	repository.EXPECT().LoadGoldenSubmissionAuthority(
		mock.Anything, mock.Anything, mock.Anything, mock.Anything,
	).RunAndReturn(harness.loadAuthority).Maybe()
	repository.EXPECT().CommitGoldenSubmission(
		mock.Anything, mock.Anything,
	).RunAndReturn(harness.commitSubmission).Maybe()
	harness.MockSubmissionRepository = repository
	return harness
}

func (r *submissionTask049SubmissionHarness) loadAuthority(
	_ context.Context,
	scope goldenusecase.GoldenSubmissionScope,
	commandID uuid.UUID,
	verificationID uuid.UUID,
) (goldenusecase.GoldenSubmissionAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ledger := r.ledger
	if ledger.Scope.State.TournamentID == uuid.Nil {
		ledger = r.archived
	}
	authority := goldenusecase.GoldenSubmissionAuthority{
		Scope: scope, Execution: r.execution.Snapshot(), Ledger: ledger.Snapshot(),
		Verification:     r.verifications[verificationID],
		TerminalCommitID: r.terminalID, TerminalCommitDigest: r.terminalDigest,
	}
	if replay, found := r.replays[commandID]; found {
		clone := replay
		authority.Replay = &clone
	}
	return authority, nil
}

func (r *submissionTask049SubmissionHarness) commitSubmission(
	_ context.Context,
	commit goldenusecase.GoldenSubmissionCommit,
) (*goldenusecase.GoldenSubmissionLedger, bool, error) {
	if r.beforeCommit != nil {
		r.beforeCommit()
	}
	r.transactionMu.Lock()
	defer r.transactionMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.execution.Expectation().Equal(commit.ExpectedExecution) ||
		!r.ledger.Expectation().Equal(commit.ExpectedLedger) {
		return nil, false, domain.ErrConflict
	}
	if _, found := r.replays[commit.Command.CommandID]; found {
		return nil, false, domain.ErrConflict
	}
	live := goldenusecase.GoldenSubmissionAuthority{
		Scope: commit.Command.Scope, Execution: r.execution.Snapshot(), Ledger: r.ledger.Snapshot(),
		Verification:     r.verifications[commit.Command.VerificationID],
		TerminalCommitID: r.terminalID, TerminalCommitDigest: r.terminalDigest,
	}
	nextID := uint64(0)
	if !r.ledger.HasParticipant(commit.Command.ParticipantID) {
		nextID = r.nextID
	}
	committedAt := r.committedAt
	if specific, found := r.committedAtByCommand[commit.Command.CommandID]; found {
		committedAt = specific
	}
	next, applyErr := goldenusecase.ApplyGoldenSubmissionCommit(commit, live, nextID, committedAt)
	if applyErr != nil {
		return nil, false, applyErr
	}
	if nextID != 0 {
		r.nextID++
	}
	r.ledger = next.Snapshot()
	r.replays[commit.Command.CommandID] = next.Receipts[len(next.Receipts)-1]
	r.commits++
	result := r.ledger.Snapshot()
	if r.returnLedger != nil {
		result = r.returnLedger.Snapshot()
		return &result, r.returnChanged, nil
	}
	return &result, true, nil
}

func (r *submissionTask049SubmissionHarness) snapshot() goldenusecase.GoldenSubmissionLedger {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ledger.Scope.State.TournamentID == uuid.Nil {
		return r.archived.Snapshot()
	}
	return r.ledger.Snapshot()
}

func (r *submissionTask049SubmissionHarness) archiveCurrent() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.archived = r.ledger.Snapshot()
	r.ledger = goldenusecase.GoldenSubmissionLedger{}
}

func (r *submissionTask049SubmissionHarness) restoreCurrent() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ledger = r.archived.Snapshot()
}

func (r *submissionTask049SubmissionHarness) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *submissionTask049SubmissionHarness) stateSnapshot() submissionTask049SubmissionHarnessState {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := submissionTask049SubmissionHarnessState{
		execution: r.execution.Snapshot(), ledger: r.ledger.Snapshot(), archived: r.archived.Snapshot(),
		verifications:        make(map[uuid.UUID]goldenusecase.GoldenSubmissionVerification, len(r.verifications)),
		replays:              make(map[uuid.UUID]goldenusecase.GoldenSubmissionReceipt, len(r.replays)),
		committedAt:          r.committedAt,
		committedAtByCommand: make(map[uuid.UUID]time.Time, len(r.committedAtByCommand)),
		nextID:               r.nextID, commits: r.commits,
		terminalID: r.terminalID, terminalDigest: r.terminalDigest,
		returnChanged: r.returnChanged, hasBeforeCommit: r.beforeCommit != nil,
	}
	for key, value := range r.verifications {
		state.verifications[key] = value
	}
	for key, value := range r.replays {
		state.replays[key] = value
	}
	for key, value := range r.committedAtByCommand {
		state.committedAtByCommand[key] = value
	}
	if r.returnLedger != nil {
		clone := r.returnLedger.Snapshot()
		state.returnLedger = &clone
	}
	return state
}

func (r *submissionTask049SubmissionHarness) clone(t *testing.T) *submissionTask049SubmissionHarness {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := submissionNewTask049SubmissionHarness(t, r.execution, r.ledger, r.committedAt)
	clone.archived = r.archived.Snapshot()
	clone.nextID = r.nextID
	for key, value := range r.verifications {
		clone.verifications[key] = value
	}
	for key, value := range r.replays {
		clone.replays[key] = value
	}
	for key, value := range r.committedAtByCommand {
		clone.committedAtByCommand[key] = value
	}
	return clone
}

func submissionTask049StartedExecution(t *testing.T, startedAt time.Time) goldenusecase.GoldenWaveExecution {
	t.Helper()
	_, execution := submissionTask049StartedFixture(t, startedAt)
	return execution
}

func submissionTask049StartedFixture(t *testing.T, startedAt time.Time) (goldenusecase.GoldenState, goldenusecase.GoldenWaveExecution) {
	t.Helper()
	return submissionNewStartedGoldenFixture(t, startedAt)
}

func submissionTask049SubmissionScope(execution goldenusecase.GoldenWaveExecution) goldenusecase.GoldenSubmissionScope {
	return goldenusecase.GoldenSubmissionScope{
		State: execution.Scope, AttemptID: execution.Attempt.ID, WaveID: execution.Wave.ID,
		AssignmentID: execution.Assignment.ID, SnapshotID: execution.Assignment.Snapshot.SnapshotID,
		TaskID: execution.Assignment.Snapshot.TaskID,
	}
}

func submissionTask049Verification(
	scope goldenusecase.GoldenSubmissionScope,
	execution goldenusecase.GoldenWaveExecution,
	participantID uuid.UUID,
	base int,
) goldenusecase.GoldenSubmissionVerification {
	digest := sha256.Sum256([]byte("correct:" + participantID.String()))
	return goldenusecase.GoldenSubmissionVerification{
		ID: submissionTask049ID(base), RevisionID: submissionTask049ID(base + 1), Scope: scope,
		ParticipantID: participantID, Correct: true, VerifiedAt: execution.Start.StartedAt.Add(time.Second),
		EvidenceDigest: digest, Authority: execution.Start.Authority.Identity,
		AssignmentDigest: execution.Assignment.PayloadDigest,
	}
}

func task049SubmissionIDs(values []goldenusecase.GoldenSubmissionRecord) []uint64 {
	result := make([]uint64, len(values))
	for index, value := range values {
		result[index] = value.ID
	}
	return result
}

func submissionTask049SealSubmissionLedger(t *testing.T, ledger *goldenusecase.GoldenSubmissionLedger) {
	t.Helper()
	ledger.PayloadDigest = submissionTask049GobDigest(t, struct {
		Scope              goldenusecase.GoldenSubmissionScope
		RevisionID         uuid.UUID
		Revision           int64
		PreviousRevisionID *uuid.UUID
		NextSubmissionID   uint64
		Submissions        []goldenusecase.GoldenSubmissionRecord
		Receipts           []goldenusecase.GoldenSubmissionReceipt
	}{
		Scope: ledger.Scope, RevisionID: ledger.RevisionID, Revision: ledger.Revision,
		PreviousRevisionID: ledger.PreviousRevisionID, NextSubmissionID: ledger.NextSubmissionID,
		Submissions: ledger.Submissions, Receipts: ledger.Receipts,
	})
}

func submissionTask049Encode(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

func submissionTask049GobDigest(t *testing.T, value any) [sha256.Size]byte {
	t.Helper()
	var buffer bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buffer).Encode(value))
	return sha256.Sum256(buffer.Bytes())
}

func submissionTask049TwoPartyBarrier(t *testing.T) func() {
	t.Helper()
	var mu sync.Mutex
	arrived := 0
	release := make(chan struct{})
	timedOut := make(chan struct{})
	timer := time.AfterFunc(2*time.Second, func() { close(timedOut) })
	return func() {
		mu.Lock()
		if arrived >= 2 {
			mu.Unlock()
			return
		}
		arrived++
		if arrived == 2 {
			timer.Stop()
			close(release)
		}
		mu.Unlock()
		select {
		case <-release:
		case <-timedOut:
			t.Errorf("two-party repository barrier timed out")
		}
	}
}

const submissionTask049WaitTimeout = 2 * time.Second

func task049AwaitCompletions(t *testing.T, completed <-chan struct{}, count int, label string) bool {
	t.Helper()
	timer := time.NewTimer(submissionTask049WaitTimeout)
	defer timer.Stop()
	for received := 0; received < count; received++ {
		select {
		case <-completed:
		case <-timer.C:
			t.Errorf("%s timed out after %d of %d completions", label, received, count)
			return false
		}
	}
	return true
}

func submissionTask049Latch(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()
	ready := make(chan struct{})
	var once sync.Once
	signal := func() {
		once.Do(func() { close(ready) })
	}
	t.Cleanup(signal)
	return ready, signal
}

func submissionTask049AwaitSignal(t *testing.T, signal <-chan struct{}, label string) bool {
	t.Helper()
	select {
	case <-signal:
		return true
	case <-time.After(submissionTask049WaitTimeout):
		t.Errorf("%s timed out", label)
		return false
	}
}

func submissionTask049AwaitValue[T any](t *testing.T, values <-chan T, label string) (T, bool) {
	t.Helper()
	select {
	case value := <-values:
		return value, true
	case <-time.After(submissionTask049WaitTimeout):
		t.Errorf("%s timed out", label)
		var zero T
		return zero, false
	}
}
