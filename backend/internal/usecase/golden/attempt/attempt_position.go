package golden

import (
	"crypto/sha256"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GoldenSwissPointLedgerSentinel struct {
	RevisionID uuid.UUID
	Revision   int64
	Digest     [sha256.Size]byte
}

func (s GoldenSwissPointLedgerSentinel) Validate() error {
	if s.RevisionID == uuid.Nil || s.Revision < 1 || s.Digest == [sha256.Size]byte{} {
		return goldenAttemptCommitError("invalid Swiss point ledger sentinel")
	}
	return nil
}

type GoldenPositionOrderEntry struct {
	SubmissionID   uint64
	ParticipantID  uuid.UUID
	CommittedAt    time.Time
	EvidenceDigest [sha256.Size]byte
}

type GoldenAttemptOrderingEvidence struct {
	AttemptID      uuid.UUID
	AttemptNo      int
	SubmissionHead GoldenSubmissionLedgerExpectation
	Order          []GoldenPositionOrderEntry
	PayloadDigest  [sha256.Size]byte
}

func (e GoldenAttemptOrderingEvidence) Snapshot() GoldenAttemptOrderingEvidence {
	clone := e
	clone.Order = append([]GoldenPositionOrderEntry(nil), e.Order...)
	return clone
}

func (e GoldenAttemptOrderingEvidence) Validate() error {
	if !validGoldenAttemptOrderingHeader(e) {
		return goldenAttemptCommitError("invalid ordering evidence identity")
	}
	if e.SubmissionHead.NextSubmissionID != uint64(len(e.Order))+1 {
		return goldenAttemptCommitError("ordering evidence does not cover its submission head")
	}
	if err := validateGoldenPositionOrder(e); err != nil {
		return err
	}
	payload, err := goldenAttemptOrderingPayload(e)
	if err != nil || e.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != e.PayloadDigest {
		return goldenAttemptCommitError("ordering evidence digest changed")
	}
	return nil
}

func validGoldenAttemptOrderingHeader(e GoldenAttemptOrderingEvidence) bool {
	return e.AttemptID != uuid.Nil && e.AttemptNo >= 1 && e.SubmissionHead.Scope.IsValid() &&
		e.SubmissionHead.RevisionID != uuid.Nil && e.SubmissionHead.Revision >= 1 &&
		e.SubmissionHead.NextSubmissionID >= 1 && e.SubmissionHead.PayloadDigest != [sha256.Size]byte{}
}

func validateGoldenPositionOrder(e GoldenAttemptOrderingEvidence) error {
	seen := make(map[uuid.UUID]struct{}, len(e.Order))
	seenSubmissions := make(map[uint64]struct{}, len(e.Order))
	var previous GoldenPositionOrderEntry
	for index, item := range e.Order {
		if !validGoldenPositionOrderEntry(item, e.SubmissionHead.NextSubmissionID) {
			return goldenAttemptCommitError("invalid provisional ordering evidence")
		}
		if index > 0 && !goldenPositionOrderEntryAfter(previous, item) {
			return goldenAttemptCommitError("provisional ordering evidence is not deterministic")
		}
		if _, duplicate := seen[item.ParticipantID]; duplicate {
			return goldenAttemptCommitError("ordering evidence repeats a participant")
		}
		if _, duplicate := seenSubmissions[item.SubmissionID]; duplicate {
			return goldenAttemptCommitError("ordering evidence repeats a submission")
		}
		seen[item.ParticipantID] = struct{}{}
		seenSubmissions[item.SubmissionID] = struct{}{}
		previous = item
	}
	return nil
}

func validGoldenPositionOrderEntry(item GoldenPositionOrderEntry, nextSubmissionID uint64) bool {
	return item.SubmissionID >= 1 && item.SubmissionID < nextSubmissionID && item.ParticipantID != uuid.Nil &&
		domain.IsValidServerTime(item.CommittedAt) && item.EvidenceDigest != [sha256.Size]byte{}
}

func goldenPositionOrderEntryAfter(previous, current GoldenPositionOrderEntry) bool {
	return current.CommittedAt.After(previous.CommittedAt) ||
		(current.CommittedAt.Equal(previous.CommittedAt) && current.SubmissionID > previous.SubmissionID)
}

type GoldenCommittedPosition struct {
	Position       int
	ParticipantID  uuid.UUID
	AttemptID      uuid.UUID
	AttemptNo      int
	SubmissionID   uint64
	EvidenceDigest [sha256.Size]byte
	CommitID       uuid.UUID
}

type GoldenPositionLedgerExpectation struct {
	Scope         GoldenStateScope
	RevisionID    uuid.UUID
	Revision      int64
	PositionFrom  int
	PositionTo    int
	PositionCount int
	PayloadDigest [sha256.Size]byte
}

func (e GoldenPositionLedgerExpectation) Equal(other GoldenPositionLedgerExpectation) bool {
	return e == other
}

type GoldenPositionLedger struct {
	Scope              GoldenStateScope
	RevisionID         uuid.UUID
	Revision           int64
	PreviousRevisionID *uuid.UUID
	RevisionIDs        []uuid.UUID
	PositionFrom       int
	PositionTo         int
	Positions          []GoldenCommittedPosition
	Attempts           []GoldenAttemptOrderingEvidence
	PayloadDigest      [sha256.Size]byte
}

func NewGoldenPositionLedger(
	scope GoldenStateScope,
	positionFrom int,
	positionTo int,
	revisionID uuid.UUID,
) (GoldenPositionLedger, error) {
	ledger := GoldenPositionLedger{
		Scope: scope, RevisionID: revisionID, Revision: 1,
		RevisionIDs: []uuid.UUID{revisionID}, PositionFrom: positionFrom, PositionTo: positionTo,
	}
	return buildGoldenPositionLedger(ledger)
}

func (l GoldenPositionLedger) Snapshot() GoldenPositionLedger {
	clone := l
	clone.PreviousRevisionID = attemptCloneUUIDPointer(l.PreviousRevisionID)
	clone.RevisionIDs = append([]uuid.UUID(nil), l.RevisionIDs...)
	clone.Positions = append([]GoldenCommittedPosition(nil), l.Positions...)
	clone.Attempts = make([]GoldenAttemptOrderingEvidence, len(l.Attempts))
	for index, attempt := range l.Attempts {
		clone.Attempts[index] = attempt.Snapshot()
	}
	return clone
}

func (l GoldenPositionLedger) Expectation() GoldenPositionLedgerExpectation {
	return GoldenPositionLedgerExpectation{
		Scope: l.Scope, RevisionID: l.RevisionID, Revision: l.Revision,
		PositionFrom: l.PositionFrom, PositionTo: l.PositionTo,
		PositionCount: len(l.Positions), PayloadDigest: l.PayloadDigest,
	}
}

func (l GoldenPositionLedger) Validate() error {
	if !validGoldenPositionLedgerHeader(l) {
		return goldenAttemptCommitError("invalid position ledger identity or interval")
	}
	if err := validateGoldenCommittedPositions(l); err != nil {
		return err
	}
	for _, attempt := range l.Attempts {
		if err := attempt.Validate(); err != nil {
			return err
		}
	}
	if err := validateGoldenPositionHistory(l); err != nil {
		return err
	}
	payload, err := goldenPositionLedgerPayload(l)
	if err != nil || l.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != l.PayloadDigest {
		return goldenAttemptCommitError("position ledger digest changed")
	}
	return nil
}

func validateGoldenPositionHistory(ledger GoldenPositionLedger) error {
	attemptIDs := make(map[uuid.UUID]struct{}, len(ledger.Attempts))
	commitIDs := make(map[uuid.UUID]struct{}, len(ledger.Attempts))
	positionIndex := 0
	previousAttemptNo := 0
	for _, attempt := range ledger.Attempts {
		if !validGoldenPositionAttemptSuccessor(ledger.Scope, attempt, previousAttemptNo) {
			return goldenAttemptCommitError("position attempt history is not sequential")
		}
		previousAttemptNo = attempt.AttemptNo
		if _, duplicate := attemptIDs[attempt.AttemptID]; duplicate {
			return goldenAttemptCommitError("position attempt identity is reused")
		}
		attemptIDs[attempt.AttemptID] = struct{}{}
		attemptCommitID, nextPositionIndex, err := validateGoldenPositionAttemptEntries(
			ledger.Positions, attempt, positionIndex,
		)
		if err != nil {
			return err
		}
		positionIndex = nextPositionIndex
		if attemptCommitID != uuid.Nil {
			if _, duplicate := commitIDs[attemptCommitID]; duplicate {
				return goldenAttemptCommitError("terminal commit identity is reused")
			}
			commitIDs[attemptCommitID] = struct{}{}
		}
	}
	if positionIndex != len(ledger.Positions) {
		return goldenAttemptCommitError("committed position lacks attempt ordering evidence")
	}
	return nil
}

func validGoldenPositionAttemptSuccessor(
	scope GoldenStateScope,
	attempt GoldenAttemptOrderingEvidence,
	previousAttemptNo int,
) bool {
	return attempt.AttemptNo > previousAttemptNo && attempt.SubmissionHead.Scope.State == scope &&
		attempt.SubmissionHead.Scope.AttemptID == attempt.AttemptID
}

func validateGoldenPositionAttemptEntries(
	positions []GoldenCommittedPosition,
	attempt GoldenAttemptOrderingEvidence,
	positionIndex int,
) (uuid.UUID, int, error) {
	var commitID uuid.UUID
	for _, item := range attempt.Order {
		if positionIndex >= len(positions) {
			return uuid.Nil, positionIndex, goldenAttemptCommitError("position history exceeds committed positions")
		}
		position := positions[positionIndex]
		if !goldenPositionMatchesOrderEntry(position, attempt, item) {
			return uuid.Nil, positionIndex, goldenAttemptCommitError("position history changed ordering evidence")
		}
		if commitID != uuid.Nil && commitID != position.CommitID {
			return uuid.Nil, positionIndex, goldenAttemptCommitError("attempt positions were not committed atomically")
		}
		commitID = position.CommitID
		positionIndex++
	}
	return commitID, positionIndex, nil
}

func goldenPositionMatchesOrderEntry(
	position GoldenCommittedPosition,
	attempt GoldenAttemptOrderingEvidence,
	item GoldenPositionOrderEntry,
) bool {
	return position.AttemptID == attempt.AttemptID && position.AttemptNo == attempt.AttemptNo &&
		position.SubmissionID == item.SubmissionID && position.ParticipantID == item.ParticipantID &&
		position.EvidenceDigest == item.EvidenceDigest
}

func validGoldenPositionLedgerHeader(l GoldenPositionLedger) bool {
	validIdentity := ValidStateScope(l.Scope) && l.RevisionID != uuid.Nil && l.Revision >= 1 &&
		attemptValidRevisionPredecessor(l.RevisionID, l.Revision, l.PreviousRevisionID)
	validInterval := l.PositionFrom >= 1 && l.PositionTo >= l.PositionFrom &&
		l.PositionTo < math.MaxInt && len(l.Positions) <= l.PositionTo-l.PositionFrom+1
	return validIdentity && validInterval && len(l.Attempts) == int(l.Revision-1) &&
		validGoldenPositionRevisionLineage(l)
}

func validGoldenPositionRevisionLineage(l GoldenPositionLedger) bool {
	if int64(len(l.RevisionIDs)) != l.Revision || len(l.RevisionIDs) == 0 ||
		l.RevisionIDs[len(l.RevisionIDs)-1] != l.RevisionID {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(l.RevisionIDs))
	for _, revisionID := range l.RevisionIDs {
		if revisionID == uuid.Nil {
			return false
		}
		if _, duplicate := seen[revisionID]; duplicate {
			return false
		}
		seen[revisionID] = struct{}{}
	}
	if l.Revision == 1 {
		return l.PreviousRevisionID == nil
	}
	return l.PreviousRevisionID != nil && *l.PreviousRevisionID == l.RevisionIDs[len(l.RevisionIDs)-2]
}

func validateGoldenCommittedPositions(l GoldenPositionLedger) error {
	participants := make(map[uuid.UUID]struct{}, len(l.Positions))
	for index, position := range l.Positions {
		if !validGoldenCommittedPosition(position, l.PositionFrom+index) {
			return goldenAttemptCommitError("invalid committed position")
		}
		if _, duplicate := participants[position.ParticipantID]; duplicate {
			return goldenAttemptCommitError("participant has more than one position")
		}
		participants[position.ParticipantID] = struct{}{}
	}
	return nil
}

func validGoldenCommittedPosition(position GoldenCommittedPosition, expected int) bool {
	return position.Position == expected && position.ParticipantID != uuid.Nil &&
		position.AttemptID != uuid.Nil && position.AttemptNo >= 1 && position.SubmissionID > 0 &&
		position.EvidenceDigest != [sha256.Size]byte{} && position.CommitID != uuid.Nil
}
