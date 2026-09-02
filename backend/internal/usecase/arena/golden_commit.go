package arena

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

const goldenAttemptCommitAttempts = 3

var (
	ErrInvalidGoldenAttemptCommit           = errors.New("invalid Golden attempt commit")
	ErrGoldenAttemptCommitAuthorityConflict = errors.New("golden attempt commit authority conflict")
	ErrGoldenAttemptCommitConflict          = errors.New("golden attempt commit conflict")
	ErrGoldenAttemptCommitCommandReuse      = errors.New("golden attempt commit command identifier was reused")
	ErrGoldenAttemptNotTerminal             = errors.New("golden attempt is not terminal")
)

type GoldenAttemptTerminalReason string

const (
	GoldenAttemptTerminalAllSolved GoldenAttemptTerminalReason = "all_solved"
	GoldenAttemptTerminalDeadline  GoldenAttemptTerminalReason = "deadline"
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
		validArenaServerTime(item.CommittedAt) && item.EvidenceDigest != [sha256.Size]byte{}
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
	clone.PreviousRevisionID = cloneGoldenUUID(l.PreviousRevisionID)
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
	validIdentity := validGoldenStateScope(l.Scope) && l.RevisionID != uuid.Nil && l.Revision >= 1 &&
		validGoldenRevisionPredecessor(l.RevisionID, l.Revision, l.PreviousRevisionID)
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

type GoldenAttemptCommitCommand struct {
	Scope                  GoldenSubmissionScope
	CommandID              uuid.UUID
	CommitID               uuid.UUID
	ExpectedExecution      GoldenWaveExecutionExpectation
	ExpectedSubmissions    GoldenSubmissionLedgerExpectation
	ExpectedPositions      GoldenPositionLedgerExpectation
	ExpectedSwissPoints    GoldenSwissPointLedgerSentinel
	NextPositionRevisionID uuid.UUID
	Reason                 GoldenAttemptTerminalReason
}

type GoldenAttemptCommitAuthority struct {
	Scope       GoldenSubmissionScope
	Execution   GoldenWaveExecution
	Submissions GoldenSubmissionLedger
	Positions   GoldenPositionLedger
	SwissPoints GoldenSwissPointLedgerSentinel
}

// GoldenAttemptAssignmentEvidence is the immutable, sanitized assignment
// authority retained by a terminal receipt. It deliberately carries only task
// snapshot identities and digests, never the snapshot's secret material.
type GoldenAttemptAssignmentEvidence struct {
	ID                     uuid.UUID
	RevisionID             uuid.UUID
	Revision               int64
	Scope                  GoldenStateScope
	AttemptID              uuid.UUID
	WaveID                 uuid.UUID
	MembershipID           uuid.UUID
	Plan                   GoldenPlanStateBinding
	EdgeID                 uuid.UUID
	ReservationID          uuid.UUID
	SnapshotID             uuid.UUID
	TaskID                 uuid.UUID
	ContentDigest          [sha256.Size]byte
	Private                []GoldenPrivateAssignment
	ExecutionPayloadDigest [sha256.Size]byte
	PayloadDigest          [sha256.Size]byte
}

func (e GoldenAttemptAssignmentEvidence) Snapshot() GoldenAttemptAssignmentEvidence {
	clone := e
	clone.Private = append([]GoldenPrivateAssignment(nil), e.Private...)
	return clone
}

func (e GoldenAttemptAssignmentEvidence) Validate() error {
	ids := make([]uuid.UUID, 0, 11+len(e.Private))
	ids = append(ids,
		e.ID, e.RevisionID, e.AttemptID, e.WaveID, e.MembershipID, e.Plan.PlanID,
		e.Plan.RevisionID, e.EdgeID, e.ReservationID, e.SnapshotID, e.TaskID,
	)
	if !validGoldenAttemptAssignmentEvidenceHeader(e) {
		return goldenAttemptCommitError("invalid sanitized assignment evidence")
	}
	participants, privateIDs, validPrivate := goldenAttemptAssignmentPrivateEvidence(e)
	if !validPrivate {
		return goldenAttemptCommitError("invalid private assignment evidence")
	}
	ids = append(ids, privateIDs...)
	canonicalGoldenIDs(participants)
	canonicalGoldenIDs(privateIDs)
	if !uniqueNonZeroUUIDs(ids) || !goldenIDsAreCanonical(participants) || !goldenIDsAreCanonical(privateIDs) {
		return goldenAttemptCommitError("assignment evidence repeats a participant or identity")
	}
	payload, err := goldenAttemptAssignmentEvidencePayload(e)
	if err != nil || e.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != e.PayloadDigest {
		return goldenAttemptCommitError("assignment evidence digest changed")
	}
	return nil
}

func validGoldenAttemptAssignmentEvidenceHeader(e GoldenAttemptAssignmentEvidence) bool {
	return validGoldenStateScope(e.Scope) && e.Revision == 1 && e.Plan.GroupID == e.Scope.GroupID &&
		e.Plan.GroupRevisionID == e.Scope.GroupRevisionID && e.ContentDigest != [sha256.Size]byte{} &&
		e.ExecutionPayloadDigest != [sha256.Size]byte{} && len(e.Private) >= 2
}

func goldenAttemptAssignmentPrivateEvidence(
	e GoldenAttemptAssignmentEvidence,
) ([]uuid.UUID, []uuid.UUID, bool) {
	participants := make([]uuid.UUID, len(e.Private))
	privateIDs := make([]uuid.UUID, len(e.Private))
	for index, private := range e.Private {
		if private.ID == uuid.Nil || private.ParticipantID == uuid.Nil || private.SnapshotID != e.SnapshotID ||
			private.ContentDigest != e.ContentDigest {
			return nil, nil, false
		}
		participants[index] = private.ParticipantID
		privateIDs[index] = private.ID
	}
	return participants, privateIDs, true
}

type GoldenAttemptCommitRecord struct {
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
	Attempt             domain.ArenaGoldenAttempt
	Group               domain.ArenaGoldenGroupState
	Wave                domain.ArenaWave
	Ordering            GoldenAttemptOrderingEvidence
	PriorPositions      GoldenPositionLedger
	Positions           GoldenPositionLedger
	PayloadDigest       [sha256.Size]byte
}

func (r GoldenAttemptCommitRecord) Snapshot() GoldenAttemptCommitRecord {
	clone := r
	clone.ActiveExecution = cloneGoldenExecutionExpectationValue(r.ActiveExecution)
	clone.Assignment = r.Assignment.Snapshot()
	clone.Attempt = cloneGoldenAttempt(r.Attempt)
	clone.Group = cloneGoldenStateGroup(r.Group)
	clone.Wave = cloneArenaWaveExecution(r.Wave)
	clone.Ordering = r.Ordering.Snapshot()
	clone.PriorPositions = r.PriorPositions.Snapshot()
	clone.Positions = r.Positions.Snapshot()
	return clone
}

func (r GoldenAttemptCommitRecord) Validate() error {
	if !validGoldenAttemptCommitHeader(r) || !validGoldenAttemptCommitTerminal(r) {
		return goldenAttemptCommitError("invalid terminal record identity or evidence")
	}
	if !goldenAttemptCommitRetainsTerminalAttempt(r) {
		return goldenAttemptCommitError("completed group does not retain terminal attempt")
	}
	if err := r.Ordering.Validate(); err != nil {
		return err
	}
	if err := r.Positions.Validate(); err != nil {
		return err
	}
	if !goldenAttemptCommitPriorPositionsMatch(r) {
		return goldenAttemptCommitError("prior position commitment changed")
	}
	if err := validateGoldenAttemptPositionAuthority(r); err != nil {
		return err
	}
	if !goldenAttemptCommitOrderingMatches(r) {
		return goldenAttemptCommitError("terminal ordering is not bound to the attempt")
	}
	if err := validateGoldenAttemptPositionLinks(r); err != nil {
		return err
	}
	payload, err := goldenAttemptCommitPayload(r)
	if err != nil || r.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != r.PayloadDigest {
		return goldenAttemptCommitError("terminal record digest changed")
	}
	return nil
}

func goldenAttemptCommitRetainsTerminalAttempt(r GoldenAttemptCommitRecord) bool {
	if _, err := domain.NewArenaGoldenGroup(r.Group); err != nil || len(r.Group.Attempts) == 0 {
		return false
	}
	return reflect.DeepEqual(r.Group.Attempts[len(r.Group.Attempts)-1], r.Attempt)
}

func goldenAttemptCommitPriorPositionsMatch(r GoldenAttemptCommitRecord) bool {
	return r.PriorPositions.Validate() == nil &&
		r.PriorPositions.Expectation().Equal(r.ExpectedPositions)
}

func validateGoldenAttemptPositionAuthority(record GoldenAttemptCommitRecord) error {
	if !goldenPositionLedgerMatchesGroup(record.PriorPositions, record.Group) ||
		!goldenPositionLedgerMatchesGroup(record.Positions, record.Group) {
		return goldenAttemptCommitError("position ledger is not bound to the terminal group")
	}
	members := make(map[uuid.UUID]struct{}, len(record.Group.Members))
	for _, member := range record.Group.Members {
		members[member.ParticipantID] = struct{}{}
	}
	if !goldenPositionParticipantsBelongToGroup(record.PriorPositions, members) ||
		!goldenPositionParticipantsBelongToGroup(record.Positions, members) {
		return goldenAttemptCommitError("position evidence contains a foreign group participant")
	}
	return nil
}

func goldenPositionLedgerMatchesGroup(
	ledger GoldenPositionLedger,
	group domain.ArenaGoldenGroupState,
) bool {
	return ledger.Scope.TournamentID == group.TournamentID && ledger.Scope.GroupID == group.ID &&
		ledger.Scope.GroupRevisionID == group.RevisionID && ledger.PositionFrom == group.PositionFrom &&
		ledger.PositionTo == group.PositionTo && len(ledger.Positions) <= len(group.Members)
}

func goldenPositionParticipantsBelongToGroup(
	ledger GoldenPositionLedger,
	members map[uuid.UUID]struct{},
) bool {
	for _, position := range ledger.Positions {
		if _, found := members[position.ParticipantID]; !found {
			return false
		}
	}
	for _, attempt := range ledger.Attempts {
		for _, item := range attempt.Order {
			if _, found := members[item.ParticipantID]; !found {
				return false
			}
		}
	}
	return true
}

func validGoldenAttemptCommitHeader(r GoldenAttemptCommitRecord) bool {
	return validGoldenAttemptCommitIdentity(r) && goldenAttemptCommitExecutionMatchesScope(r) &&
		goldenAttemptCommitAssignmentMatchesScope(r) && goldenAttemptCommitAssignmentMatchesExecution(r) &&
		goldenAttemptCommitHeadsMatchScope(r) && validArenaServerTime(r.FinishedAt)
}

func validGoldenAttemptCommitIdentity(r GoldenAttemptCommitRecord) bool {
	return r.ID != uuid.Nil && r.CommandID != uuid.Nil && r.ID != r.CommandID && r.Scope.IsValid() &&
		r.CommandDigest != [sha256.Size]byte{}
}

func goldenAttemptCommitExecutionMatchesScope(r GoldenAttemptCommitRecord) bool {
	return r.ActiveExecution.Scope == r.Scope.State && r.ActiveExecution.Source.Scope == r.Scope.State &&
		r.ActiveExecution.AttemptID == r.Scope.AttemptID && r.ActiveExecution.WaveID == r.Scope.WaveID &&
		r.ActiveExecution.AssignmentID == r.Scope.AssignmentID && r.ActiveExecution.Started
}

func goldenAttemptCommitAssignmentMatchesScope(r GoldenAttemptCommitRecord) bool {
	return r.Assignment.Validate() == nil && r.Assignment.ID == r.Scope.AssignmentID &&
		r.Assignment.Scope == r.Scope.State && r.Assignment.AttemptID == r.Scope.AttemptID &&
		r.Assignment.WaveID == r.Scope.WaveID && r.Assignment.SnapshotID == r.Scope.SnapshotID &&
		r.Assignment.TaskID == r.Scope.TaskID
}

func goldenAttemptCommitAssignmentMatchesExecution(r GoldenAttemptCommitRecord) bool {
	return r.Assignment.RevisionID == r.ActiveExecution.AssignmentRevisionID &&
		r.Assignment.Revision == r.ActiveExecution.AssignmentRevision &&
		r.Assignment.MembershipID == r.ActiveExecution.MembershipID &&
		r.Assignment.ExecutionPayloadDigest == r.ActiveExecution.AssignmentDigest &&
		r.Assignment.Plan == r.ActiveExecution.Source.Plan &&
		equalGoldenIDs(goldenPrivateAssignmentIDs(r.Assignment.Private), r.Attempt.ParticipantIDs)
}

func goldenAttemptCommitHeadsMatchScope(r GoldenAttemptCommitRecord) bool {
	return r.ExpectedSubmissions.Scope == r.Scope && r.ExpectedPositions.Scope == r.Scope.State &&
		r.SwissPoints.Validate() == nil
}

func validGoldenAttemptCommitTerminal(r GoldenAttemptCommitRecord) bool {
	validAttempt := r.Attempt.Validate() == nil && r.Attempt.State == domain.ArenaGoldenAttemptStateCompleted &&
		r.Attempt.ID == r.Scope.AttemptID
	validWave := r.Wave.Validate() == nil && r.Wave.State == domain.ArenaWaveStateCompleted &&
		r.Wave.ID == r.Scope.WaveID
	validGroup := r.Group.ID == r.Scope.State.GroupID && r.Group.RevisionID == r.Scope.State.GroupRevisionID
	return validAttempt && validWave && validGroup
}

func goldenAttemptCommitOrderingMatches(r GoldenAttemptCommitRecord) bool {
	return r.Ordering.AttemptID == r.Attempt.ID && r.Ordering.AttemptNo == r.Attempt.AttemptNo &&
		r.Ordering.SubmissionHead.Equal(r.ExpectedSubmissions) &&
		r.Positions.Expectation().ScopeIs(r.Scope.State)
}

func (e GoldenPositionLedgerExpectation) ScopeIs(scope GoldenStateScope) bool {
	return e.Scope == scope
}

// GoldenAttemptCommitRepository uses the same lock order as submission:
// execution, then submission ledger, then group position ledger. Commit proves
// the exact state/execution/submission/position heads and the read-only Swiss
// point sentinel, appends all positions, stores the terminal receipt, archives
// the active execution and clears its current pointer atomically. Receipts stay
// globally findable after archival. No partial position or point write is valid.
// The same transaction globally reserves the command, commit and successor
// position revision IDs across current and archived Golden records.
type GoldenAttemptCommitRepository interface {
	FindGoldenAttemptCommit(
		ctx context.Context,
		tournamentID uuid.UUID,
		commandID uuid.UUID,
	) (*GoldenAttemptCommitRecord, error)
	LoadGoldenAttemptCommitAuthority(
		ctx context.Context,
		scope GoldenSubmissionScope,
	) (GoldenAttemptCommitAuthority, error)
	CommitGoldenAttempt(
		ctx context.Context,
		record GoldenAttemptCommitRecord,
	) (*GoldenAttemptCommitRecord, bool, error)
}

type GoldenAttemptCommitUseCase struct {
	repository GoldenAttemptCommitRepository
	clock      Clock
	observer   observability.ArenaEventObserver
}

func NewGoldenAttemptCommitUseCase(
	repository GoldenAttemptCommitRepository,
	clock Clock,
	observers ...observability.ArenaEventObserver,
) *GoldenAttemptCommitUseCase {
	return &GoldenAttemptCommitUseCase{
		repository: repository,
		clock:      clock,
		observer:   observability.FirstArenaEventObserver(observers...),
	}
}

func (u *GoldenAttemptCommitUseCase) CommitAttempt(
	ctx context.Context,
	command GoldenAttemptCommitCommand,
) (*GoldenAttemptCommitRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	command.ExpectedExecution = cloneGoldenExecutionExpectationValue(command.ExpectedExecution)
	if err := validateGoldenAttemptCommitCommand(command); err != nil {
		return nil, false, err
	}
	finishedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(finishedAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenAttemptCommitAttempts {
		record, changed, retry, err := u.commitAttempt(ctx, command, finishedAt)
		if retry {
			u.emitGoldenAttemptEvent(
				ctx, command, nil, observability.ArenaOutcomeRetry, "conflict_retried", 0,
			)
			continue
		}
		outcome, reason := arenaCoreEventResult(changed, err, ErrGoldenAttemptCommitConflict)
		switch {
		case errors.Is(err, ErrGoldenAttemptCommitAuthorityConflict):
			outcome, reason = observability.ArenaOutcomeRejected, "authority_conflict"
		case errors.Is(err, ErrGoldenAttemptCommitCommandReuse):
			outcome, reason = observability.ArenaOutcomeRejected, "command_reused"
		case errors.Is(err, ErrGoldenAttemptNotTerminal), errors.Is(err, ErrInvalidGoldenAttemptCommit):
			outcome, reason = observability.ArenaOutcomeRejected, "attempt_not_terminal"
		}
		u.emitGoldenAttemptEvent(ctx, command, record, outcome, reason, 0)
		return record, changed, err
	}
	u.emitGoldenAttemptEvent(
		ctx, command, nil, observability.ArenaOutcomeFailure, "conflict_exhausted", 0,
	)
	return nil, false, ErrGoldenAttemptCommitConflict
}

func (u *GoldenAttemptCommitUseCase) emitGoldenAttemptEvent(
	ctx context.Context,
	command GoldenAttemptCommitCommand,
	record *GoldenAttemptCommitRecord,
	outcome string,
	reason string,
	revision int64,
) {
	duration := time.Duration(0)
	if record != nil {
		revision = record.ActiveExecution.Revision
		if record.Attempt.StartedAt != nil && !record.FinishedAt.Before(*record.Attempt.StartedAt) {
			duration = record.FinishedAt.Sub(*record.Attempt.StartedAt)
		}
	}
	emitArenaCoreEvent(ctx, u.observer, arenaCoreEventInput{
		event: "arena.command.golden_attempt", outcome: outcome,
		correlationID: command.CommandID.String(), commandID: command.CommandID.String(),
		tournamentID: command.Scope.State.TournamentID.String(), entityKind: "golden_attempt",
		entityID: command.Scope.AttemptID.String(), stage: "golden", transition: "commit_attempt",
		duration: duration, reasonCode: reason, revision: revision,
	})
	if record != nil && outcome == observability.ArenaOutcomeSuccess && reason == "committed" {
		emitArenaCoreEvent(ctx, u.observer, arenaCoreEventInput{
			event: "arena.game.duration", outcome: outcome,
			correlationID: command.CommandID.String(), tournamentID: command.Scope.State.TournamentID.String(),
			entityKind: "golden_attempt", entityID: command.Scope.AttemptID.String(),
			stage: "game", transition: "completed", duration: duration,
			reasonCode: "completed", revision: revision,
		})
	}
}

func (u *GoldenAttemptCommitUseCase) commitAttempt(
	ctx context.Context,
	command GoldenAttemptCommitCommand,
	finishedAt time.Time,
) (*GoldenAttemptCommitRecord, bool, bool, error) {
	replay, err := u.repository.FindGoldenAttemptCommit(ctx, command.Scope.State.TournamentID, command.CommandID)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenAttemptCommitUseCase - find replay: %w", err)
	}
	if replay != nil {
		record, replayErr := reconcileGoldenAttemptCommit(*replay, command)
		return record, false, false, replayErr
	}
	authority, err := u.repository.LoadGoldenAttemptCommitAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenAttemptCommitUseCase - load authority: %w", err)
	}
	if err := validateGoldenAttemptCommitAuthority(authority, command, finishedAt); err != nil {
		return nil, false, false, err
	}
	record, err := buildGoldenAttemptCommitRecord(authority, command, finishedAt)
	if err != nil {
		return nil, false, false, err
	}
	committed, changed, err := u.repository.CommitGoldenAttempt(ctx, record.Snapshot())
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenAttemptCommitUseCase - commit settlement: %w", err)
	}
	if committed == nil || committed.Validate() != nil ||
		!goldenAttemptCommitResultMatches(record, *committed, !changed) {
		return nil, false, false, domain.ErrInternal
	}
	result, reconcileErr := reconcileGoldenAttemptCommit(*committed, command)
	if reconcileErr != nil {
		return nil, false, false, domain.ErrInternal
	}
	return result, changed, false, nil
}

func goldenAttemptCommitResultMatches(
	expected, actual GoldenAttemptCommitRecord,
	allowServerTimeDrift bool,
) bool {
	expected = expected.Snapshot()
	actual = actual.Snapshot()
	if !allowServerTimeDrift {
		return reflect.DeepEqual(expected, actual)
	}
	expected.FinishedAt = time.Time{}
	actual.FinishedAt = time.Time{}
	expected.Attempt.FinishedAt = nil
	actual.Attempt.FinishedAt = nil
	if len(expected.Group.Attempts) > 0 {
		expected.Group.Attempts[len(expected.Group.Attempts)-1].FinishedAt = nil
	}
	if len(actual.Group.Attempts) > 0 {
		actual.Group.Attempts[len(actual.Group.Attempts)-1].FinishedAt = nil
	}
	expected.PayloadDigest = [sha256.Size]byte{}
	actual.PayloadDigest = [sha256.Size]byte{}
	return reflect.DeepEqual(expected, actual)
}

func validateGoldenAttemptCommitCommand(command GoldenAttemptCommitCommand) error {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil || command.CommitID == uuid.Nil ||
		command.NextPositionRevisionID == uuid.Nil || command.ExpectedSwissPoints.Validate() != nil ||
		(command.Reason != GoldenAttemptTerminalAllSolved && command.Reason != GoldenAttemptTerminalDeadline) {
		return goldenAttemptCommitError("invalid command identity or terminal reason")
	}
	if !uniqueNonZeroUUIDs([]uuid.UUID{command.CommandID, command.CommitID, command.NextPositionRevisionID}) {
		return goldenAttemptCommitError("command identity is aliased")
	}
	return nil
}

func validateGoldenAttemptCommitAuthority(
	authority GoldenAttemptCommitAuthority,
	command GoldenAttemptCommitCommand,
	finishedAt time.Time,
) error {
	if !validGoldenAttemptCommitAuthorityDocuments(authority, command) {
		return domain.ErrInternal
	}
	if !goldenAttemptCommitHeadsMatch(authority, command) {
		return ErrGoldenAttemptCommitAuthorityConflict
	}
	execution := authority.Execution
	if !goldenAttemptCommitExecutionActive(command.Scope, execution, finishedAt) {
		return ErrGoldenAttemptCommitAuthorityConflict
	}
	if !goldenAttemptCommitLedgersMatchExecution(authority, command.Scope, execution) {
		return ErrGoldenAttemptCommitAuthorityConflict
	}
	if !goldenAttemptTerminalCondition(authority, command, finishedAt) {
		return ErrGoldenAttemptNotTerminal
	}
	if len(authority.Positions.Positions)+len(authority.Submissions.Submissions) >
		authority.Positions.PositionTo-authority.Positions.PositionFrom+1 {
		return goldenAttemptCommitError("position interval would overflow")
	}
	if err := validateGoldenAttemptCommitSubmissions(authority, command.Scope, execution, finishedAt); err != nil {
		return err
	}
	if authority.Positions.Revision == math.MaxInt64 ||
		goldenPositionRevisionIDUsed(authority.Positions, command.NextPositionRevisionID) {
		return goldenAttemptCommitError("position successor identity is reused")
	}
	if !goldenAttemptCommitNewIDsAreFresh(authority, command) {
		return goldenAttemptCommitError("terminal identity aliases retained authority")
	}
	return nil
}

func goldenAttemptCommitNewIDsAreFresh(
	authority GoldenAttemptCommitAuthority,
	command GoldenAttemptCommitCommand,
) bool {
	reserved := goldenSubmissionRetainedIdentitySet(authority.Execution)
	reserveGoldenSubmissionLedgerIdentities(reserved, authority.Submissions)
	reserveGoldenPositionLedgerIdentities(reserved, authority.Positions)
	reserved[authority.SwissPoints.RevisionID] = struct{}{}
	identities := []uuid.UUID{command.CommandID, command.CommitID, command.NextPositionRevisionID}
	seen := make(map[uuid.UUID]struct{}, len(identities))
	for _, identity := range identities {
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

func reserveGoldenSubmissionLedgerIdentities(
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

func reserveGoldenPositionLedgerIdentities(
	reserved map[uuid.UUID]struct{},
	ledger GoldenPositionLedger,
) {
	for _, revisionID := range ledger.RevisionIDs {
		reserved[revisionID] = struct{}{}
	}
	for _, position := range ledger.Positions {
		reserved[position.ParticipantID] = struct{}{}
		reserved[position.AttemptID] = struct{}{}
		reserved[position.CommitID] = struct{}{}
	}
	for _, attempt := range ledger.Attempts {
		reserved[attempt.AttemptID] = struct{}{}
		reserved[attempt.SubmissionHead.RevisionID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.State.TournamentID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.State.GroupID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.AttemptID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.WaveID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.AssignmentID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.SnapshotID] = struct{}{}
		reserved[attempt.SubmissionHead.Scope.TaskID] = struct{}{}
		for _, item := range attempt.Order {
			reserved[item.ParticipantID] = struct{}{}
		}
	}
}

func validGoldenAttemptCommitAuthorityDocuments(
	authority GoldenAttemptCommitAuthority,
	command GoldenAttemptCommitCommand,
) bool {
	return authority.Scope == command.Scope && authority.Execution.Validate() == nil &&
		authority.Submissions.Validate() == nil && authority.Positions.Validate() == nil &&
		authority.SwissPoints.Validate() == nil
}

func goldenAttemptCommitHeadsMatch(
	authority GoldenAttemptCommitAuthority,
	command GoldenAttemptCommitCommand,
) bool {
	return authority.Execution.Expectation().Equal(command.ExpectedExecution) &&
		authority.Submissions.Expectation().Equal(command.ExpectedSubmissions) &&
		authority.Positions.Expectation().Equal(command.ExpectedPositions) &&
		authority.SwissPoints == command.ExpectedSwissPoints
}

func goldenAttemptCommitExecutionActive(
	scope GoldenSubmissionScope,
	execution GoldenWaveExecution,
	finishedAt time.Time,
) bool {
	return goldenSubmissionScopeMatchesExecution(scope, execution) && execution.Start != nil &&
		execution.Wave.State == domain.ArenaWaveStateActive &&
		execution.Attempt.State == domain.ArenaGoldenAttemptStateActive &&
		!finishedAt.Before(execution.Start.StartedAt)
}

func goldenAttemptCommitLedgersMatchExecution(
	authority GoldenAttemptCommitAuthority,
	scope GoldenSubmissionScope,
	execution GoldenWaveExecution,
) bool {
	return authority.Submissions.Scope == scope && authority.Positions.Scope == scope.State &&
		authority.Positions.PositionFrom == execution.Group.PositionFrom &&
		authority.Positions.PositionTo == execution.Group.PositionTo
}

func goldenAttemptTerminalCondition(
	authority GoldenAttemptCommitAuthority,
	command GoldenAttemptCommitCommand,
	finishedAt time.Time,
) bool {
	if command.Reason == GoldenAttemptTerminalDeadline {
		return !finishedAt.Before(authority.Execution.Start.Deadline)
	}
	return len(authority.Submissions.Submissions) == len(authority.Execution.Attempt.ParticipantIDs)
}

func validateGoldenAttemptCommitSubmissions(
	authority GoldenAttemptCommitAuthority,
	scope GoldenSubmissionScope,
	execution GoldenWaveExecution,
	finishedAt time.Time,
) error {
	positioned := make(map[uuid.UUID]struct{}, len(authority.Positions.Positions))
	for _, position := range authority.Positions.Positions {
		positioned[position.ParticipantID] = struct{}{}
	}
	for _, submission := range authority.Submissions.Submissions {
		if submission.Scope != scope || !goldenIDsContain(execution.Attempt.ParticipantIDs, submission.ParticipantID) ||
			submission.CommittedAt.After(finishedAt) || !submission.CommittedAt.Before(execution.Start.Deadline) {
			return ErrGoldenAttemptCommitAuthorityConflict
		}
		if _, duplicate := positioned[submission.ParticipantID]; duplicate {
			return goldenAttemptCommitError("submission participant already has a position")
		}
	}
	return nil
}

func buildGoldenAttemptCommitRecord(
	authority GoldenAttemptCommitAuthority,
	command GoldenAttemptCommitCommand,
	finishedAt time.Time,
) (GoldenAttemptCommitRecord, error) {
	order := authority.Submissions.ProvisionalOrder()
	ordering := GoldenAttemptOrderingEvidence{
		AttemptID: authority.Execution.Attempt.ID, AttemptNo: authority.Execution.Attempt.AttemptNo,
		SubmissionHead: authority.Submissions.Expectation(), Order: make([]GoldenPositionOrderEntry, len(order)),
	}
	for index, submission := range order {
		ordering.Order[index] = GoldenPositionOrderEntry{
			SubmissionID: submission.ID, ParticipantID: submission.ParticipantID,
			CommittedAt: submission.CommittedAt, EvidenceDigest: submission.EvidenceDigest,
		}
	}
	payload, err := goldenAttemptOrderingPayload(ordering)
	if err != nil {
		return GoldenAttemptCommitRecord{}, goldenAttemptCommitError("encode ordering evidence")
	}
	ordering.PayloadDigest = sha256.Sum256(payload)
	positions := authority.Positions.Snapshot()
	positions.PreviousRevisionID = goldenUUID(positions.RevisionID)
	positions.RevisionID = command.NextPositionRevisionID
	positions.Revision++
	positions.RevisionIDs = append(positions.RevisionIDs, command.NextPositionRevisionID)
	positions.Attempts = append(positions.Attempts, ordering.Snapshot())
	for _, submission := range order {
		positions.Positions = append(positions.Positions, GoldenCommittedPosition{
			Position:      positions.PositionFrom + len(positions.Positions),
			ParticipantID: submission.ParticipantID, AttemptID: authority.Execution.Attempt.ID,
			AttemptNo: authority.Execution.Attempt.AttemptNo, SubmissionID: submission.ID,
			EvidenceDigest: submission.EvidenceDigest, CommitID: command.CommitID,
		})
	}
	positions, err = buildGoldenPositionLedger(positions)
	if err != nil {
		return GoldenAttemptCommitRecord{}, err
	}
	attempt := cloneGoldenAttempt(authority.Execution.Attempt)
	attempt.State = domain.ArenaGoldenAttemptStateCompleted
	attempt.FinishedAt = cloneGoldenTime(&finishedAt)
	group := cloneGoldenStateGroup(authority.Execution.Group)
	group.Attempts[len(group.Attempts)-1] = cloneGoldenAttempt(attempt)
	if _, err := domain.NewArenaGoldenGroup(group); err != nil {
		return GoldenAttemptCommitRecord{}, goldenAttemptCommitError("terminal group is invalid")
	}
	wave := cloneArenaWaveExecution(authority.Execution.Wave)
	wave.State = domain.ArenaWaveStateCompleted
	assignment, err := buildGoldenAttemptAssignmentEvidence(authority.Execution.Assignment)
	if err != nil {
		return GoldenAttemptCommitRecord{}, err
	}
	record := GoldenAttemptCommitRecord{
		ID: command.CommitID, CommandID: command.CommandID,
		CommandDigest: goldenAttemptCommitCommandDigest(command), Scope: command.Scope,
		ActiveExecution:     cloneGoldenExecutionExpectationValue(command.ExpectedExecution),
		Assignment:          assignment,
		ExpectedSubmissions: command.ExpectedSubmissions, ExpectedPositions: command.ExpectedPositions,
		SwissPoints: command.ExpectedSwissPoints, Reason: command.Reason, FinishedAt: finishedAt,
		Attempt: attempt, Group: group, Wave: wave, Ordering: ordering,
		PriorPositions: authority.Positions.Snapshot(), Positions: positions,
	}
	payload, err = goldenAttemptCommitPayload(record)
	if err != nil {
		return GoldenAttemptCommitRecord{}, goldenAttemptCommitError("encode terminal record")
	}
	record.PayloadDigest = sha256.Sum256(payload)
	if err := record.Validate(); err != nil {
		return GoldenAttemptCommitRecord{}, err
	}
	return record.Snapshot(), nil
}

func buildGoldenAttemptAssignmentEvidence(
	assignment GoldenAttemptAssignment,
) (GoldenAttemptAssignmentEvidence, error) {
	evidence := GoldenAttemptAssignmentEvidence{
		ID: assignment.ID, RevisionID: assignment.RevisionID, Revision: assignment.Revision,
		Scope: assignment.Scope, AttemptID: assignment.AttemptID, WaveID: assignment.WaveID,
		MembershipID: assignment.MembershipID, Plan: assignment.Plan, EdgeID: assignment.EdgeID,
		ReservationID: assignment.ReservationID, SnapshotID: assignment.Snapshot.SnapshotID,
		TaskID: assignment.Snapshot.TaskID, ContentDigest: assignment.ContentDigest,
		Private:                append([]GoldenPrivateAssignment(nil), assignment.Private...),
		ExecutionPayloadDigest: assignment.PayloadDigest,
	}
	payload, err := goldenAttemptAssignmentEvidencePayload(evidence)
	if err != nil {
		return GoldenAttemptAssignmentEvidence{}, goldenAttemptCommitError("encode assignment evidence")
	}
	evidence.PayloadDigest = sha256.Sum256(payload)
	if err := evidence.Validate(); err != nil {
		return GoldenAttemptAssignmentEvidence{}, err
	}
	return evidence.Snapshot(), nil
}

func validateGoldenAttemptPositionLinks(record GoldenAttemptCommitRecord) error {
	if !goldenPositionLedgerExtends(record.PriorPositions, record.Positions) {
		return goldenAttemptCommitError("prior position ledger is not an immutable prefix")
	}
	start := record.ExpectedPositions.PositionCount
	if len(record.Positions.Positions) != start+len(record.Ordering.Order) ||
		len(record.Positions.Attempts) == 0 {
		return goldenAttemptCommitError("position append is not atomic")
	}
	for index, item := range record.Ordering.Order {
		position := record.Positions.Positions[start+index]
		if position.ParticipantID != item.ParticipantID || position.SubmissionID != item.SubmissionID ||
			position.EvidenceDigest != item.EvidenceDigest || position.AttemptID != record.Attempt.ID ||
			position.AttemptNo != record.Attempt.AttemptNo || position.CommitID != record.ID {
			return goldenAttemptCommitError("position changed provisional ordering evidence")
		}
	}
	return nil
}

func goldenPositionLedgerExtends(prior GoldenPositionLedger, final GoldenPositionLedger) bool {
	validHead := final.Scope == prior.Scope && final.PositionFrom == prior.PositionFrom &&
		final.PositionTo == prior.PositionTo && final.Revision == prior.Revision+1 &&
		final.PreviousRevisionID != nil && *final.PreviousRevisionID == prior.RevisionID &&
		len(final.RevisionIDs) == len(prior.RevisionIDs)+1 &&
		goldenUUIDPrefixEqual(final.RevisionIDs, prior.RevisionIDs)
	if !validHead || len(final.Positions) < len(prior.Positions) || len(final.Attempts) != len(prior.Attempts)+1 {
		return false
	}
	return goldenPositionPrefixEqual(final.Positions, prior.Positions) &&
		goldenAttemptEvidencePrefixEqual(final.Attempts, prior.Attempts)
}

func goldenUUIDPrefixEqual(final []uuid.UUID, prior []uuid.UUID) bool {
	if len(final) < len(prior) {
		return false
	}
	for index := range prior {
		if final[index] != prior[index] {
			return false
		}
	}
	return true
}

func goldenPositionPrefixEqual(final []GoldenCommittedPosition, prior []GoldenCommittedPosition) bool {
	if len(final) < len(prior) {
		return false
	}
	for index := range prior {
		if final[index] != prior[index] {
			return false
		}
	}
	return true
}

func goldenAttemptEvidencePrefixEqual(final []GoldenAttemptOrderingEvidence, prior []GoldenAttemptOrderingEvidence) bool {
	if len(final) < len(prior) {
		return false
	}
	for index := range prior {
		if !reflect.DeepEqual(final[index], prior[index]) {
			return false
		}
	}
	return true
}

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
	return goldenEncode(struct {
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
	return goldenEncode(struct {
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
	return goldenEncode(struct {
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
		Attempt             domain.ArenaGoldenAttempt
		Group               domain.ArenaGoldenGroupState
		Wave                domain.ArenaWave
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

func goldenAttemptAssignmentEvidencePayload(evidence GoldenAttemptAssignmentEvidence) ([]byte, error) {
	return goldenEncode(struct {
		ID                     uuid.UUID
		RevisionID             uuid.UUID
		Revision               int64
		Scope                  GoldenStateScope
		AttemptID              uuid.UUID
		WaveID                 uuid.UUID
		MembershipID           uuid.UUID
		Plan                   GoldenPlanStateBinding
		EdgeID                 uuid.UUID
		ReservationID          uuid.UUID
		SnapshotID             uuid.UUID
		TaskID                 uuid.UUID
		ContentDigest          [sha256.Size]byte
		Private                []GoldenPrivateAssignment
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

func goldenAttemptCommitCommandDigest(command GoldenAttemptCommitCommand) [sha256.Size]byte {
	payload, _ := goldenEncode(command)
	return sha256.Sum256(payload)
}

func goldenPositionRevisionIDUsed(ledger GoldenPositionLedger, revisionID uuid.UUID) bool {
	return goldenIDsContain(ledger.RevisionIDs, revisionID)
}

func goldenAttemptCommitError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenAttemptCommit, message)
}
