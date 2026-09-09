package golden

import (
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"
)

func buildGoldenSubmissionLedger(ledger GoldenSubmissionLedger) (GoldenSubmissionLedger, error) {
	clone := ledger.Snapshot()
	payload, err := goldenSubmissionLedgerPayload(clone)
	if err != nil {
		return GoldenSubmissionLedger{}, goldenSubmissionError("encode ledger")
	}
	clone.PayloadDigest = sha256.Sum256(payload)
	if err := clone.Validate(); err != nil {
		return GoldenSubmissionLedger{}, err
	}
	return clone.Snapshot(), nil
}

func goldenSubmissionLedgerPayload(ledger GoldenSubmissionLedger) ([]byte, error) {
	return Encode(struct {
		Scope              GoldenSubmissionScope
		RevisionID         uuid.UUID
		Revision           int64
		PreviousRevisionID *uuid.UUID
		NextSubmissionID   uint64
		Submissions        []GoldenSubmissionRecord
		Receipts           []GoldenSubmissionReceipt
	}{
		Scope: ledger.Scope, RevisionID: ledger.RevisionID, Revision: ledger.Revision,
		PreviousRevisionID: ledger.PreviousRevisionID, NextSubmissionID: ledger.NextSubmissionID,
		Submissions: ledger.Submissions, Receipts: ledger.Receipts,
	})
}

func goldenSubmissionCommandDigest(command GoldenSubmissionCommand) [sha256.Size]byte {
	payload, _ := Encode(command)
	return sha256.Sum256(payload)
}

func goldenSubmissionError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenSubmission, message)
}
