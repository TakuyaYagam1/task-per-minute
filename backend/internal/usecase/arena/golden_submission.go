package arena

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const goldenSubmissionCommitAttempts = 4

var (
	ErrInvalidGoldenSubmission           = errors.New("invalid Golden submission")
	ErrGoldenSubmissionAuthorityConflict = errors.New("golden submission authority conflict")
	ErrGoldenSubmissionConflict          = errors.New("golden submission commit conflict")
	ErrGoldenSubmissionCommandReuse      = errors.New("golden submission command identifier was reused")
	ErrGoldenSubmissionIncorrect         = errors.New("golden submission is not correct")
	ErrGoldenSubmissionClosed            = errors.New("golden submission window is closed")
	ErrGoldenSubmissionDuplicateLimit    = errors.New("golden submission duplicate limit reached")
)

type GoldenSubmissionDisposition string

const (
	GoldenSubmissionAccepted  GoldenSubmissionDisposition = "accepted"
	GoldenSubmissionDuplicate GoldenSubmissionDisposition = "duplicate_first_correct"
)

type GoldenSubmissionScope struct {
	State        GoldenStateScope
	AttemptID    uuid.UUID
	WaveID       uuid.UUID
	AssignmentID uuid.UUID
	SnapshotID   uuid.UUID
	TaskID       uuid.UUID
}

func (s GoldenSubmissionScope) IsValid() bool {
	if !validGoldenStateScope(s.State) || s.AttemptID == uuid.Nil || s.WaveID == uuid.Nil ||
		s.AssignmentID == uuid.Nil || s.SnapshotID == uuid.Nil || s.TaskID == uuid.Nil {
		return false
	}
	identities := []uuid.UUID{
		s.State.TournamentID, s.State.GroupID, s.State.GroupRevisionID.UUID(),
		s.AttemptID, s.WaveID, s.AssignmentID, s.SnapshotID, s.TaskID,
	}
	return uniqueNonZeroUUIDs(identities)
}

type GoldenSubmissionVerification struct {
	ID               uuid.UUID
	RevisionID       uuid.UUID
	Scope            GoldenSubmissionScope
	ParticipantID    uuid.UUID
	Correct          bool
	VerifiedAt       time.Time
	EvidenceDigest   [sha256.Size]byte
	Authority        ExecutionAuthorityIdentity
	AssignmentDigest [sha256.Size]byte
}

func (v GoldenSubmissionVerification) Validate() error {
	if v.ID == uuid.Nil || v.RevisionID == uuid.Nil || v.ID == v.RevisionID || !v.Scope.IsValid() ||
		v.ParticipantID == uuid.Nil || !validArenaServerTime(v.VerifiedAt) ||
		v.EvidenceDigest == [sha256.Size]byte{} || v.AssignmentDigest == [sha256.Size]byte{} ||
		v.Authority.Validate() != nil {
		return goldenSubmissionError("invalid correctness attestation")
	}
	if !uniqueNonZeroUUIDs([]uuid.UUID{
		v.ID, v.RevisionID, v.ParticipantID, v.Authority.HolderID, v.Authority.LeaseID,
	}) {
		return goldenSubmissionError("correctness attestation identity is aliased")
	}
	return nil
}

type GoldenSubmissionCommand struct {
	Scope                GoldenSubmissionScope
	CommandID            uuid.UUID
	ActorParticipantID   uuid.UUID
	ParticipantID        uuid.UUID
	VerificationID       uuid.UUID
	ExpectedExecution    GoldenWaveExecutionExpectation
	NextLedgerRevisionID uuid.UUID
}

type GoldenSubmissionRecord struct {
	ID                     uint64
	Scope                  GoldenSubmissionScope
	ParticipantID          uuid.UUID
	VerificationID         uuid.UUID
	VerificationRevisionID uuid.UUID
	EvidenceDigest         [sha256.Size]byte
	VerifiedAt             time.Time
	CommittedAt            time.Time
	Authority              ExecutionAuthorityIdentity
	AssignmentDigest       [sha256.Size]byte
}

type GoldenSubmissionLedgerExpectation struct {
	Scope            GoldenSubmissionScope
	RevisionID       uuid.UUID
	Revision         int64
	NextSubmissionID uint64
	PayloadDigest    [sha256.Size]byte
}

func (e GoldenSubmissionLedgerExpectation) Equal(other GoldenSubmissionLedgerExpectation) bool {
	return e == other
}

type GoldenSubmissionReceipt struct {
	CommandID        uuid.UUID
	Scope            GoldenSubmissionScope
	ParticipantID    uuid.UUID
	VerificationID   uuid.UUID
	CommandDigest    [sha256.Size]byte
	Disposition      GoldenSubmissionDisposition
	SubmissionID     uint64
	Expected         GoldenSubmissionLedgerExpectation
	ResultRevisionID uuid.UUID
	ResultRevision   int64
	CommittedAt      time.Time
}

type GoldenSubmissionLedger struct {
	Scope              GoldenSubmissionScope
	RevisionID         uuid.UUID
	Revision           int64
	PreviousRevisionID *uuid.UUID
	NextSubmissionID   uint64
	Submissions        []GoldenSubmissionRecord
	Receipts           []GoldenSubmissionReceipt
	PayloadDigest      [sha256.Size]byte
}

func NewGoldenSubmissionLedger(
	scope GoldenSubmissionScope,
	revisionID uuid.UUID,
) (GoldenSubmissionLedger, error) {
	ledger := GoldenSubmissionLedger{
		Scope: scope, RevisionID: revisionID, Revision: 1, NextSubmissionID: 1,
	}
	return buildGoldenSubmissionLedger(ledger)
}

func (l GoldenSubmissionLedger) Snapshot() GoldenSubmissionLedger {
	clone := l
	clone.PreviousRevisionID = cloneGoldenUUID(l.PreviousRevisionID)
	clone.Submissions = append([]GoldenSubmissionRecord(nil), l.Submissions...)
	clone.Receipts = append([]GoldenSubmissionReceipt(nil), l.Receipts...)
	return clone
}

func (l GoldenSubmissionLedger) Expectation() GoldenSubmissionLedgerExpectation {
	return GoldenSubmissionLedgerExpectation{
		Scope: l.Scope, RevisionID: l.RevisionID, Revision: l.Revision,
		NextSubmissionID: l.NextSubmissionID, PayloadDigest: l.PayloadDigest,
	}
}

func (l GoldenSubmissionLedger) HasParticipant(participantID uuid.UUID) bool {
	for _, submission := range l.Submissions {
		if submission.ParticipantID == participantID {
			return true
		}
	}
	return false
}

func (l GoldenSubmissionLedger) ProvisionalOrder() []GoldenSubmissionRecord {
	result := append([]GoldenSubmissionRecord(nil), l.Submissions...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].CommittedAt.Equal(result[j].CommittedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CommittedAt.Before(result[j].CommittedAt)
	})
	return result
}

func (l GoldenSubmissionLedger) Validate() error {
	if !l.Scope.IsValid() || l.RevisionID == uuid.Nil || l.Revision < 1 ||
		!validGoldenRevisionPredecessor(l.RevisionID, l.Revision, l.PreviousRevisionID) ||
		l.NextSubmissionID < 1 || len(l.Receipts) != int(l.Revision-1) {
		return goldenSubmissionError("invalid ledger identity or revision")
	}
	if err := validateGoldenSubmissionRecords(l); err != nil {
		return err
	}
	if err := validateGoldenSubmissionReceipts(l); err != nil {
		return err
	}
	payload, err := goldenSubmissionLedgerPayload(l)
	if err != nil || l.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != l.PayloadDigest {
		return goldenSubmissionError("ledger payload digest changed")
	}
	return nil
}

func validateGoldenSubmissionRecords(ledger GoldenSubmissionLedger) error {
	participants := make(map[uuid.UUID]struct{}, len(ledger.Submissions))
	verifications := make(map[uuid.UUID]struct{}, len(ledger.Submissions))
	expectedID := uint64(1)
	for _, submission := range ledger.Submissions {
		if !validGoldenSubmissionRecord(submission, ledger.Scope, expectedID) {
			return goldenSubmissionError("invalid retained correct submission")
		}
		if _, duplicate := participants[submission.ParticipantID]; duplicate {
			return goldenSubmissionError("participant has more than one correct submission")
		}
		if _, duplicate := verifications[submission.VerificationID]; duplicate {
			return goldenSubmissionError("correctness attestation is reused")
		}
		participants[submission.ParticipantID] = struct{}{}
		verifications[submission.VerificationID] = struct{}{}
		expectedID++
	}
	if ledger.NextSubmissionID != uint64(len(ledger.Submissions))+1 {
		return goldenSubmissionError("monotonic submission sequence changed")
	}
	return nil
}

func validGoldenSubmissionRecord(
	submission GoldenSubmissionRecord,
	scope GoldenSubmissionScope,
	expectedID uint64,
) bool {
	validIdentity := submission.ID == expectedID && submission.Scope == scope &&
		submission.ParticipantID != uuid.Nil && submission.VerificationID != uuid.Nil &&
		submission.VerificationRevisionID != uuid.Nil
	validEvidence := submission.EvidenceDigest != [sha256.Size]byte{} &&
		submission.AssignmentDigest != [sha256.Size]byte{} && submission.Authority.Validate() == nil
	validTime := validArenaServerTime(submission.VerifiedAt) && validArenaServerTime(submission.CommittedAt) &&
		!submission.CommittedAt.Before(submission.VerifiedAt)
	return validIdentity && validEvidence && validTime
}

func validateGoldenSubmissionReceipts(ledger GoldenSubmissionLedger) error {
	state := goldenSubmissionReceiptValidationState{
		commands:   make(map[uuid.UUID]struct{}, len(ledger.Receipts)),
		revisions:  make(map[uuid.UUID]struct{}, len(ledger.Receipts)+1),
		duplicates: make(map[uuid.UUID]struct{}),
		nextID:     1,
	}
	for index, receipt := range ledger.Receipts {
		if err := state.consume(ledger, receipt, index); err != nil {
			return err
		}
	}
	if len(ledger.Receipts) > 0 && (ledger.PreviousRevisionID == nil ||
		*ledger.PreviousRevisionID != ledger.Receipts[len(ledger.Receipts)-1].Expected.RevisionID) {
		return goldenSubmissionError("ledger predecessor changed")
	}
	if state.nextID != ledger.NextSubmissionID ||
		(len(ledger.Receipts) > 0 && state.previousResult != ledger.RevisionID) {
		return goldenSubmissionError("receipt chain does not reach the ledger head")
	}
	return nil
}

type goldenSubmissionReceiptValidationState struct {
	commands       map[uuid.UUID]struct{}
	revisions      map[uuid.UUID]struct{}
	duplicates     map[uuid.UUID]struct{}
	previousResult uuid.UUID
	nextID         uint64
}

func (s *goldenSubmissionReceiptValidationState) consume(
	ledger GoldenSubmissionLedger,
	receipt GoldenSubmissionReceipt,
	index int,
) error {
	if !validGoldenSubmissionReceiptHeader(receipt, ledger.Scope, index, s.previousResult) {
		return goldenSubmissionError("invalid retained command receipt")
	}
	if !validGoldenSubmissionReceiptExpected(receipt.Expected, s.nextID) {
		return goldenSubmissionError("command receipt expected head changed")
	}
	if _, duplicate := s.commands[receipt.CommandID]; duplicate {
		return goldenSubmissionError("duplicate retained command")
	}
	if index == 0 {
		s.revisions[receipt.Expected.RevisionID] = struct{}{}
	}
	if _, reused := s.revisions[receipt.ResultRevisionID]; reused {
		return goldenSubmissionError("submission ledger revision identity is reused")
	}
	submission, found := goldenSubmissionByID(ledger.Submissions, receipt.SubmissionID)
	if !found || submission.ParticipantID != receipt.ParticipantID {
		return goldenSubmissionError("command receipt references another participant submission")
	}
	if receipt.Disposition == GoldenSubmissionDuplicate {
		if _, duplicate := s.duplicates[receipt.ParticipantID]; duplicate {
			return goldenSubmissionError("participant duplicate receipt limit exceeded")
		}
		s.duplicates[receipt.ParticipantID] = struct{}{}
	}
	nextID, err := goldenSubmissionReceiptNextID(receipt, submission, s.nextID)
	if err != nil {
		return err
	}
	s.commands[receipt.CommandID] = struct{}{}
	s.revisions[receipt.ResultRevisionID] = struct{}{}
	s.previousResult = receipt.ResultRevisionID
	s.nextID = nextID
	return nil
}

func validGoldenSubmissionReceiptExpected(expected GoldenSubmissionLedgerExpectation, nextID uint64) bool {
	return expected.RevisionID != uuid.Nil && expected.Revision >= 1 &&
		expected.NextSubmissionID == nextID && expected.PayloadDigest != [sha256.Size]byte{}
}

func goldenSubmissionReceiptNextID(
	receipt GoldenSubmissionReceipt,
	submission GoldenSubmissionRecord,
	nextID uint64,
) (uint64, error) {
	switch receipt.Disposition {
	case GoldenSubmissionAccepted:
		if receipt.SubmissionID != nextID || receipt.VerificationID != submission.VerificationID ||
			!receipt.CommittedAt.Equal(submission.CommittedAt) {
			return 0, goldenSubmissionError("accepted receipt changed its retained submission")
		}
		return nextID + 1, nil
	case GoldenSubmissionDuplicate:
		if receipt.SubmissionID >= nextID {
			return 0, goldenSubmissionError("duplicate receipt references a future submission")
		}
		return nextID, nil
	default:
		return 0, goldenSubmissionError("invalid submission disposition")
	}
}

func goldenSubmissionByID(
	submissions []GoldenSubmissionRecord,
	submissionID uint64,
) (GoldenSubmissionRecord, bool) {
	if submissionID < 1 || submissionID > uint64(len(submissions)) {
		return GoldenSubmissionRecord{}, false
	}
	submission := submissions[submissionID-1]
	return submission, submission.ID == submissionID
}

func validGoldenSubmissionReceiptHeader(
	receipt GoldenSubmissionReceipt,
	scope GoldenSubmissionScope,
	index int,
	previousResult uuid.UUID,
) bool {
	validIdentity := receipt.CommandID != uuid.Nil && receipt.Scope == scope &&
		receipt.ParticipantID != uuid.Nil && receipt.VerificationID != uuid.Nil &&
		receipt.CommandDigest != [sha256.Size]byte{}
	validRevision := receipt.Expected.Scope == scope && receipt.ResultRevisionID != uuid.Nil &&
		receipt.ResultRevision == int64(index+2) && receipt.Expected.Revision == int64(index+1)
	validPrevious := index == 0 || receipt.Expected.RevisionID == previousResult
	return validIdentity && validRevision && validPrevious && validArenaServerTime(receipt.CommittedAt)
}

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

// GoldenSubmissionRepository uses one transaction and one lock order:
// execution, then submission ledger. It revalidates the active execution,
// correctness attestation, assignment ownership and both expected heads. The
// transaction assigns CommittedAt from transaction time and the next uint64 ID;
// callers cannot supply either. Accepted and first-duplicate command receipts
// remain globally durable after the current attempt is archived. A request
// rejected by the duplicate cap creates no receipt, revision or sequence. No
// position is written by this contract.
// The transaction globally reserves the command and successor revision IDs
// across current and archived Golden records.
// Adapters must also rate-limit ingress; the durable duplicate cap bounds
// storage amplification but is not a substitute for request throttling.
type GoldenSubmissionRepository interface {
	LoadGoldenSubmissionAuthority(
		ctx context.Context,
		scope GoldenSubmissionScope,
		commandID uuid.UUID,
		verificationID uuid.UUID,
	) (GoldenSubmissionAuthority, error)
	CommitGoldenSubmission(
		ctx context.Context,
		commit GoldenSubmissionCommit,
	) (*GoldenSubmissionLedger, bool, error)
}

type GoldenSubmissionUseCase struct {
	repository GoldenSubmissionRepository
}

func NewGoldenSubmissionUseCase(repository GoldenSubmissionRepository) *GoldenSubmissionUseCase {
	return &GoldenSubmissionUseCase{repository: repository}
}

func (u *GoldenSubmissionUseCase) Submit(
	ctx context.Context,
	command GoldenSubmissionCommand,
) (*GoldenSubmissionLedger, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	command.ExpectedExecution = cloneGoldenExecutionExpectationValue(command.ExpectedExecution)
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
		ExpectedExecution: cloneGoldenExecutionExpectationValue(command.ExpectedExecution),
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
	if !uniqueNonZeroUUIDs([]uuid.UUID{
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
	if !goldenSubmissionScopeMatchesExecution(command.Scope, execution) {
		return goldenSubmissionError("command is not bound to the active assignment")
	}
	if !goldenSubmissionActorOwnsExecution(command, execution) {
		return domain.ErrArenaAssignmentParticipant
	}
	if !goldenSubmissionParticipantIsActive(execution, command.ParticipantID) {
		return domain.ErrArenaAssignmentParticipant
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
	reserved := goldenSubmissionRetainedIdentitySet(authority.Execution)
	reserveGoldenSubmissionLedgerIdentities(reserved, authority.Ledger)
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

func goldenSubmissionRetainedIdentitySet(execution GoldenWaveExecution) map[uuid.UUID]struct{} {
	reserved := goldenExecutionIdentitySet(execution)
	values := []uuid.UUID{
		execution.Scope.TournamentID, execution.Scope.GroupID, execution.Scope.GroupRevisionID.UUID(),
		execution.Source.RevisionID, execution.Source.Membership.RevisionID,
		execution.Source.Plan.PlanID, execution.Source.Plan.RevisionID,
		execution.Source.SourceProjectionRevisionID.UUID(),
	}
	if execution.Source.Membership.PreviousRevisionID != nil {
		values = append(values, *execution.Source.Membership.PreviousRevisionID)
	}
	for _, member := range execution.Group.Members {
		values = append(values, member.ParticipantID)
	}
	for _, attempt := range execution.Group.Attempts {
		values = append(values, attempt.ID)
		values = append(values, attempt.ParticipantIDs...)
	}
	if execution.Start != nil {
		values = append(values,
			execution.Start.CommandID, execution.Start.ResultExecutionRevisionID,
			execution.Start.ResultWindowRevisionID, execution.Start.Authority.Identity.HolderID,
			execution.Start.Authority.Identity.LeaseID,
		)
		for _, assignment := range execution.Start.Assignments {
			values = append(values, assignment.AssignmentID, assignment.ParticipantID, assignment.SnapshotID)
		}
	}
	for _, value := range values {
		if value != uuid.Nil {
			reserved[value] = struct{}{}
		}
	}
	return reserved
}

func goldenSubmissionAuthorityScopeMatches(authority GoldenSubmissionAuthority, scope GoldenSubmissionScope) bool {
	return authority.Scope == scope && authority.Ledger.Scope == scope
}

func goldenSubmissionParticipantIsActive(execution GoldenWaveExecution, participantID uuid.UUID) bool {
	member, found := goldenStateMember(execution.Group.Members, participantID)
	return found && !member.Excluded
}

func goldenSubmissionSuccessorIDIsFresh(ledger GoldenSubmissionLedger, revisionID uuid.UUID) bool {
	return revisionID != ledger.RevisionID && !goldenSubmissionRevisionIDUsed(ledger, revisionID)
}

func goldenSubmissionAuthorityIsClosed(authority GoldenSubmissionAuthority) bool {
	return authority.TerminalCommitID != uuid.Nil || authority.TerminalCommitDigest != [sha256.Size]byte{} ||
		authority.Execution.Wave.State == domain.ArenaWaveStatePaused ||
		authority.Execution.Wave.State == domain.ArenaWaveStateCompleted ||
		authority.Execution.Attempt.State.IsTerminal()
}

func goldenSubmissionExecutionIsActive(execution GoldenWaveExecution) bool {
	return execution.Start != nil && execution.Wave.State == domain.ArenaWaveStateActive &&
		execution.Attempt.State == domain.ArenaGoldenAttemptStateActive
}

func goldenSubmissionActorOwnsExecution(
	command GoldenSubmissionCommand,
	execution GoldenWaveExecution,
) bool {
	return command.ActorParticipantID == command.ParticipantID &&
		goldenIDsContain(execution.Membership.ParticipantIDs, command.ParticipantID) &&
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

func goldenSubmissionScopeMatchesExecution(scope GoldenSubmissionScope, execution GoldenWaveExecution) bool {
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
	if !validArenaServerTime(committedAt) || !committedAt.Before(commit.Deadline) {
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
		!validArenaServerTime(committedAt) || committedAt.Before(commit.Verification.VerifiedAt) ||
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
	next.PreviousRevisionID = goldenUUID(next.RevisionID)
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
	return goldenEncode(struct {
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
	payload, _ := goldenEncode(command)
	return sha256.Sum256(payload)
}

func cloneGoldenExecutionExpectationValue(
	expectation GoldenWaveExecutionExpectation,
) GoldenWaveExecutionExpectation {
	clone := expectation
	clone.Source = cloneGoldenStateExpectation(expectation.Source)
	return clone
}

func goldenSubmissionError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenSubmission, message)
}
