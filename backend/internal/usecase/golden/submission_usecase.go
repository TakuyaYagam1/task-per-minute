package golden

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GoldenSubmissionAuthority struct {
	Scope                GoldenSubmissionScope
	Execution            GoldenWaveExecution
	Ledger               GoldenSubmissionLedger
	Verification         GoldenSubmissionVerification
	Replay               *GoldenSubmissionReceipt
	TerminalCommitID     uuid.UUID
	TerminalCommitDigest [sha256.Size]byte
}

type GoldenSubmissionCommit struct {
	ExpectedExecution GoldenWaveExecutionExpectation
	ExpectedLedger    GoldenSubmissionLedgerExpectation
	Deadline          time.Time
	Command           GoldenSubmissionCommand
	Verification      GoldenSubmissionVerification
	frozenLedger      *GoldenSubmissionLedger
}

type GoldenSubmissionUseCase struct {
	repository SubmissionRepository
}

func NewGoldenSubmissionUseCase(repository SubmissionRepository) *GoldenSubmissionUseCase {
	return &GoldenSubmissionUseCase{repository: repository}
}

func (u *GoldenSubmissionUseCase) Submit(
	ctx context.Context,
	command GoldenSubmissionCommand,
) (*GoldenSubmissionLedger, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	command.ExpectedExecution = CloneExecutionExpectation(command.ExpectedExecution)
	if err := validateGoldenSubmissionCommand(command); err != nil {
		return nil, false, err
	}
	for range goldenSubmissionCommitAttempts {
		ledger, changed, retry, err := u.submitAttempt(ctx, command)
		if retry {
			continue
		}
		return ledger, changed, err
	}
	return nil, false, ErrGoldenSubmissionConflict
}

func (u *GoldenSubmissionUseCase) submitAttempt(
	ctx context.Context,
	command GoldenSubmissionCommand,
) (*GoldenSubmissionLedger, bool, bool, error) {
	authority, err := u.repository.LoadGoldenSubmissionAuthority(
		ctx, command.Scope, command.CommandID, command.VerificationID,
	)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenSubmissionUseCase - load authority: %w", err)
	}
	if authority.Replay != nil {
		ledger, replayErr := reconcileGoldenSubmissionReplay(authority, command)
		return ledger, false, false, replayErr
	}
	if err := validateGoldenSubmissionAuthority(authority, command); err != nil {
		return nil, false, false, err
	}
	commit := GoldenSubmissionCommit{
		ExpectedExecution: CloneExecutionExpectation(command.ExpectedExecution),
		ExpectedLedger:    authority.Ledger.Expectation(), Deadline: authority.Execution.Start.Deadline,
		Command:      command,
		Verification: authority.Verification,
	}
	commit = freezeGoldenSubmissionCommit(commit, authority.Ledger)
	committed, changed, err := u.repository.CommitGoldenSubmission(ctx, commit)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if errors.Is(err, ErrGoldenSubmissionClosed) {
		return nil, false, false, ErrGoldenSubmissionClosed
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenSubmissionUseCase - commit submission: %w", err)
	}
	if committed == nil || committed.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	receipt, found := goldenSubmissionReceiptByCommand(committed.Receipts, command.CommandID)
	if !found || !goldenSubmissionReceiptMatchesCommand(receipt, command) ||
		!receipt.CommittedAt.Before(commit.Deadline) ||
		(changed && receipt.ResultRevisionID != committed.RevisionID) {
		return nil, false, false, domain.ErrInternal
	}
	result := committed.Snapshot()
	return &result, changed, false, nil
}

func validateGoldenSubmissionCommand(command GoldenSubmissionCommand) error {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil || command.ActorParticipantID == uuid.Nil ||
		command.ParticipantID == uuid.Nil || command.VerificationID == uuid.Nil ||
		command.NextLedgerRevisionID == uuid.Nil {
		return goldenSubmissionError("invalid command identity or scope")
	}
	if !ValidIdentitySet([]uuid.UUID{
		command.CommandID, command.VerificationID, command.NextLedgerRevisionID,
	}) {
		return goldenSubmissionError("command identity is aliased")
	}
	return nil
}

func validateGoldenSubmissionAuthority(
	authority GoldenSubmissionAuthority,
	command GoldenSubmissionCommand,
) error {
	if !goldenSubmissionAuthorityScopeMatches(authority, command.Scope) {
		return goldenSubmissionError("authority scope does not match command")
	}
	if goldenSubmissionAuthorityIsClosed(authority) {
		return ErrGoldenSubmissionClosed
	}
	if authority.Execution.Validate() != nil || authority.Ledger.Validate() != nil {
		return domain.ErrInternal
	}
	execution := authority.Execution
	if !goldenSubmissionExecutionIsActive(execution) {
		return ErrGoldenSubmissionClosed
	}
	if !execution.Expectation().Equal(command.ExpectedExecution) {
		return ErrGoldenSubmissionAuthorityConflict
	}
	if !ScopeMatchesExecution(command.Scope, execution) {
		return goldenSubmissionError("command is not bound to the active assignment")
	}
	if !goldenSubmissionActorOwnsExecution(command, execution) {
		return domain.ErrAssignmentParticipant
	}
	if !goldenSubmissionParticipantIsActive(execution, command.ParticipantID) {
		return domain.ErrAssignmentParticipant
	}
	verification := authority.Verification
	if !goldenSubmissionVerificationMatches(verification, command, execution) {
		return goldenSubmissionError("correctness attestation is ambiguous or stale")
	}
	if !verification.Correct {
		return ErrGoldenSubmissionIncorrect
	}
	if goldenSubmissionDuplicateLimitReached(authority.Ledger, command.ParticipantID) {
		return ErrGoldenSubmissionDuplicateLimit
	}
	if !goldenSubmissionSuccessorIDIsFresh(authority.Ledger, command.NextLedgerRevisionID) {
		return goldenSubmissionError("ledger successor identity is reused")
	}
	if !goldenSubmissionNewIDsAreFresh(
		authority,
		command.CommandID,
		command.NextLedgerRevisionID,
	) {
		return goldenSubmissionError("submission identity aliases retained execution authority")
	}
	return nil
}

func goldenSubmissionDuplicateLimitReached(ledger GoldenSubmissionLedger, participantID uuid.UUID) bool {
	if !ledger.HasParticipant(participantID) {
		return false
	}
	for _, receipt := range ledger.Receipts {
		if receipt.ParticipantID == participantID && receipt.Disposition == GoldenSubmissionDuplicate {
			return true
		}
	}
	return false
}

func goldenSubmissionNewIDsAreFresh(
	authority GoldenSubmissionAuthority,
	identities ...uuid.UUID,
) bool {
	reserved := RetainedIdentitySet(authority.Execution)
	ReserveLedgerIdentities(reserved, authority.Ledger)
	identities = append(identities, authority.Verification.ID, authority.Verification.RevisionID)
	seen := make(map[uuid.UUID]struct{}, len(identities))
	for _, identity := range identities {
		if identity == uuid.Nil {
			continue
		}
		if _, found := reserved[identity]; found {
			return false
		}
		if _, found := seen[identity]; found {
			return false
		}
		seen[identity] = struct{}{}
	}
	return true
}

func ReserveLedgerIdentities(
	reserved map[uuid.UUID]struct{},
	ledger GoldenSubmissionLedger,
) {
	reserved[ledger.RevisionID] = struct{}{}
	if ledger.PreviousRevisionID != nil {
		reserved[*ledger.PreviousRevisionID] = struct{}{}
	}
	for _, submission := range ledger.Submissions {
		reserved[submission.VerificationID] = struct{}{}
		reserved[submission.VerificationRevisionID] = struct{}{}
	}
	for _, receipt := range ledger.Receipts {
		reserved[receipt.CommandID] = struct{}{}
		reserved[receipt.Expected.RevisionID] = struct{}{}
		reserved[receipt.ResultRevisionID] = struct{}{}
	}
}

func goldenSubmissionAuthorityScopeMatches(authority GoldenSubmissionAuthority, scope GoldenSubmissionScope) bool {
	return authority.Scope == scope && authority.Ledger.Scope == scope
}

func goldenSubmissionParticipantIsActive(execution GoldenWaveExecution, participantID uuid.UUID) bool {
	member, found := FindMember(execution.Group.Members, participantID)
	return found && !member.Excluded
}

func goldenSubmissionSuccessorIDIsFresh(ledger GoldenSubmissionLedger, revisionID uuid.UUID) bool {
	return revisionID != ledger.RevisionID && !goldenSubmissionRevisionIDUsed(ledger, revisionID)
}

func goldenSubmissionAuthorityIsClosed(authority GoldenSubmissionAuthority) bool {
	return authority.TerminalCommitID != uuid.Nil || authority.TerminalCommitDigest != [sha256.Size]byte{} ||
		authority.Execution.Wave.State == domain.WaveStatePaused ||
		authority.Execution.Wave.State == domain.WaveStateCompleted ||
		authority.Execution.Attempt.State.IsTerminal()
}

func goldenSubmissionExecutionIsActive(execution GoldenWaveExecution) bool {
	return execution.Start != nil && execution.Wave.State == domain.WaveStateActive &&
		execution.Attempt.State == domain.GoldenAttemptStateActive
}

func goldenSubmissionActorOwnsExecution(
	command GoldenSubmissionCommand,
	execution GoldenWaveExecution,
) bool {
	return command.ActorParticipantID == command.ParticipantID &&
		ContainsID(execution.Membership.ParticipantIDs, command.ParticipantID) &&
		goldenSubmissionParticipantOwnsAssignment(execution, command.ParticipantID)
}

func goldenSubmissionVerificationMatches(
	verification GoldenSubmissionVerification,
	command GoldenSubmissionCommand,
	execution GoldenWaveExecution,
) bool {
	validIdentity := verification.Validate() == nil && verification.ID == command.VerificationID &&
		verification.Scope == command.Scope && verification.ParticipantID == command.ParticipantID
	validAuthority := verification.Authority == execution.Start.Authority.Identity &&
		verification.AssignmentDigest == execution.Assignment.PayloadDigest
	validTime := !verification.VerifiedAt.Before(execution.Start.StartedAt) &&
		!verification.VerifiedAt.After(execution.Start.Deadline)
	return validIdentity && validAuthority && validTime
}

func ScopeMatchesExecution(scope GoldenSubmissionScope, execution GoldenWaveExecution) bool {
	return scope.State == execution.Scope && scope.AttemptID == execution.Attempt.ID &&
		scope.WaveID == execution.Wave.ID && scope.AssignmentID == execution.Assignment.ID &&
		scope.SnapshotID == execution.Assignment.Snapshot.SnapshotID &&
		scope.TaskID == execution.Assignment.Snapshot.TaskID
}

func goldenSubmissionParticipantOwnsAssignment(execution GoldenWaveExecution, participantID uuid.UUID) bool {
	for _, assignment := range execution.Assignment.Private {
		if assignment.ParticipantID == participantID && assignment.SnapshotID == execution.Assignment.Snapshot.SnapshotID &&
			assignment.ContentDigest == execution.Assignment.ContentDigest {
			return true
		}
	}
	return false
}

// ApplyGoldenSubmissionCommit is the canonical transaction result builder for
// repository adapters. submissionID and committedAt must come from the same
// transaction that proves ExpectedExecution and ExpectedLedger.
