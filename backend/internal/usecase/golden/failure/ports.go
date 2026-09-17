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
	goldensubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"
	goldenwave "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/wave"
)

type GoldenState = goldenstate.GoldenState
type GoldenStateScope = goldenstate.GoldenStateScope
type GoldenStateExpectation = goldenstate.GoldenStateExpectation
type GoldenReadyWindow = goldenstate.GoldenReadyWindow
type GoldenReadyWindowExpectation = goldenstate.GoldenReadyWindowExpectation
type GoldenPlanStateBinding = goldenstate.GoldenPlanStateBinding
type GoldenSubmissionScope = goldensubmission.GoldenSubmissionScope
type GoldenSubmissionLedger = goldensubmission.GoldenSubmissionLedger
type GoldenSubmissionLedgerExpectation = goldensubmission.GoldenSubmissionLedgerExpectation
type GoldenSubmissionRecord = goldensubmission.GoldenSubmissionRecord
type GoldenWaveExecution = goldenexecution.GoldenWaveExecution
type GoldenWaveExecutionExpectation = goldenexecution.GoldenWaveExecutionExpectation
type GoldenWaveMembershipBinding = goldenexecution.GoldenWaveMembershipBinding
type GoldenAttemptAssignment = goldenexecution.GoldenAttemptAssignment
type GoldenAttemptAssignmentEvidence = goldenexecution.GoldenAttemptAssignmentEvidence
type GoldenPrivateAssignment = goldenexecution.GoldenPrivateAssignment
type GoldenPrivateAssignmentCommand = goldenwave.GoldenPrivateAssignmentCommand
type GoldenAttemptOrderingEvidence = goldenattempt.GoldenAttemptOrderingEvidence
type GoldenPositionLedger = goldenattempt.GoldenPositionLedger
type GoldenPositionLedgerExpectation = goldenattempt.GoldenPositionLedgerExpectation
type GoldenCommittedPosition = goldenattempt.GoldenCommittedPosition
type GoldenSwissPointLedgerSentinel = goldenattempt.GoldenSwissPointLedgerSentinel
type GoldenReserveAttemptAssignment = goldenattempt.GoldenReserveAttemptAssignment
type Group = goldenplan.Group
type Edge = goldenplan.Edge
type ExactPlan = goldenplan.ExactPlan

const GoldenReadyWindowOpen = goldenstate.GoldenReadyWindowOpen

type FailureClock interface {
	Now() time.Time
}

const (
	goldenFailureCommitAttempts = 3
	goldenFailureAttemptLimit   = domain.AssignmentReserveCount + 1
	goldenFailureReceiptLimit   = domain.TournamentMaxParticipants * 2
)

var (
	ErrInvalidGoldenFailure           = errors.New("invalid Golden failure")
	ErrGoldenFailureAuthorityConflict = errors.New("golden failure authority conflict")
	ErrGoldenFailureConflict          = errors.New("golden failure commit conflict")
	ErrGoldenFailureCommandReuse      = errors.New("golden failure command identifier was reused")
	ErrGoldenFailureRouteConflict     = errors.New("golden failure route conflicts with locked reserve classification")
)

type GoldenFailureRoute string

const (
	GoldenFailureRouteReplay    GoldenFailureRoute = "replay"
	GoldenFailureRouteExhausted GoldenFailureRoute = "exhausted"
)

type GoldenFailureClassificationKind string

const (
	GoldenFailureClassificationReplay    GoldenFailureClassificationKind = "next_reserve"
	GoldenFailureClassificationExhausted GoldenFailureClassificationKind = "reserve_exhausted"
)

type GoldenFailureEdge struct {
	ID            uuid.UUID
	Position      int
	ReservationID uuid.UUID
	SnapshotID    uuid.UUID
	TaskID        uuid.UUID
	ContentDigest [sha256.Size]byte
}

type GoldenFailureClassification struct {
	Kind             GoldenFailureClassificationKind
	Plan             GoldenPlanStateBinding
	FailedEdge       GoldenFailureEdge
	NextEdge         *GoldenFailureEdge
	FailedAttemptNo  int
	ParticipantIDs   []uuid.UUID
	MembershipDigest [sha256.Size]byte
	Exhausted        bool
	PayloadDigest    [sha256.Size]byte
}

func (c GoldenFailureClassification) Snapshot() GoldenFailureClassification {
	clone := c
	clone.ParticipantIDs = append([]uuid.UUID(nil), c.ParticipantIDs...)
	if c.NextEdge != nil {
		next := *c.NextEdge
		clone.NextEdge = &next
	}
	return clone
}

func (c GoldenFailureClassification) Validate() error {
	if !validGoldenFailureClassificationRoute(c) || !validGoldenFailureClassificationParticipants(c) {
		return goldenFailureError("invalid reserve classification")
	}
	if c.NextEdge != nil && (!validGoldenFailureEdge(*c.NextEdge) || c.NextEdge.Position != c.FailedEdge.Position+1) {
		return goldenFailureError("next reserve edge is not sequential")
	}
	payload, err := goldenFailureClassificationPayload(c)
	if err != nil || c.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != c.PayloadDigest {
		return goldenFailureError("reserve classification digest changed")
	}
	return nil
}

func validGoldenFailureClassificationRoute(classification GoldenFailureClassification) bool {
	if classification.Kind != GoldenFailureClassificationReplay &&
		classification.Kind != GoldenFailureClassificationExhausted {
		return false
	}
	if classification.FailedAttemptNo < 1 || !validGoldenFailureEdge(classification.FailedEdge) {
		return false
	}
	if classification.Exhausted != (classification.NextEdge == nil) {
		return false
	}
	return classification.Exhausted == (classification.Kind == GoldenFailureClassificationExhausted)
}

func validGoldenFailureClassificationParticipants(classification GoldenFailureClassification) bool {
	return len(classification.ParticipantIDs) >= 2 &&
		len(classification.ParticipantIDs) <= domain.TournamentMaxParticipants &&
		IDsCanonical(classification.ParticipantIDs) &&
		classification.MembershipDigest == DigestIDs(classification.ParticipantIDs)
}

type GoldenFailureActiveExecution struct {
	Scope          GoldenSubmissionScope
	Expectation    GoldenWaveExecutionExpectation
	Group          domain.GoldenGroupState
	Attempt        domain.GoldenAttempt
	Wave           domain.Wave
	Assignment     GoldenAttemptAssignmentEvidence
	ParticipantIDs []uuid.UUID
	StartedAt      time.Time
	Deadline       time.Time
}

func NewGoldenFailureActiveExecution(execution GoldenWaveExecution) (GoldenFailureActiveExecution, error) {
	if execution.Validate() != nil || execution.Start == nil {
		return GoldenFailureActiveExecution{}, goldenFailureError("active execution is malformed")
	}
	assignment, err := BuildAttemptAssignmentEvidence(execution.Assignment)
	if err != nil {
		return GoldenFailureActiveExecution{}, err
	}
	active := GoldenFailureActiveExecution{
		Scope: GoldenSubmissionScope{
			State: execution.Scope, AttemptID: execution.Attempt.ID, WaveID: execution.Wave.ID,
			AssignmentID: execution.Assignment.ID, SnapshotID: execution.Assignment.Snapshot.SnapshotID,
			TaskID: execution.Assignment.Snapshot.TaskID,
		},
		Expectation: execution.Expectation(), Group: CloneGroup(execution.Group),
		Attempt: CloneAttempt(execution.Attempt), Wave: CloneExecution(execution.Wave),
		Assignment: assignment, ParticipantIDs: append([]uuid.UUID(nil), execution.Membership.ParticipantIDs...),
		StartedAt: execution.Start.StartedAt, Deadline: execution.Start.Deadline,
	}
	SortIDs(active.ParticipantIDs)
	if err := active.Validate(); err != nil {
		return GoldenFailureActiveExecution{}, err
	}
	return active.Snapshot(), nil
}

func (a GoldenFailureActiveExecution) Snapshot() GoldenFailureActiveExecution {
	clone := a
	clone.Expectation = CloneExecutionExpectation(a.Expectation)
	clone.Group = CloneGroup(a.Group)
	clone.Attempt = CloneAttempt(a.Attempt)
	clone.Wave = CloneExecution(a.Wave)
	clone.Assignment = a.Assignment.Snapshot()
	clone.ParticipantIDs = append([]uuid.UUID(nil), a.ParticipantIDs...)
	return clone
}

func (a GoldenFailureActiveExecution) Validate() error {
	if !validGoldenFailureActiveExpectation(a) || !validGoldenFailureActiveTiming(a) {
		return goldenFailureError("invalid active execution header")
	}
	if !validGoldenFailureActiveAttempt(a) {
		return goldenFailureError("invalid active attempt")
	}
	if !validGoldenFailureActiveGroup(a) {
		return goldenFailureError("active group lost its attempt")
	}
	if !validGoldenFailureActiveWave(a) {
		return goldenFailureError("invalid active Wave")
	}
	if !validGoldenFailureActiveAssignment(a) {
		return goldenFailureError("invalid active assignment binding")
	}
	return nil
}

func validGoldenFailureActiveExpectation(active GoldenFailureActiveExecution) bool {
	return validGoldenFailureActiveExecutionHead(active) &&
		validGoldenFailureActiveWindowHead(active.Expectation.Window) &&
		validGoldenFailureActiveMembershipHead(active)
}

func validGoldenFailureActiveExecutionHead(active GoldenFailureActiveExecution) bool {
	expected := active.Expectation
	return active.Scope.IsValid() && expected.Scope == active.Scope.State && expected.Started &&
		expected.Source.Scope == active.Scope.State && expected.RevisionID != uuid.Nil && expected.Revision >= 1 &&
		expected.PayloadDigest != [sha256.Size]byte{} && expected.AttemptID == active.Scope.AttemptID &&
		expected.WaveID == active.Scope.WaveID && !expected.WaveRevisionID.IsZero() &&
		expected.AssignmentID == active.Scope.AssignmentID && expected.AssignmentRevisionID != uuid.Nil &&
		expected.AssignmentRevision >= 1 && expected.AssignmentDigest != [sha256.Size]byte{}
}

func validGoldenFailureActiveWindowHead(window GoldenReadyWindowExpectation) bool {
	return window.WindowID != uuid.Nil && window.RevisionID != uuid.Nil && window.Revision >= 1 &&
		window.ReadinessRevisionID != uuid.Nil && window.ReadinessRevision >= 1 &&
		window.PresenceRevisionID != uuid.Nil && window.PresenceRevision >= 1
}

func validGoldenFailureActiveMembershipHead(active GoldenFailureActiveExecution) bool {
	expected := active.Expectation
	participantDigest := DigestIDs(active.ParticipantIDs)
	return expected.MembershipID != uuid.Nil && expected.MembershipRevisionID != uuid.Nil &&
		expected.MembershipRevision >= 1 && expected.MembershipDigest != [sha256.Size]byte{} &&
		expected.MembershipDigest == participantDigest && expected.Window.ReadinessDigest == participantDigest &&
		expected.Window.PresenceDigest == participantDigest
}

func validGoldenFailureActiveTiming(active GoldenFailureActiveExecution) bool {
	return domain.IsValidServerTime(active.StartedAt) && domain.IsValidServerTime(active.Deadline) &&
		active.Deadline.After(active.StartedAt) && len(active.ParticipantIDs) >= 2 &&
		len(active.ParticipantIDs) <= domain.TournamentMaxParticipants && IDsCanonical(active.ParticipantIDs)
}

func validGoldenFailureActiveAttempt(active GoldenFailureActiveExecution) bool {
	attempt := active.Attempt
	return attempt.Validate() == nil && attempt.State == domain.GoldenAttemptStateActive &&
		attempt.ID == active.Scope.AttemptID && attempt.StartedAt != nil && attempt.StartedAt.Equal(active.StartedAt) &&
		attempt.GroupID == active.Scope.State.GroupID && attempt.GroupRevisionID == active.Scope.State.GroupRevisionID &&
		EqualIDs(attempt.ParticipantIDs, active.ParticipantIDs)
}

func validGoldenFailureActiveGroup(active GoldenFailureActiveExecution) bool {
	if _, err := domain.NewGoldenGroup(active.Group); err != nil || len(active.Group.Attempts) == 0 {
		return false
	}
	return active.Group.ID == active.Scope.State.GroupID &&
		active.Group.RevisionID == active.Scope.State.GroupRevisionID &&
		active.Group.TournamentID == active.Scope.State.TournamentID &&
		reflect.DeepEqual(active.Group.Attempts[len(active.Group.Attempts)-1], active.Attempt)
}

func validGoldenFailureActiveWave(active GoldenFailureActiveExecution) bool {
	wave := active.Wave
	if wave.Validate() != nil || wave.State != domain.WaveStateActive || wave.StartedAt == nil ||
		wave.ReadyWindow == nil || wave.ReadyWindow.ConsumedAt == nil {
		return false
	}
	return wave.ID == active.Scope.WaveID && wave.StartedAt.Equal(active.StartedAt) &&
		wave.TournamentID == active.Scope.State.TournamentID && wave.RevisionID == active.Expectation.WaveRevisionID &&
		wave.ReadyWindow.ID == active.Expectation.Window.WindowID &&
		wave.ReadyWindow.State == domain.ReadyWindowStateConsumed &&
		wave.ReadyWindow.ConsumedAt.Equal(active.StartedAt) &&
		EqualIDs(MemberIDs(wave), active.ParticipantIDs)
}

func validGoldenFailureActiveAssignment(active GoldenFailureActiveExecution) bool {
	assignment := active.Assignment
	if assignment.Validate() != nil || assignment.ID != active.Scope.AssignmentID ||
		assignment.ID != active.Expectation.AssignmentID || assignment.RevisionID != active.Expectation.AssignmentRevisionID ||
		assignment.Revision != active.Expectation.AssignmentRevision || assignment.Scope != active.Scope.State ||
		assignment.MembershipID != active.Expectation.MembershipID || assignment.Plan != active.Expectation.Source.Plan {
		return false
	}
	return validGoldenFailureActiveAssignmentPayload(active)
}

func validGoldenFailureActiveAssignmentPayload(active GoldenFailureActiveExecution) bool {
	assignment := active.Assignment
	return assignment.AttemptID == active.Scope.AttemptID && assignment.WaveID == active.Scope.WaveID &&
		assignment.SnapshotID == active.Scope.SnapshotID && assignment.TaskID == active.Scope.TaskID &&
		assignment.ExecutionPayloadDigest == active.Expectation.AssignmentDigest &&
		EqualIDs(PrivateAssignmentParticipantIDs(assignment.Private), active.ParticipantIDs)
}

func ClassifyGoldenFailure(
	plan ExactPlan,
	active GoldenFailureActiveExecution,
) (GoldenFailureClassification, error) {
	if len(plan.Groups) == 0 || len(plan.Groups) > domain.TournamentMaxParticipants || active.Validate() != nil || plan.Validate() != nil {
		return GoldenFailureClassification{}, goldenFailureError("malformed plan or active execution")
	}
	return classifyGoldenFailureValidatedPlan(plan, active)
}

func classifyGoldenFailureValidatedPlan(
	plan ExactPlan,
	active GoldenFailureActiveExecution,
) (GoldenFailureClassification, error) {
	group := goldenFailurePlanGroup(plan, active.Scope.State)
	if group == nil || len(group.Edges) != domain.AssignmentReserveCount+1 {
		return GoldenFailureClassification{}, goldenFailureError("locked group chain is missing")
	}
	failedIndex := active.Attempt.AttemptNo - 1
	if failedIndex < 0 || failedIndex >= len(group.Edges) {
		return GoldenFailureClassification{}, goldenFailureError("failed attempt is outside the locked chain")
	}
	failed := goldenFailureEdge(group.Edges[failedIndex])
	if !goldenFailureEdgeMatchesAssignment(failed, active.Assignment) {
		return GoldenFailureClassification{}, goldenFailureError("failed attempt did not use its locked edge")
	}
	classification := GoldenFailureClassification{
		Plan: active.Expectation.Source.Plan, FailedEdge: failed, FailedAttemptNo: active.Attempt.AttemptNo,
		ParticipantIDs:   append([]uuid.UUID(nil), active.ParticipantIDs...),
		MembershipDigest: DigestIDs(active.ParticipantIDs),
	}
	if failedIndex+1 < len(group.Edges) {
		next := goldenFailureEdge(group.Edges[failedIndex+1])
		classification.Kind = GoldenFailureClassificationReplay
		classification.NextEdge = &next
	} else {
		classification.Kind = GoldenFailureClassificationExhausted
		classification.Exhausted = true
	}
	payload, err := goldenFailureClassificationPayload(classification)
	if err != nil {
		return GoldenFailureClassification{}, goldenFailureError("encode reserve classification")
	}
	classification.PayloadDigest = sha256.Sum256(payload)
	if err := classification.Validate(); err != nil {
		return GoldenFailureClassification{}, err
	}
	return classification.Snapshot(), nil
}

func goldenFailurePlanGroup(plan ExactPlan, scope GoldenStateScope) *Group {
	for index := range plan.Groups {
		candidate := &plan.Groups[index]
		if candidate.GroupID == scope.GroupID && candidate.GroupRevisionID == scope.GroupRevisionID {
			return candidate
		}
	}
	return nil
}

func goldenFailureEdgeMatchesAssignment(edge GoldenFailureEdge, assignment GoldenAttemptAssignmentEvidence) bool {
	return edge.ID == assignment.EdgeID && edge.ReservationID == assignment.ReservationID &&
		edge.SnapshotID == assignment.SnapshotID && edge.TaskID == assignment.TaskID &&
		edge.ContentDigest == assignment.ContentDigest
}

func goldenFailureEdge(edge Edge) GoldenFailureEdge {
	return GoldenFailureEdge{
		ID: edge.ID, Position: edge.Position, ReservationID: edge.ReservationID,
		SnapshotID: edge.Snapshot.SnapshotID, TaskID: edge.Snapshot.TaskID, ContentDigest: edge.ContentDigest,
	}
}

func validGoldenFailureEdge(edge GoldenFailureEdge) bool {
	return edge.ID != uuid.Nil && edge.Position >= 1 && edge.ReservationID != uuid.Nil &&
		edge.SnapshotID != uuid.Nil && edge.TaskID != uuid.Nil && edge.ContentDigest != [sha256.Size]byte{}
}

// FailureRepository atomically archives the failed execution and stores one failure route.
type FailureRepository interface {
	FindGoldenFailure(ctx context.Context, tournamentID, commandID uuid.UUID) (*GoldenFailureRecord, error)
	LoadGoldenFailureAuthority(ctx context.Context, scope GoldenSubmissionScope) (GoldenFailureAuthority, error)
	CommitGoldenFailure(ctx context.Context, commit GoldenFailureCommit) (*GoldenFailureRecord, bool, error)
}

func Encode(value any) ([]byte, error) {
	return goldenattempt.Encode(value)
}

func DigestIDs(values []uuid.UUID) [sha256.Size]byte {
	canonical := append([]uuid.UUID(nil), values...)
	SortIDs(canonical)
	payload, _ := Encode(canonical)
	return sha256.Sum256(payload)
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

func ContainsID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func IDSet(values []uuid.UUID) map[uuid.UUID]bool {
	result := make(map[uuid.UUID]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
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

func CloneExecution(input domain.Wave) domain.Wave {
	return goldenexecution.CloneExecution(input)
}

func CloneExecutionExpectation(input GoldenWaveExecutionExpectation) GoldenWaveExecutionExpectation {
	return goldenexecution.CloneExecutionExpectation(input)
}

func CloneExpectation(input GoldenStateExpectation) GoldenStateExpectation {
	return goldenstate.CloneExpectation(input)
}

func CloneMembershipBinding(input GoldenWaveMembershipBinding) GoldenWaveMembershipBinding {
	return goldenexecution.CloneMembershipBinding(input)
}

func CloneReadyWindow(input GoldenReadyWindow) GoldenReadyWindow {
	return goldenstate.CloneReadyWindow(input)
}

func BuildAttemptAssignmentEvidence(input goldenexecution.GoldenAttemptAssignment) (GoldenAttemptAssignmentEvidence, error) {
	return goldenexecution.BuildAttemptAssignmentEvidence(input)
}

func PrivateAssignmentParticipantIDs(assignments []GoldenPrivateAssignment) []uuid.UUID {
	return goldenexecution.PrivateAssignmentParticipantIDs(assignments)
}

func MemberIDs(wave domain.Wave) []uuid.UUID {
	return goldenexecution.MemberIDs(wave)
}

func ReadyMemberIDs(wave domain.Wave) []uuid.UUID {
	return goldenexecution.ReadyMemberIDs(wave)
}

func BuildMembers(participants []uuid.UUID) []domain.WaveMember {
	return goldenexecution.BuildMembers(participants)
}

func ValidateReadyWindowIdentity(window GoldenReadyWindow) error {
	return goldenstate.ValidateReadyWindowIdentity(window)
}

func ValidateReadyWindowParticipants(window GoldenReadyWindow) error {
	return goldenexecution.ValidateReadyWindowParticipants(window)
}

func MembershipRevisionsEqual(first, second goldenstate.GoldenMembershipRevision) bool {
	return goldenstate.MembershipRevisionsEqual(first, second)
}

func ValidRecord(submission GoldenSubmissionRecord, scope GoldenSubmissionScope, expectedID uint64) bool {
	return goldensubmission.ValidRecord(submission, scope, expectedID)
}

func ValidateFreshIdentityIDs(state GoldenState, identities ...uuid.UUID) error {
	return goldenwave.ValidateFreshIdentityIDs(state, identities...)
}

func IdentitySetFromExpectation(expectation GoldenWaveExecutionExpectation) map[uuid.UUID]struct{} {
	return goldenexecution.IdentitySetFromExpectation(expectation)
}

type failureIdentityRole struct{ Value uuid.UUID }

func CoreIdentityRoles(state GoldenState) []failureIdentityRole {
	values := goldenstate.RetainedIdentityValues(state)
	roles := make([]failureIdentityRole, len(values))
	for index, value := range values {
		roles[index] = failureIdentityRole{Value: value}
	}
	return roles
}
func PlanIdentityRoles(state GoldenState) []failureIdentityRole   { return CoreIdentityRoles(state) }
func WindowIdentityRoles(state GoldenState) []failureIdentityRole { return CoreIdentityRoles(state) }
func TransitionIdentityRoles(state GoldenState) []failureIdentityRole {
	return CoreIdentityRoles(state)
}
