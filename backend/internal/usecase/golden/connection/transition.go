package golden

import (
	"crypto/sha256"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validateGoldenIndividualMutationAuthority(
	authority GoldenIndividualDisconnectAuthority,
	operation goldenIndividualConnectionOperation,
	now time.Time,
) error {
	if authority.Execution.Validate() != nil || authority.Submissions.Validate() != nil ||
		authority.Connections.Validate() != nil {
		return domain.ErrInternal
	}
	if !goldenIndividualAuthorityMatches(authority, operation) {
		return ErrGoldenIndividualConnectionAuthorityConflict
	}
	start := authority.Execution.Start
	if now.Before(start.StartedAt) || now.Before(authority.Connections.StartedAt) {
		return goldenIndividualConnectionError("connection time precedes start")
	}
	if !now.Before(start.Deadline) {
		return ErrGoldenIndividualReconnectDeadline
	}
	if len(authority.Connections.Receipts) > 0 &&
		now.Before(authority.Connections.Receipts[len(authority.Connections.Receipts)-1].OccurredAt) {
		return goldenIndividualConnectionError("connection time regressed")
	}
	return nil
}

func validateGoldenIndividualCommittedMutation(
	committed *GoldenIndividualConnectionLedger,
	next GoldenIndividualConnectionLedger,
	operation goldenIndividualConnectionOperation,
	digest [sha256.Size]byte,
	changed bool,
) error {
	if committed == nil || committed.Validate() != nil {
		return domain.ErrInternal
	}
	receipt, found := goldenIndividualReceiptByCommand(committed.Receipts, operation.commandID)
	if !found || !validGoldenIndividualCommittedLedger(committed, operation) ||
		!validGoldenIndividualCommittedReceipt(receipt, committed, operation, digest) {
		return domain.ErrInternal
	}
	if changed && !committed.Expectation().Equal(next.Expectation()) {
		return domain.ErrInternal
	}
	return nil
}

func validGoldenIndividualCommittedLedger(
	committed *GoldenIndividualConnectionLedger,
	operation goldenIndividualConnectionOperation,
) bool {
	return committed.Scope == operation.scope && committed.Execution.Equal(operation.expectedExecution) &&
		committed.Submissions.Equal(operation.expectedSubmissions) &&
		committed.RevisionID == operation.nextRevisionID &&
		committed.Revision == operation.expectedConnections.Revision+1
}

func validGoldenIndividualCommittedReceipt(
	receipt GoldenIndividualConnectionReceipt,
	committed *GoldenIndividualConnectionLedger,
	operation goldenIndividualConnectionOperation,
	digest [sha256.Size]byte,
) bool {
	return receipt.CommandDigest == digest && receipt.Kind == operation.kind &&
		receipt.ParticipantID == operation.participantID && receipt.IntervalID == operation.intervalID &&
		receipt.Expected.Equal(operation.expectedConnections) &&
		receipt.Expected.Execution.Equal(operation.expectedExecution) &&
		receipt.ObservedSubmissions.Equal(operation.expectedSubmissions) &&
		receipt.ResultRevisionID == operation.nextRevisionID && receipt.ResultRevision == committed.Revision
}

func goldenIndividualAuthorityMatches(
	authority GoldenIndividualDisconnectAuthority,
	operation goldenIndividualConnectionOperation,
) bool {
	execution := authority.Execution
	return execution.Start != nil && execution.Attempt.State == domain.GoldenAttemptStateActive &&
		execution.Wave.State == domain.WaveStateActive && execution.Expectation().Equal(operation.expectedExecution) &&
		authority.Submissions.Expectation().Equal(operation.expectedSubmissions) &&
		authority.Connections.Expectation().Equal(operation.expectedConnections) &&
		authority.Connections.Execution.Equal(operation.expectedExecution) &&
		authority.Connections.StartedAt.Equal(execution.Start.StartedAt) &&
		authority.Connections.Deadline.Equal(execution.Start.Deadline) &&
		authority.Connections.Scope == operation.scope && connectionContainsID(execution.Membership.ParticipantIDs, operation.participantID) &&
		connectionEqualIDs(authority.Connections.ParticipantIDs, execution.Membership.ParticipantIDs)
}

func buildGoldenIndividualConnectionSuccessor(
	authority GoldenIndividualDisconnectAuthority,
	operation goldenIndividualConnectionOperation,
	digest [sha256.Size]byte,
	now time.Time,
) (GoldenIndividualConnectionLedger, []uuid.UUID, error) {
	current := authority.Connections
	if current.Revision == math.MaxInt64 {
		return GoldenIndividualConnectionLedger{}, nil, goldenIndividualConnectionError("ledger revision overflow")
	}
	ids, err := goldenIndividualSuccessorIdentityIDs(authority, operation)
	if err != nil {
		return GoldenIndividualConnectionLedger{}, nil, err
	}
	next := current.Snapshot()
	expected := current.Expectation()
	next.Submissions = authority.Submissions.Expectation()
	next.PreviousRevisionID = connectionUUIDPointer(next.RevisionID)
	next.RevisionID = operation.nextRevisionID
	next.Revision++
	if len(next.Receipts) >= goldenIndividualReceiptLimit {
		return GoldenIndividualConnectionLedger{}, nil, ErrGoldenIndividualConnectionUnavailable
	}
	if err := applyGoldenIndividualSuccessorTransition(&next, authority, operation, now); err != nil {
		return GoldenIndividualConnectionLedger{}, nil, err
	}
	next.Receipts = append(next.Receipts, GoldenIndividualConnectionReceipt{
		CommandID: operation.commandID, CommandDigest: digest, Kind: operation.kind,
		ParticipantID: operation.participantID, IntervalID: operation.intervalID,
		Expected: expected, ObservedSubmissions: authority.Submissions.Expectation(), ResultRevisionID: operation.nextRevisionID,
		ResultRevision: next.Revision, OccurredAt: now,
	})
	if err := rebuildGoldenIndividualConnectionLedger(&next); err != nil {
		return GoldenIndividualConnectionLedger{}, nil, err
	}
	return next.Snapshot(), ids, nil
}

func goldenIndividualSuccessorIdentityIDs(
	authority GoldenIndividualDisconnectAuthority,
	operation goldenIndividualConnectionOperation,
) ([]uuid.UUID, error) {
	ids := []uuid.UUID{operation.commandID, operation.nextRevisionID}
	if operation.kind == GoldenIndividualConnectionDisconnected {
		ids = append(ids, operation.intervalID)
	}
	sortConnectionIDs(ids)
	if !connectionValidIdentitySet(ids) || goldenIndividualIdentityUsed(authority, ids) {
		return nil, goldenIndividualConnectionError("connection identity is reused")
	}
	return ids, nil
}

func applyGoldenIndividualSuccessorTransition(
	next *GoldenIndividualConnectionLedger,
	authority GoldenIndividualDisconnectAuthority,
	operation goldenIndividualConnectionOperation,
	now time.Time,
) error {
	switch operation.kind {
	case GoldenIndividualConnectionDisconnected:
		return applyGoldenIndividualDisconnectSuccessor(next, authority, operation, now)
	case GoldenIndividualConnectionReconnected:
		return applyGoldenIndividualReconnectSuccessor(next, operation, now)
	default:
		return domain.ErrValidation
	}
}

func applyGoldenIndividualDisconnectSuccessor(
	next *GoldenIndividualConnectionLedger,
	authority GoldenIndividualDisconnectAuthority,
	operation goldenIndividualConnectionOperation,
	now time.Time,
) error {
	if !next.IsPresent(operation.participantID) || goldenIndividualOpenInterval(next.Intervals, operation.participantID) != nil ||
		len(next.Intervals) >= goldenIndividualIntervalLimit ||
		goldenIndividualIntervalCount(next.Intervals, operation.participantID) >= domain.ReconnectCycleLimit {
		return ErrGoldenIndividualConnectionUnavailable
	}
	next.PresentParticipantIDs = connectionWithoutID(next.PresentParticipantIDs, operation.participantID)
	next.Intervals = append(next.Intervals, GoldenIndividualReconnectInterval{
		ID: operation.intervalID, ParticipantID: operation.participantID,
		Sequence: goldenIndividualIntervalCount(next.Intervals, operation.participantID) + 1,
		State:    GoldenIndividualReconnectOpen, DisconnectedAt: now, Deadline: authority.Execution.Start.Deadline,
	})
	return nil
}

func applyGoldenIndividualReconnectSuccessor(
	next *GoldenIndividualConnectionLedger,
	operation goldenIndividualConnectionOperation,
	now time.Time,
) error {
	interval := goldenIndividualIntervalByID(next.Intervals, operation.intervalID)
	if interval == nil || interval.ParticipantID != operation.participantID ||
		interval.State != GoldenIndividualReconnectOpen || next.IsPresent(operation.participantID) {
		return ErrGoldenIndividualConnectionUnavailable
	}
	interval.State = GoldenIndividualReconnectClosed
	interval.ReconnectedAt = connectionCloneTimePointer(&now)
	next.PresentParticipantIDs = append(next.PresentParticipantIDs, operation.participantID)
	sortConnectionIDs(next.PresentParticipantIDs)
	return nil
}

func validGoldenIndividualOperation(operation goldenIndividualConnectionOperation) bool {
	return operation.scope.IsValid() && operation.commandID != uuid.Nil && operation.participantID != uuid.Nil &&
		operation.intervalID != uuid.Nil && operation.nextRevisionID != uuid.Nil &&
		operation.expectedExecution.Scope == operation.scope.State && operation.expectedSubmissions.Scope == operation.scope &&
		operation.expectedConnections.Scope == operation.scope &&
		(operation.kind == GoldenIndividualConnectionDisconnected || operation.kind == GoldenIndividualConnectionReconnected)
}

func goldenIndividualIdentityUsed(authority GoldenIndividualDisconnectAuthority, ids []uuid.UUID) bool {
	reserved := connectionRetainedIdentitySet(authority.Execution)
	reserved[authority.Submissions.RevisionID] = struct{}{}
	if authority.Submissions.PreviousRevisionID != nil {
		reserved[*authority.Submissions.PreviousRevisionID] = struct{}{}
	}
	for _, submission := range authority.Submissions.Submissions {
		reserved[submission.VerificationID] = struct{}{}
		reserved[submission.VerificationRevisionID] = struct{}{}
	}
	for _, receipt := range authority.Submissions.Receipts {
		reserved[receipt.CommandID] = struct{}{}
		reserved[receipt.ResultRevisionID] = struct{}{}
	}
	reserved[authority.Connections.RevisionID] = struct{}{}
	for _, interval := range authority.Connections.Intervals {
		reserved[interval.ID] = struct{}{}
	}
	for _, receipt := range authority.Connections.Receipts {
		reserved[receipt.CommandID] = struct{}{}
		reserved[receipt.ResultRevisionID] = struct{}{}
	}
	for _, id := range ids {
		if _, found := reserved[id]; found {
			return true
		}
	}
	return false
}

func goldenIndividualOpenInterval(
	intervals []GoldenIndividualReconnectInterval,
	participantID uuid.UUID,
) *GoldenIndividualReconnectInterval {
	for index := range intervals {
		if intervals[index].ParticipantID == participantID && intervals[index].State == GoldenIndividualReconnectOpen {
			return &intervals[index]
		}
	}
	return nil
}

func goldenIndividualIntervalByID(
	intervals []GoldenIndividualReconnectInterval,
	intervalID uuid.UUID,
) *GoldenIndividualReconnectInterval {
	for index := range intervals {
		if intervals[index].ID == intervalID {
			return &intervals[index]
		}
	}
	return nil
}

func goldenIndividualIntervalCount(intervals []GoldenIndividualReconnectInterval, participantID uuid.UUID) int {
	count := 0
	for _, interval := range intervals {
		if interval.ParticipantID == participantID {
			count++
		}
	}
	return count
}

func goldenIndividualReceiptByCommand(
	receipts []GoldenIndividualConnectionReceipt,
	commandID uuid.UUID,
) (GoldenIndividualConnectionReceipt, bool) {
	for _, receipt := range receipts {
		if receipt.CommandID == commandID {
			return receipt, true
		}
	}
	return GoldenIndividualConnectionReceipt{}, false
}

func reconcileGoldenIndividualReplay(
	replay GoldenIndividualConnectionReplay,
	operation goldenIndividualConnectionOperation,
	digest [sha256.Size]byte,
) (*GoldenIndividualConnectionLedger, bool, error) {
	if replay.Connections.Validate() != nil {
		return nil, false, domain.ErrInternal
	}
	if replay.Receipt.CommandID != operation.commandID {
		return nil, false, domain.ErrInternal
	}
	if replay.Receipt.CommandDigest != digest || replay.Receipt.Kind != operation.kind ||
		replay.Receipt.ParticipantID != operation.participantID || replay.Receipt.IntervalID != operation.intervalID {
		return nil, false, ErrGoldenIndividualConnectionCommandReuse
	}
	stored, found := goldenIndividualReceiptByCommand(replay.Connections.Receipts, operation.commandID)
	if !found || !reflect.DeepEqual(stored, replay.Receipt) {
		return nil, false, domain.ErrInternal
	}
	clone := replay.Connections.Snapshot()
	return &clone, false, nil
}

func rebuildGoldenIndividualConnectionLedger(ledger *GoldenIndividualConnectionLedger) error {
	if ledger == nil {
		return goldenIndividualConnectionError("nil connection ledger")
	}
	payload, err := goldenIndividualConnectionPayload(*ledger)
	if err != nil {
		return goldenIndividualConnectionError("encode connection ledger")
	}
	ledger.PayloadDigest = sha256.Sum256(payload)
	return ledger.Validate()
}

func goldenIndividualConnectionPayload(ledger GoldenIndividualConnectionLedger) ([]byte, error) {
	clone := ledger.Snapshot()
	clone.PayloadDigest = [sha256.Size]byte{}
	return connectionEncode(clone)
}

func goldenIndividualOperationDigest(operation goldenIndividualConnectionOperation) [sha256.Size]byte {
	document := struct {
		Scope               GoldenSubmissionScope
		CommandID           uuid.UUID
		ParticipantID       uuid.UUID
		IntervalID          uuid.UUID
		ExpectedExecution   GoldenWaveExecutionExpectation
		ExpectedSubmissions GoldenSubmissionLedgerExpectation
		ExpectedConnections GoldenIndividualConnectionExpectation
		NextRevisionID      uuid.UUID
		Kind                GoldenIndividualConnectionKind
	}{
		Scope: operation.scope, CommandID: operation.commandID, ParticipantID: operation.participantID,
		IntervalID: operation.intervalID, ExpectedExecution: operation.expectedExecution,
		ExpectedSubmissions: operation.expectedSubmissions, ExpectedConnections: operation.expectedConnections,
		NextRevisionID: operation.nextRevisionID, Kind: operation.kind,
	}
	payload, _ := connectionEncode(document)
	return sha256.Sum256(payload)
}

func goldenIndividualConnectionError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenIndividualConnection, message)
}
