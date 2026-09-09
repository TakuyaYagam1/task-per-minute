package golden

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"

	"github.com/google/uuid"
)

func reconcileGoldenAttemptCommit(
	record GoldenAttemptCommitRecord,
	command GoldenAttemptCommitCommand,
) (*GoldenAttemptCommitRecord, error) {
	if record.Validate() != nil || record.CommandID != command.CommandID || record.ID != command.CommitID ||
		record.Scope != command.Scope || record.CommandDigest != goldenAttemptCommitCommandDigest(command) ||
		!record.ActiveExecution.Equal(command.ExpectedExecution) ||
		!record.ExpectedSubmissions.Equal(command.ExpectedSubmissions) ||
		!record.ExpectedPositions.Equal(command.ExpectedPositions) ||
		record.SwissPoints != command.ExpectedSwissPoints || record.Reason != command.Reason ||
		record.Positions.RevisionID != command.NextPositionRevisionID {
		return nil, ErrGoldenAttemptCommitCommandReuse
	}
	clone := record.Snapshot()
	return &clone, nil
}

func buildGoldenPositionLedger(ledger GoldenPositionLedger) (GoldenPositionLedger, error) {
	clone := ledger.Snapshot()
	payload, err := goldenPositionLedgerPayload(clone)
	if err != nil {
		return GoldenPositionLedger{}, goldenAttemptCommitError("encode position ledger")
	}
	clone.PayloadDigest = sha256.Sum256(payload)
	if err := clone.Validate(); err != nil {
		return GoldenPositionLedger{}, err
	}
	return clone.Snapshot(), nil
}

func goldenAttemptOrderingPayload(evidence GoldenAttemptOrderingEvidence) ([]byte, error) {
	return Encode(struct {
		AttemptID      uuid.UUID
		AttemptNo      int
		SubmissionHead GoldenSubmissionLedgerExpectation
		Order          []GoldenPositionOrderEntry
	}{
		AttemptID: evidence.AttemptID, AttemptNo: evidence.AttemptNo,
		SubmissionHead: evidence.SubmissionHead, Order: evidence.Order,
	})
}

func goldenPositionLedgerPayload(ledger GoldenPositionLedger) ([]byte, error) {
	return Encode(struct {
		Scope              GoldenStateScope
		RevisionID         uuid.UUID
		Revision           int64
		PreviousRevisionID *uuid.UUID
		RevisionIDs        []uuid.UUID
		PositionFrom       int
		PositionTo         int
		Positions          []GoldenCommittedPosition
		Attempts           []GoldenAttemptOrderingEvidence
	}{
		Scope: ledger.Scope, RevisionID: ledger.RevisionID, Revision: ledger.Revision,
		PreviousRevisionID: ledger.PreviousRevisionID, RevisionIDs: ledger.RevisionIDs, PositionFrom: ledger.PositionFrom,
		PositionTo: ledger.PositionTo, Positions: ledger.Positions, Attempts: ledger.Attempts,
	})
}

func goldenAttemptCommitPayload(record GoldenAttemptCommitRecord) ([]byte, error) {
	return Encode(struct {
		ID                  uuid.UUID
		CommandID           uuid.UUID
		CommandDigest       [sha256.Size]byte
		Scope               GoldenSubmissionScope
		ActiveExecution     GoldenWaveExecutionExpectation
		Assignment          GoldenAttemptAssignmentEvidence
		ExpectedSubmissions GoldenSubmissionLedgerExpectation
		ExpectedPositions   GoldenPositionLedgerExpectation
		SwissPoints         GoldenSwissPointLedgerSentinel
		Reason              GoldenAttemptTerminalReason
		FinishedAt          time.Time
		Attempt             domain.GoldenAttempt
		Group               domain.GoldenGroupState
		Wave                domain.Wave
		Ordering            GoldenAttemptOrderingEvidence
		PriorPositions      GoldenPositionLedger
		Positions           GoldenPositionLedger
	}{
		ID: record.ID, CommandID: record.CommandID, CommandDigest: record.CommandDigest,
		Scope: record.Scope, ActiveExecution: record.ActiveExecution, Assignment: record.Assignment,
		ExpectedSubmissions: record.ExpectedSubmissions, ExpectedPositions: record.ExpectedPositions,
		SwissPoints: record.SwissPoints, Reason: record.Reason, FinishedAt: record.FinishedAt,
		Attempt: record.Attempt, Group: record.Group, Wave: record.Wave,
		Ordering: record.Ordering, PriorPositions: record.PriorPositions, Positions: record.Positions,
	})
}

func goldenAttemptCommitCommandDigest(command GoldenAttemptCommitCommand) [sha256.Size]byte {
	payload, _ := Encode(command)
	return sha256.Sum256(payload)
}

func goldenPositionRevisionIDUsed(ledger GoldenPositionLedger, revisionID uuid.UUID) bool {
	return ContainsID(ledger.RevisionIDs, revisionID)
}

func goldenAttemptCommitError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenAttemptCommit, message)
}
