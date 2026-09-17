package golden_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenconnection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/connection"
	goldenmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/connection/mocks"
	goldensubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"
	goldenwave "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/wave"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func task050SealConnectionLedger(t *testing.T, ledger *goldenconnection.GoldenIndividualConnectionLedger) {
	t.Helper()
	clone := ledger.Snapshot()
	clone.PayloadDigest = [sha256.Size]byte{}
	var payload bytes.Buffer
	require.NoError(t, gob.NewEncoder(&payload).Encode(clone))
	ledger.PayloadDigest = sha256.Sum256(payload.Bytes())
}

func task050SealFirstExpectedConnectionHead(
	t *testing.T,
	ledger *goldenconnection.GoldenIndividualConnectionLedger,
) {
	t.Helper()
	receipt := &ledger.Receipts[0]
	initial := goldenconnection.GoldenIndividualConnectionLedger{
		Scope: ledger.Scope, Execution: ledger.Execution,
		Submissions: receipt.Expected.Submissions, StartedAt: ledger.StartedAt, Deadline: ledger.Deadline,
		RevisionID: receipt.Expected.RevisionID, Revision: receipt.Expected.Revision,
		ParticipantIDs:        append([]uuid.UUID(nil), ledger.ParticipantIDs...),
		PresentParticipantIDs: append([]uuid.UUID(nil), ledger.ParticipantIDs...),
	}
	task050SealConnectionLedger(t, &initial)
	receipt.Expected.PayloadDigest = initial.PayloadDigest
	task050SealConnectionLedger(t, ledger)
}

type task050ConnectionRepositoryHarness struct {
	*goldenmocks.MockConnectionRepository

	mu                sync.Mutex
	execution         goldenwave.GoldenWaveExecution
	submissions       goldensubmission.GoldenSubmissionLedger
	connections       goldenconnection.GoldenIndividualConnectionLedger
	replays           map[uuid.UUID]goldenconnection.GoldenIndividualConnectionReplay
	beforeCommit      func()
	returnConnections *goldenconnection.GoldenIndividualConnectionLedger
	writes            int
}

func newTask050ConnectionRepositoryHarness(
	t *testing.T,
	execution goldenwave.GoldenWaveExecution,
	submissions goldensubmission.GoldenSubmissionLedger,
	connections goldenconnection.GoldenIndividualConnectionLedger,
) *task050ConnectionRepositoryHarness {
	t.Helper()

	harness := &task050ConnectionRepositoryHarness{
		execution: execution.Snapshot(), submissions: submissions.Snapshot(), connections: connections.Snapshot(),
		replays: make(map[uuid.UUID]goldenconnection.GoldenIndividualConnectionReplay),
	}
	repository := goldenmocks.NewMockConnectionRepository(t)
	repository.EXPECT().
		FindGoldenIndividualConnectionCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(harness.findConnectionReplay).
		Maybe()
	repository.EXPECT().
		LoadGoldenIndividualDisconnectAuthority(mock.Anything, mock.Anything).
		RunAndReturn(harness.loadDisconnectSnapshot).
		Maybe()
	repository.EXPECT().
		CommitGoldenIndividualConnection(mock.Anything, mock.Anything).
		RunAndReturn(harness.commitConnectionState).
		Maybe()
	harness.MockConnectionRepository = repository
	return harness
}

func (r *task050ConnectionRepositoryHarness) findConnectionReplay(
	_ context.Context,
	_ uuid.UUID,
	commandID uuid.UUID,
) (*goldenconnection.GoldenIndividualConnectionReplay, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	replay, found := r.replays[commandID]
	if !found {
		return nil, nil
	}
	clone := replay.Snapshot()
	return &clone, nil
}

func (r *task050ConnectionRepositoryHarness) loadDisconnectSnapshot(
	_ context.Context,
	_ goldensubmission.GoldenSubmissionScope,
) (goldenconnection.GoldenIndividualDisconnectAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return goldenconnection.GoldenIndividualDisconnectAuthority{
		Execution: r.execution.Snapshot(), Submissions: r.submissions.Snapshot(),
		Connections: r.connections.Snapshot(),
	}, nil
}

func (r *task050ConnectionRepositoryHarness) commitConnectionState(
	_ context.Context,
	commit goldenconnection.GoldenIndividualConnectionCommit,
) (*goldenconnection.GoldenIndividualConnectionLedger, bool, error) {
	if r.beforeCommit != nil {
		r.beforeCommit()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.execution.Expectation().Equal(commit.ExpectedExecution) ||
		!r.submissions.Expectation().Equal(commit.ExpectedSubmissions) ||
		!r.connections.Expectation().Equal(commit.ExpectedConnections) {
		return nil, false, domain.ErrConflict
	}
	last := commit.Next.Receipts[len(commit.Next.Receipts)-1]
	if _, exists := r.replays[last.CommandID]; exists {
		return nil, false, errors.New("duplicate command")
	}
	if r.returnConnections != nil {
		clone := r.returnConnections.Snapshot()
		return &clone, false, nil
	}
	r.connections = commit.Next.Snapshot()
	r.writes++
	r.replays[last.CommandID] = goldenconnection.GoldenIndividualConnectionReplay{
		Receipt: last, Connections: r.connections.Snapshot(),
	}
	clone := r.connections.Snapshot()
	return &clone, true, nil
}

func (r *task050ConnectionRepositoryHarness) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func (r *task050ConnectionRepositoryHarness) executionSnapshot() goldenwave.GoldenWaveExecution {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.execution.Snapshot()
}

func (r *task050ConnectionRepositoryHarness) submissionSnapshot() goldensubmission.GoldenSubmissionLedger {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.submissions.Snapshot()
}

func (r *task050ConnectionRepositoryHarness) replaceSubmissions(submissions goldensubmission.GoldenSubmissionLedger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.submissions = submissions.Snapshot()
}

func (r *task050ConnectionRepositoryHarness) connectionSnapshot() goldenconnection.GoldenIndividualConnectionLedger {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.connections.Snapshot()
}

func connectionNewGoldenClock(t *testing.T, now time.Time) *goldenmocks.MockConnectionClock {
	t.Helper()
	clock := goldenmocks.NewMockConnectionClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func connectionTask049TwoPartyBarrier(t *testing.T) func() {
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
