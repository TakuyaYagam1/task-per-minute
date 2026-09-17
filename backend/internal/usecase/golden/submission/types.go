package submission

import (
	"context"
	"crypto/sha256"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
)

const commitAttempts = 4

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

type GoldenSubmissionVerification struct {
	ID               uuid.UUID
	RevisionID       uuid.UUID
	Scope            GoldenSubmissionScope
	ParticipantID    uuid.UUID
	Correct          bool
	VerifiedAt       time.Time
	EvidenceDigest   [sha256.Size]byte
	Authority        authoritydomain.Identity
	AssignmentDigest [sha256.Size]byte
}

func (v GoldenSubmissionVerification) Validate() error {
	if v.ID == uuid.Nil || v.RevisionID == uuid.Nil || v.ID == v.RevisionID || !v.Scope.IsValid() ||
		v.ParticipantID == uuid.Nil || !domain.IsValidServerTime(v.VerifiedAt) ||
		v.EvidenceDigest == [sha256.Size]byte{} || v.AssignmentDigest == [sha256.Size]byte{} ||
		v.Authority.Validate() != nil {
		return submissionError("invalid correctness attestation")
	}
	if !validSubmissionIdentitySet([]uuid.UUID{
		v.ID, v.RevisionID, v.ParticipantID, v.Authority.HolderID, v.Authority.LeaseID,
	}) {
		return submissionError("correctness attestation identity is aliased")
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
	Authority              authoritydomain.Identity
	AssignmentDigest       [sha256.Size]byte
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

func NewGoldenSubmissionLedger(scope GoldenSubmissionScope, revisionID uuid.UUID) (GoldenSubmissionLedger, error) {
	ledger := GoldenSubmissionLedger{
		Scope: scope, RevisionID: revisionID, Revision: 1, NextSubmissionID: 1,
	}
	return buildSubmissionLedger(ledger)
}

func (l GoldenSubmissionLedger) Snapshot() GoldenSubmissionLedger {
	clone := l
	clone.PreviousRevisionID = cloneSubmissionUUIDPointer(l.PreviousRevisionID)
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
		!validSubmissionRevisionPredecessor(l.RevisionID, l.Revision, l.PreviousRevisionID) ||
		l.NextSubmissionID < 1 || len(l.Receipts) != int(l.Revision-1) {
		return submissionError("invalid ledger identity or revision")
	}
	if err := validateSubmissionRecords(l); err != nil {
		return err
	}
	if err := validateSubmissionReceipts(l); err != nil {
		return err
	}
	payload, err := submissionLedgerPayload(l)
	if err != nil || l.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != l.PayloadDigest {
		return submissionError("ledger payload digest changed")
	}
	return nil
}

func ValidRecord(submission GoldenSubmissionRecord, scope GoldenSubmissionScope, expectedID uint64) bool {
	validIdentity := submission.ID == expectedID && submission.Scope == scope &&
		submission.ParticipantID != uuid.Nil && submission.VerificationID != uuid.Nil &&
		submission.VerificationRevisionID != uuid.Nil
	validEvidence := submission.EvidenceDigest != [sha256.Size]byte{} &&
		submission.AssignmentDigest != [sha256.Size]byte{} && submission.Authority.Validate() == nil
	validTime := domain.IsValidServerTime(submission.VerifiedAt) && domain.IsValidServerTime(submission.CommittedAt) &&
		!submission.CommittedAt.Before(submission.VerifiedAt)
	return validIdentity && validEvidence && validTime
}

type submissionReceiptValidationState struct {
	commands       map[uuid.UUID]struct{}
	revisions      map[uuid.UUID]struct{}
	duplicates     map[uuid.UUID]struct{}
	previousResult uuid.UUID
	nextID         uint64
}

func validateSubmissionRecords(ledger GoldenSubmissionLedger) error {
	participants := make(map[uuid.UUID]struct{}, len(ledger.Submissions))
	verifications := make(map[uuid.UUID]struct{}, len(ledger.Submissions))
	expectedID := uint64(1)
	for _, submission := range ledger.Submissions {
		if !ValidRecord(submission, ledger.Scope, expectedID) {
			return submissionError("invalid retained correct submission")
		}
		if _, duplicate := participants[submission.ParticipantID]; duplicate {
			return submissionError("participant has more than one correct submission")
		}
		if _, duplicate := verifications[submission.VerificationID]; duplicate {
			return submissionError("correctness attestation is reused")
		}
		participants[submission.ParticipantID] = struct{}{}
		verifications[submission.VerificationID] = struct{}{}
		expectedID++
	}
	if ledger.NextSubmissionID != uint64(len(ledger.Submissions))+1 {
		return submissionError("monotonic submission sequence changed")
	}
	return nil
}

func validateSubmissionReceipts(ledger GoldenSubmissionLedger) error {
	state := submissionReceiptValidationState{
		commands:   make(map[uuid.UUID]struct{}, len(ledger.Receipts)),
		revisions:  make(map[uuid.UUID]struct{}, len(ledger.Receipts)+1),
		duplicates: make(map[uuid.UUID]struct{}), nextID: 1,
	}
	for index, receipt := range ledger.Receipts {
		if err := state.consume(ledger, receipt, index); err != nil {
			return err
		}
	}
	if len(ledger.Receipts) > 0 && (ledger.PreviousRevisionID == nil ||
		*ledger.PreviousRevisionID != ledger.Receipts[len(ledger.Receipts)-1].Expected.RevisionID) {
		return submissionError("ledger predecessor changed")
	}
	if state.nextID != ledger.NextSubmissionID ||
		(len(ledger.Receipts) > 0 && state.previousResult != ledger.RevisionID) {
		return submissionError("receipt chain does not reach the ledger head")
	}
	return nil
}

func (s *submissionReceiptValidationState) consume(
	ledger GoldenSubmissionLedger,
	receipt GoldenSubmissionReceipt,
	index int,
) error {
	if !validSubmissionReceiptHeader(receipt, ledger.Scope, index, s.previousResult) {
		return submissionError("invalid retained command receipt")
	}
	if !validSubmissionReceiptExpected(receipt.Expected, s.nextID) {
		return submissionError("command receipt expected head changed")
	}
	if _, duplicate := s.commands[receipt.CommandID]; duplicate {
		return submissionError("duplicate retained command")
	}
	if index == 0 {
		s.revisions[receipt.Expected.RevisionID] = struct{}{}
	}
	if _, reused := s.revisions[receipt.ResultRevisionID]; reused {
		return submissionError("submission ledger revision identity is reused")
	}
	submission, found := submissionByID(ledger.Submissions, receipt.SubmissionID)
	if !found || submission.ParticipantID != receipt.ParticipantID {
		return submissionError("command receipt references another participant submission")
	}
	if receipt.Disposition == GoldenSubmissionDuplicate {
		if _, duplicate := s.duplicates[receipt.ParticipantID]; duplicate {
			return submissionError("participant duplicate receipt limit exceeded")
		}
		s.duplicates[receipt.ParticipantID] = struct{}{}
	}
	nextID, err := submissionReceiptNextID(receipt, submission, s.nextID)
	if err != nil {
		return err
	}
	s.commands[receipt.CommandID] = struct{}{}
	s.revisions[receipt.ResultRevisionID] = struct{}{}
	s.previousResult = receipt.ResultRevisionID
	s.nextID = nextID
	return nil
}

func validSubmissionReceiptExpected(expected GoldenSubmissionLedgerExpectation, nextID uint64) bool {
	return expected.RevisionID != uuid.Nil && expected.Revision >= 1 &&
		expected.NextSubmissionID == nextID && expected.PayloadDigest != [sha256.Size]byte{}
}

func submissionReceiptNextID(
	receipt GoldenSubmissionReceipt,
	submission GoldenSubmissionRecord,
	nextID uint64,
) (uint64, error) {
	switch receipt.Disposition {
	case GoldenSubmissionAccepted:
		if receipt.SubmissionID != nextID || receipt.VerificationID != submission.VerificationID ||
			!receipt.CommittedAt.Equal(submission.CommittedAt) {
			return 0, submissionError("accepted receipt changed its retained submission")
		}
		return nextID + 1, nil
	case GoldenSubmissionDuplicate:
		if receipt.SubmissionID >= nextID {
			return 0, submissionError("duplicate receipt references a future submission")
		}
		return nextID, nil
	default:
		return 0, submissionError("invalid submission disposition")
	}
}

func submissionByID(submissions []GoldenSubmissionRecord, submissionID uint64) (GoldenSubmissionRecord, bool) {
	if submissionID < 1 || submissionID > uint64(len(submissions)) {
		return GoldenSubmissionRecord{}, false
	}
	submission := submissions[submissionID-1]
	return submission, submission.ID == submissionID
}

func validSubmissionReceiptHeader(
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
	return validIdentity && validRevision && validPrevious && domain.IsValidServerTime(receipt.CommittedAt)
}

// SubmissionRepository owns the durable submission-ledger transaction.
type SubmissionRepository interface {
	LoadGoldenSubmissionAuthority(
		ctx context.Context,
		scope GoldenSubmissionScope,
		commandID uuid.UUID,
		verificationID uuid.UUID,
	) (GoldenSubmissionAuthority, error)
	CommitGoldenSubmission(ctx context.Context, commit GoldenSubmissionCommit) (*GoldenSubmissionLedger, bool, error)
}
