package golden

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenattempt "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/attempt"
	goldenexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/execution"
	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
	goldenwave "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/wave"
)

type ContinuationClock interface {
	Now() time.Time
}

const goldenContinuationAttempts = 3

var (
	ErrInvalidGoldenContinuation           = errors.New("invalid Golden continuation")
	ErrGoldenContinuationAuthorityConflict = errors.New("golden continuation authority conflict")
	ErrGoldenContinuationConflict          = errors.New("golden continuation conflict")
	ErrGoldenContinuationCommandReuse      = errors.New("golden continuation command identifier was reused")
	ErrGoldenContinuationAlreadyCommitted  = errors.New("golden continuation is already committed")
	ErrGoldenContinuationFallbackRequired  = errors.New("golden continuation requires terminal fallback")
	ErrGoldenContinuationReservesExhausted = errors.New("golden continuation reserves are exhausted")
)

type GoldenStateScope = goldenstate.GoldenStateScope
type GoldenState = goldenstate.GoldenState
type GoldenStateExpectation = goldenstate.GoldenStateExpectation
type GoldenPlanStateBinding = goldenstate.GoldenPlanStateBinding
type ExactPlan = goldenplan.ExactPlan
type Edge = goldenplan.Edge
type GoldenAttemptCommitRecord = goldenattempt.GoldenAttemptCommitRecord
type GoldenPositionLedger = goldenattempt.GoldenPositionLedger
type GoldenPositionLedgerExpectation = goldenattempt.GoldenPositionLedgerExpectation
type GoldenSwissPointLedgerSentinel = goldenattempt.GoldenSwissPointLedgerSentinel
type GoldenAttemptOrderingEvidence = goldenattempt.GoldenAttemptOrderingEvidence
type GoldenReserveAttemptAssignment = goldenattempt.GoldenReserveAttemptAssignment
type GoldenAttemptAssignmentEvidence = goldenexecution.GoldenAttemptAssignmentEvidence
type GoldenPrivateAssignment = goldenexecution.GoldenPrivateAssignment
type GoldenPrivateAssignmentCommand = goldenwave.GoldenPrivateAssignmentCommand

type GoldenContinuationCommand struct {
	Scope                    GoldenStateScope
	CommandID                uuid.UUID
	ContinuationID           uuid.UUID
	ExpectedTerminalID       uuid.UUID
	ExpectedTerminalDigest   [sha256.Size]byte
	ExpectedParentID         uuid.UUID
	ExpectedParentDigest     [sha256.Size]byte
	ExpectedState            GoldenStateExpectation
	ExpectedPlan             GoldenPlanStateBinding
	ExpectedPositions        GoldenPositionLedgerExpectation
	ExpectedSwissPoints      GoldenSwissPointLedgerSentinel
	NextAttemptID            uuid.UUID
	NextAssignmentID         uuid.UUID
	NextAssignmentRevisionID uuid.UUID
	PrivateAssignments       []GoldenPrivateAssignmentCommand
}

type GoldenContinuationAuthority struct {
	Scope       GoldenStateScope
	State       GoldenState
	Plan        ExactPlan
	Terminal    GoldenAttemptCommitRecord
	Positions   GoldenPositionLedger
	SwissPoints GoldenSwissPointLedgerSentinel
	Parent      *GoldenContinuationRecord
	Current     *GoldenContinuationRecord
}

type GoldenContinuationRecord struct {
	ID                       uuid.UUID
	CommandID                uuid.UUID
	CommandDigest            [sha256.Size]byte
	Scope                    GoldenStateScope
	SourceTerminalID         uuid.UUID
	SourceTerminalDigest     [sha256.Size]byte
	SourceParentID           uuid.UUID
	SourceParentDigest       [sha256.Size]byte
	ExpectedState            GoldenStateExpectation
	ExpectedPlan             GoldenPlanStateBinding
	ExpectedPositions        GoldenPositionLedgerExpectation
	SwissPoints              GoldenSwissPointLedgerSentinel
	NewIdentityIDs           []uuid.UUID
	ResolvedParticipantIDs   []uuid.UUID
	UnresolvedParticipantIDs []uuid.UUID
	RemainingPositions       []int
	Group                    domain.GoldenGroupState
	Attempt                  domain.GoldenAttempt
	Assignment               GoldenReserveAttemptAssignment
	CreatedAt                time.Time
	Positions                GoldenPositionLedger
	PayloadDigest            [sha256.Size]byte
}

func (r GoldenContinuationRecord) Snapshot() GoldenContinuationRecord {
	clone := r
	clone.ExpectedState = CloneExpectation(r.ExpectedState)
	clone.NewIdentityIDs = append([]uuid.UUID(nil), r.NewIdentityIDs...)
	clone.ResolvedParticipantIDs = append([]uuid.UUID(nil), r.ResolvedParticipantIDs...)
	clone.UnresolvedParticipantIDs = append([]uuid.UUID(nil), r.UnresolvedParticipantIDs...)
	clone.RemainingPositions = append([]int(nil), r.RemainingPositions...)
	clone.Group = CloneGroup(r.Group)
	clone.Attempt = CloneAttempt(r.Attempt)
	clone.Assignment = r.Assignment.Snapshot()
	clone.Positions = r.Positions.Snapshot()
	return clone
}

func (r GoldenContinuationRecord) Validate() error {
	if !validGoldenContinuationRecordIdentity(r) || !validGoldenContinuationRecordBindings(r) {
		return goldenContinuationError("invalid continuation identity or binding")
	}
	if !validGoldenContinuationSuccessor(r) {
		return goldenContinuationError("successor group does not retain the next attempt")
	}
	if !validGoldenContinuationMembership(r) {
		return goldenContinuationError("successor membership is not exact")
	}
	if err := validateGoldenContinuationPartition(r); err != nil {
		return err
	}
	payload, err := goldenContinuationPayload(r)
	if err != nil || r.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != r.PayloadDigest {
		return goldenContinuationError("continuation payload digest changed")
	}
	return nil
}

func validGoldenContinuationRecordIdentity(r GoldenContinuationRecord) bool {
	return r.ID != uuid.Nil && r.CommandID != uuid.Nil && r.ID != r.CommandID &&
		ValidStateScope(r.Scope) && r.CommandDigest != [sha256.Size]byte{} &&
		r.SourceTerminalID != uuid.Nil && r.SourceTerminalDigest != [sha256.Size]byte{} &&
		validGoldenContinuationParentReference(r.SourceParentID, r.SourceParentDigest) &&
		r.SourceTerminalID != r.SourceParentID && !ContainsID(r.NewIdentityIDs, r.SourceTerminalID) &&
		(r.SourceParentID == uuid.Nil || !ContainsID(r.NewIdentityIDs, r.SourceParentID)) &&
		domain.IsValidServerTime(r.CreatedAt) && IDsCanonical(r.NewIdentityIDs) &&
		EqualIDs(r.NewIdentityIDs, goldenContinuationRecordIdentityIDs(r))
}

func validGoldenContinuationParentReference(id uuid.UUID, digest [sha256.Size]byte) bool {
	return (id == uuid.Nil) == (digest == [sha256.Size]byte{})
}

func validGoldenContinuationRecordBindings(r GoldenContinuationRecord) bool {
	return r.ExpectedState.Scope == r.Scope && r.ExpectedPlan.GroupID == r.Scope.GroupID &&
		r.ExpectedPlan.GroupRevisionID == r.Scope.GroupRevisionID && r.ExpectedPositions.ScopeIs(r.Scope) &&
		r.SwissPoints.Validate() == nil && r.Attempt.Validate() == nil &&
		r.Attempt.State == domain.GoldenAttemptStatePlanned && r.Attempt.GroupID == r.Scope.GroupID &&
		r.Attempt.GroupRevisionID == r.Scope.GroupRevisionID && r.Assignment.Validate() == nil &&
		r.Assignment.Scope == r.Scope && r.Assignment.AttemptID == r.Attempt.ID &&
		r.Positions.Validate() == nil && r.Positions.Expectation().Equal(r.ExpectedPositions) &&
		goldenContinuationPositionsMatchGroup(r.Positions, r.Group)
}

func goldenContinuationPositionsMatchGroup(positions GoldenPositionLedger, group domain.GoldenGroupState) bool {
	if !PositionLedgerMatchesGroup(positions, group) {
		return false
	}
	members := make(map[uuid.UUID]struct{}, len(group.Members))
	for _, member := range group.Members {
		members[member.ParticipantID] = struct{}{}
	}
	return PositionParticipantsBelongToGroup(positions, members)
}

func validGoldenContinuationSuccessor(r GoldenContinuationRecord) bool {
	if _, err := domain.NewGoldenGroup(r.Group); err != nil || len(r.Group.Attempts) < 2 {
		return false
	}
	return reflect.DeepEqual(r.Group.Attempts[len(r.Group.Attempts)-1], r.Attempt)
}

func validGoldenContinuationMembership(r GoldenContinuationRecord) bool {
	return len(r.UnresolvedParticipantIDs) >= 2 &&
		EqualIDs(r.UnresolvedParticipantIDs, r.Attempt.ParticipantIDs) &&
		EqualIDs(r.UnresolvedParticipantIDs, PrivateAssignmentParticipantIDs(r.Assignment.Private)) &&
		IDsCanonical(r.ResolvedParticipantIDs) && IDsCanonical(r.UnresolvedParticipantIDs)
}

// ContinuationRepository atomically consumes a terminal receipt and stores its successor.
type ContinuationRepository interface {
	FindGoldenContinuation(ctx context.Context, tournamentID, commandID uuid.UUID) (*GoldenContinuationRecord, error)
	LoadGoldenContinuationAuthority(ctx context.Context, scope GoldenStateScope) (GoldenContinuationAuthority, error)
	CommitGoldenContinuation(ctx context.Context, record GoldenContinuationRecord) (*GoldenContinuationRecord, bool, error)
}

func Encode(value any) ([]byte, error) {
	return goldenattempt.Encode(value)
}

func ValidStateScope(scope GoldenStateScope) bool {
	return goldenstate.ValidStateScope(scope)
}

func ContainsID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func ValidIdentitySet(values []uuid.UUID) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func SortIDs(values []uuid.UUID) {
	sort.Slice(values, func(i, j int) bool { return bytes.Compare(values[i][:], values[j][:]) < 0 })
}

func IDsCanonical(values []uuid.UUID) bool {
	for index, value := range values {
		if value == uuid.Nil || (index > 0 && bytes.Compare(values[index-1][:], value[:]) >= 0) {
			return false
		}
	}
	return true
}

func EqualIDs(first, second []uuid.UUID) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func IDSet(values []uuid.UUID) map[uuid.UUID]bool {
	set := make(map[uuid.UUID]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func UUIDPointer(value uuid.UUID) *uuid.UUID {
	return &value
}

func CloneGroup(input domain.GoldenGroupState) domain.GoldenGroupState {
	return goldenstate.CloneGroup(input)
}

func CloneAttempt(input domain.GoldenAttempt) domain.GoldenAttempt {
	return goldenstate.CloneAttempt(input)
}

func CloneExpectation(input GoldenStateExpectation) GoldenStateExpectation {
	return goldenstate.CloneExpectation(input)
}

func PrivateAssignmentParticipantIDs(assignments []GoldenPrivateAssignment) []uuid.UUID {
	result := make([]uuid.UUID, len(assignments))
	for index, assignment := range assignments {
		result[index] = assignment.ParticipantID
	}
	SortIDs(result)
	return result
}

func PositionLedgerMatchesGroup(ledger GoldenPositionLedger, group domain.GoldenGroupState) bool {
	return goldenattempt.PositionLedgerMatchesGroup(ledger, group)
}

func PositionParticipantsBelongToGroup(ledger GoldenPositionLedger, members map[uuid.UUID]struct{}) bool {
	return goldenattempt.PositionParticipantsBelongToGroup(ledger, members)
}

func ReservePositionLedgerIdentities(reserved map[uuid.UUID]struct{}, ledger GoldenPositionLedger) {
	goldenattempt.ReservePositionLedgerIdentities(reserved, ledger)
}

func ValidateFreshIdentityIDs(state GoldenState, identities ...uuid.UUID) error {
	return goldenwave.ValidateFreshIdentityIDs(state, identities...)
}

func continuationCloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func FindMember(members []domain.GoldenMember, participantID uuid.UUID) (domain.GoldenMember, bool) {
	for _, member := range members {
		if member.ParticipantID == participantID {
			return member, true
		}
	}
	return domain.GoldenMember{}, false
}

func ReserveContinuationIdentities(reserved map[uuid.UUID]struct{}, record GoldenContinuationRecord) {
	values := []uuid.UUID{
		record.ID, record.CommandID, record.SourceTerminalID, record.SourceParentID,
		record.Assignment.ID, record.Assignment.RevisionID, record.Assignment.AttemptID,
		record.Assignment.EdgeID, record.Assignment.ReservationID, record.Assignment.SnapshotID,
		record.Assignment.TaskID, record.Attempt.ID,
	}
	values = append(values, record.NewIdentityIDs...)
	values = append(values, record.ResolvedParticipantIDs...)
	values = append(values, record.UnresolvedParticipantIDs...)
	for _, private := range record.Assignment.Private {
		values = append(values, private.ID, private.ParticipantID, private.SnapshotID)
	}
	for _, attempt := range record.Group.Attempts {
		values = append(values, attempt.ID)
		values = append(values, attempt.ParticipantIDs...)
		if attempt.PreviousAttemptID != nil {
			values = append(values, *attempt.PreviousAttemptID)
		}
	}
	ReservePositionLedgerIdentities(reserved, record.Positions)
	for _, value := range values {
		if value != uuid.Nil {
			reserved[value] = struct{}{}
		}
	}
}
