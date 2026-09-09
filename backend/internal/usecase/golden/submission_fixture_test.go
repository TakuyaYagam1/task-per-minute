package golden_test

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/mocks"
)

type attemptTask049SubmissionHarness struct {
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

type attemptTask049SubmissionHarnessState struct {
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

func attemptNewTask049SubmissionHarness(
	t *testing.T,
	execution goldenusecase.GoldenWaveExecution,
	ledger goldenusecase.GoldenSubmissionLedger,
	committedAt time.Time,
) *attemptTask049SubmissionHarness {
	t.Helper()
	harness := &attemptTask049SubmissionHarness{
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

func (r *attemptTask049SubmissionHarness) loadAuthority(
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

func (r *attemptTask049SubmissionHarness) commitSubmission(
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

func (r *attemptTask049SubmissionHarness) snapshot() goldenusecase.GoldenSubmissionLedger {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ledger.Scope.State.TournamentID == uuid.Nil {
		return r.archived.Snapshot()
	}
	return r.ledger.Snapshot()
}

func (r *attemptTask049SubmissionHarness) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *attemptTask049SubmissionHarness) stateSnapshot() attemptTask049SubmissionHarnessState {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := attemptTask049SubmissionHarnessState{
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

func attemptTask049SubmissionScope(execution goldenusecase.GoldenWaveExecution) goldenusecase.GoldenSubmissionScope {
	return goldenusecase.GoldenSubmissionScope{
		State: execution.Scope, AttemptID: execution.Attempt.ID, WaveID: execution.Wave.ID,
		AssignmentID: execution.Assignment.ID, SnapshotID: execution.Assignment.Snapshot.SnapshotID,
		TaskID: execution.Assignment.Snapshot.TaskID,
	}
}

func attemptTask049Verification(
	scope goldenusecase.GoldenSubmissionScope,
	execution goldenusecase.GoldenWaveExecution,
	participantID uuid.UUID,
	base int,
) goldenusecase.GoldenSubmissionVerification {
	digest := sha256.Sum256([]byte("correct:" + participantID.String()))
	return goldenusecase.GoldenSubmissionVerification{
		ID: attemptTask049ID(base), RevisionID: attemptTask049ID(base + 1), Scope: scope,
		ParticipantID: participantID, Correct: true, VerifiedAt: execution.Start.StartedAt.Add(time.Second),
		EvidenceDigest: digest, Authority: execution.Start.Authority.Identity,
		AssignmentDigest: execution.Assignment.PayloadDigest,
	}
}
