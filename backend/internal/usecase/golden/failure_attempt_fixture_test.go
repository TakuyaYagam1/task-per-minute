package golden_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"sync"
	"testing"
	"time"

	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func failureTask049SwissPointSentinel(base int) goldenusecase.GoldenSwissPointLedgerSentinel {
	return goldenusecase.GoldenSwissPointLedgerSentinel{
		RevisionID: failureTask049ID(base), Revision: 7,
		Digest: sha256.Sum256([]byte("swiss-points-unchanged")),
	}
}

func failureTask049SealOrdering(t *testing.T, evidence *goldenusecase.GoldenAttemptOrderingEvidence) {
	t.Helper()
	evidence.PayloadDigest = failureTask049GobDigest(t, struct {
		AttemptID      uuid.UUID
		AttemptNo      int
		SubmissionHead goldenusecase.GoldenSubmissionLedgerExpectation
		Order          []goldenusecase.GoldenPositionOrderEntry
	}{
		AttemptID: evidence.AttemptID, AttemptNo: evidence.AttemptNo,
		SubmissionHead: evidence.SubmissionHead, Order: evidence.Order,
	})
}

func failureTask049SealAttemptAssignmentEvidence(t *testing.T, evidence *goldenusecase.GoldenAttemptAssignmentEvidence) {
	t.Helper()
	evidence.PayloadDigest = failureTask049GobDigest(t, struct {
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

func failureTask049SealPositionLedger(t *testing.T, ledger *goldenusecase.GoldenPositionLedger) {
	t.Helper()
	ledger.PayloadDigest = failureTask049GobDigest(t, struct {
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

func failureTask049GobDigest(t *testing.T, value any) [sha256.Size]byte {
	t.Helper()
	var buffer bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buffer).Encode(value))
	return sha256.Sum256(buffer.Bytes())
}

func failureTask049SealSubmissionLedger(t *testing.T, ledger *goldenusecase.GoldenSubmissionLedger) {
	t.Helper()
	ledger.PayloadDigest = failureTask049GobDigest(t, struct {
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

func failureTask049TwoPartyBarrier(t *testing.T) func() {
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
