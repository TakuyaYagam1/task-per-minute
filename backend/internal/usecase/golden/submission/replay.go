package submission

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func reconcileGoldenSubmissionReplay(
	authority GoldenSubmissionAuthority,
	command GoldenSubmissionCommand,
) (*GoldenSubmissionLedger, error) {
	if authority.Replay == nil || authority.Ledger.Validate() != nil ||
		!goldenSubmissionReceiptMatchesCommand(*authority.Replay, command) {
		return nil, ErrGoldenSubmissionCommandReuse
	}
	retained, found := goldenSubmissionReceiptByCommand(authority.Ledger.Receipts, command.CommandID)
	if !found || retained != *authority.Replay {
		return nil, domain.ErrInternal
	}
	if authority.Execution.Start == nil || !retained.CommittedAt.Before(authority.Execution.Start.Deadline) {
		return nil, domain.ErrInternal
	}
	clone := authority.Ledger.Snapshot()
	return &clone, nil
}

func goldenSubmissionReceiptMatchesCommand(
	receipt GoldenSubmissionReceipt,
	command GoldenSubmissionCommand,
) bool {
	return receipt.CommandID == command.CommandID && receipt.Scope == command.Scope &&
		receipt.ParticipantID == command.ParticipantID && receipt.VerificationID == command.VerificationID &&
		receipt.CommandDigest == goldenSubmissionCommandDigest(command) &&
		receipt.ResultRevisionID == command.NextLedgerRevisionID
}

func goldenSubmissionReceiptByCommand(
	receipts []GoldenSubmissionReceipt,
	commandID uuid.UUID,
) (GoldenSubmissionReceipt, bool) {
	for _, receipt := range receipts {
		if receipt.CommandID == commandID {
			return receipt, true
		}
	}
	return GoldenSubmissionReceipt{}, false
}

func goldenSubmissionByParticipant(
	submissions []GoldenSubmissionRecord,
	participantID uuid.UUID,
) (GoldenSubmissionRecord, bool) {
	for _, submission := range submissions {
		if submission.ParticipantID == participantID {
			return submission, true
		}
	}
	return GoldenSubmissionRecord{}, false
}

func goldenSubmissionRevisionIDUsed(ledger GoldenSubmissionLedger, revisionID uuid.UUID) bool {
	if ledger.RevisionID == revisionID || ledger.PreviousRevisionID != nil && *ledger.PreviousRevisionID == revisionID {
		return true
	}
	for _, receipt := range ledger.Receipts {
		if receipt.Expected.RevisionID == revisionID || receipt.ResultRevisionID == revisionID {
			return true
		}
	}
	return false
}
