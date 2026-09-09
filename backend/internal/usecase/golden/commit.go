package golden

import (
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func ApplyGoldenSubmissionCommit(
	commit GoldenSubmissionCommit,
	live GoldenSubmissionAuthority,
	submissionID uint64,
	committedAt time.Time,
) (GoldenSubmissionLedger, error) {
	if live.Execution.Validate() != nil || live.Ledger.Validate() != nil || live.Verification.Validate() != nil {
		return GoldenSubmissionLedger{}, domain.ErrInternal
	}
	if !live.Execution.Expectation().Equal(commit.ExpectedExecution) ||
		!live.Ledger.Expectation().Equal(commit.ExpectedLedger) {
		return GoldenSubmissionLedger{}, domain.ErrConflict
	}
	if live.Verification != commit.Verification {
		return GoldenSubmissionLedger{}, ErrGoldenSubmissionAuthorityConflict
	}
	if commit.frozenLedger == nil || !commit.frozenLedger.Expectation().Equal(live.Ledger.Expectation()) ||
		!commit.Deadline.Equal(live.Execution.Start.Deadline) {
		return GoldenSubmissionLedger{}, ErrGoldenSubmissionAuthorityConflict
	}
	if err := validateGoldenSubmissionAuthority(live, commit.Command); err != nil {
		return GoldenSubmissionLedger{}, err
	}
	if !domain.IsValidServerTime(committedAt) || !committedAt.Before(commit.Deadline) {
		return GoldenSubmissionLedger{}, ErrGoldenSubmissionClosed
	}
	return applyGoldenSubmissionCommitToLedger(commit, live.Ledger, submissionID, committedAt)
}

func applyGoldenSubmissionCommitToLedger(
	commit GoldenSubmissionCommit,
	ledger GoldenSubmissionLedger,
	submissionID uint64,
	committedAt time.Time,
) (GoldenSubmissionLedger, error) {
	if ledger.Validate() != nil || !ledger.Expectation().Equal(commit.ExpectedLedger) ||
		!domain.IsValidServerTime(committedAt) || committedAt.Before(commit.Verification.VerifiedAt) ||
		!committedAt.Before(commit.Deadline) {
		return GoldenSubmissionLedger{}, goldenSubmissionError("invalid transaction authority or time")
	}
	next := ledger.Snapshot()
	disposition := GoldenSubmissionAccepted
	referencedID := submissionID
	if existing, found := goldenSubmissionByParticipant(next.Submissions, commit.Command.ParticipantID); found {
		disposition = GoldenSubmissionDuplicate
		referencedID = existing.ID
		if submissionID != 0 {
			return GoldenSubmissionLedger{}, goldenSubmissionError("duplicate submission consumed a sequence")
		}
	} else {
		if submissionID != next.NextSubmissionID {
			return GoldenSubmissionLedger{}, goldenSubmissionError("repository returned a non-monotonic sequence")
		}
		next.Submissions = append(next.Submissions, GoldenSubmissionRecord{
			ID: submissionID, Scope: commit.Command.Scope, ParticipantID: commit.Command.ParticipantID,
			VerificationID: commit.Verification.ID, VerificationRevisionID: commit.Verification.RevisionID,
			EvidenceDigest: commit.Verification.EvidenceDigest, VerifiedAt: commit.Verification.VerifiedAt,
			CommittedAt: committedAt, Authority: commit.Verification.Authority,
			AssignmentDigest: commit.Verification.AssignmentDigest,
		})
		next.NextSubmissionID++
	}
	next.PreviousRevisionID = UUIDPointer(next.RevisionID)
	next.RevisionID = commit.Command.NextLedgerRevisionID
	next.Revision++
	next.Receipts = append(next.Receipts, GoldenSubmissionReceipt{
		CommandID: commit.Command.CommandID, Scope: commit.Command.Scope,
		ParticipantID: commit.Command.ParticipantID, VerificationID: commit.Command.VerificationID,
		CommandDigest: goldenSubmissionCommandDigest(commit.Command), Disposition: disposition,
		SubmissionID: referencedID, Expected: ledger.Expectation(),
		ResultRevisionID: next.RevisionID, ResultRevision: next.Revision, CommittedAt: committedAt,
	})
	return buildGoldenSubmissionLedger(next)
}

// frozenLedger is intentionally unexported: the use case freezes the loaded
// ledger before crossing the port, so an adapter cannot mutate caller-owned
// slices while it commits.
func freezeGoldenSubmissionCommit(commit GoldenSubmissionCommit, ledger GoldenSubmissionLedger) GoldenSubmissionCommit {
	clone := commit
	frozen := ledger.Snapshot()
	clone.frozenLedger = &frozen
	return clone
}
