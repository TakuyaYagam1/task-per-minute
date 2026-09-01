package arena

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	goldenFailureCommitAttempts = 3
	goldenFailureAttemptLimit   = domain.ArenaAssignmentReserveCount + 1
	goldenFailureReceiptLimit   = domain.ArenaMaxParticipants * 2
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
		len(classification.ParticipantIDs) <= goldenIndividualParticipantLimit &&
		goldenIDsAreCanonical(classification.ParticipantIDs) &&
		classification.MembershipDigest == goldenParticipantSetDigest(classification.ParticipantIDs)
}

type GoldenFailureActiveExecution struct {
	Scope          GoldenSubmissionScope
	Expectation    GoldenWaveExecutionExpectation
	Group          domain.ArenaGoldenGroupState
	Attempt        domain.ArenaGoldenAttempt
	Wave           domain.ArenaWave
	Assignment     GoldenAttemptAssignmentEvidence
	ParticipantIDs []uuid.UUID
	StartedAt      time.Time
	Deadline       time.Time
}

func NewGoldenFailureActiveExecution(execution GoldenWaveExecution) (GoldenFailureActiveExecution, error) {
	if execution.Validate() != nil || execution.Start == nil {
		return GoldenFailureActiveExecution{}, goldenFailureError("active execution is malformed")
	}
	assignment, err := buildGoldenAttemptAssignmentEvidence(execution.Assignment)
	if err != nil {
		return GoldenFailureActiveExecution{}, err
	}
	active := GoldenFailureActiveExecution{
		Scope: GoldenSubmissionScope{
			State: execution.Scope, AttemptID: execution.Attempt.ID, WaveID: execution.Wave.ID,
			AssignmentID: execution.Assignment.ID, SnapshotID: execution.Assignment.Snapshot.SnapshotID,
			TaskID: execution.Assignment.Snapshot.TaskID,
		},
		Expectation: execution.Expectation(), Group: cloneGoldenStateGroup(execution.Group),
		Attempt: cloneGoldenAttempt(execution.Attempt), Wave: cloneArenaWaveExecution(execution.Wave),
		Assignment: assignment, ParticipantIDs: append([]uuid.UUID(nil), execution.Membership.ParticipantIDs...),
		StartedAt: execution.Start.StartedAt, Deadline: execution.Start.Deadline,
	}
	canonicalGoldenIDs(active.ParticipantIDs)
	if err := active.Validate(); err != nil {
		return GoldenFailureActiveExecution{}, err
	}
	return active.Snapshot(), nil
}

func (a GoldenFailureActiveExecution) Snapshot() GoldenFailureActiveExecution {
	clone := a
	clone.Expectation = cloneGoldenExecutionExpectationValue(a.Expectation)
	clone.Group = cloneGoldenStateGroup(a.Group)
	clone.Attempt = cloneGoldenAttempt(a.Attempt)
	clone.Wave = cloneArenaWaveExecution(a.Wave)
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
	participantDigest := goldenParticipantSetDigest(active.ParticipantIDs)
	return expected.MembershipID != uuid.Nil && expected.MembershipRevisionID != uuid.Nil &&
		expected.MembershipRevision >= 1 && expected.MembershipDigest != [sha256.Size]byte{} &&
		expected.MembershipDigest == participantDigest && expected.Window.ReadinessDigest == participantDigest &&
		expected.Window.PresenceDigest == participantDigest
}

func validGoldenFailureActiveTiming(active GoldenFailureActiveExecution) bool {
	return validArenaServerTime(active.StartedAt) && validArenaServerTime(active.Deadline) &&
		active.Deadline.After(active.StartedAt) && len(active.ParticipantIDs) >= 2 &&
		len(active.ParticipantIDs) <= goldenIndividualParticipantLimit && goldenIDsAreCanonical(active.ParticipantIDs)
}

func validGoldenFailureActiveAttempt(active GoldenFailureActiveExecution) bool {
	attempt := active.Attempt
	return attempt.Validate() == nil && attempt.State == domain.ArenaGoldenAttemptStateActive &&
		attempt.ID == active.Scope.AttemptID && attempt.StartedAt != nil && attempt.StartedAt.Equal(active.StartedAt) &&
		attempt.GroupID == active.Scope.State.GroupID && attempt.GroupRevisionID == active.Scope.State.GroupRevisionID &&
		equalGoldenIDs(attempt.ParticipantIDs, active.ParticipantIDs)
}

func validGoldenFailureActiveGroup(active GoldenFailureActiveExecution) bool {
	if _, err := domain.NewArenaGoldenGroup(active.Group); err != nil || len(active.Group.Attempts) == 0 {
		return false
	}
	return active.Group.ID == active.Scope.State.GroupID &&
		active.Group.RevisionID == active.Scope.State.GroupRevisionID &&
		active.Group.TournamentID == active.Scope.State.TournamentID &&
		reflect.DeepEqual(active.Group.Attempts[len(active.Group.Attempts)-1], active.Attempt)
}

func validGoldenFailureActiveWave(active GoldenFailureActiveExecution) bool {
	wave := active.Wave
	if wave.Validate() != nil || wave.State != domain.ArenaWaveStateActive || wave.StartedAt == nil ||
		wave.ReadyWindow == nil || wave.ReadyWindow.ConsumedAt == nil {
		return false
	}
	return wave.ID == active.Scope.WaveID && wave.StartedAt.Equal(active.StartedAt) &&
		wave.TournamentID == active.Scope.State.TournamentID && wave.RevisionID == active.Expectation.WaveRevisionID &&
		wave.ReadyWindow.ID == active.Expectation.Window.WindowID &&
		wave.ReadyWindow.State == domain.ArenaReadyWindowStateConsumed &&
		wave.ReadyWindow.ConsumedAt.Equal(active.StartedAt) &&
		equalGoldenIDs(goldenWaveMemberIDs(wave), active.ParticipantIDs)
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
		equalGoldenIDs(goldenPrivateAssignmentIDs(assignment.Private), active.ParticipantIDs)
}

func ClassifyGoldenFailure(
	plan GoldenExactPlan,
	active GoldenFailureActiveExecution,
) (GoldenFailureClassification, error) {
	if len(plan.Groups) == 0 || len(plan.Groups) > goldenIndividualParticipantLimit || active.Validate() != nil || plan.Validate() != nil {
		return GoldenFailureClassification{}, goldenFailureError("malformed plan or active execution")
	}
	return classifyGoldenFailureValidatedPlan(plan, active)
}

func classifyGoldenFailureValidatedPlan(
	plan GoldenExactPlan,
	active GoldenFailureActiveExecution,
) (GoldenFailureClassification, error) {
	group := goldenFailurePlanGroup(plan, active.Scope.State)
	if group == nil || len(group.Edges) != domain.ArenaAssignmentReserveCount+1 {
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
		MembershipDigest: goldenParticipantSetDigest(active.ParticipantIDs),
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

func goldenFailurePlanGroup(plan GoldenExactPlan, scope GoldenStateScope) *GoldenExactGroupPlan {
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

func goldenFailureEdge(edge GoldenExactPlanEdge) GoldenFailureEdge {
	return GoldenFailureEdge{
		ID: edge.ID, Position: edge.Position, ReservationID: edge.ReservationID,
		SnapshotID: edge.Snapshot.SnapshotID, TaskID: edge.Snapshot.TaskID, ContentDigest: edge.ContentDigest,
	}
}

func validGoldenFailureEdge(edge GoldenFailureEdge) bool {
	return edge.ID != uuid.Nil && edge.Position >= 1 && edge.ReservationID != uuid.Nil &&
		edge.SnapshotID != uuid.Nil && edge.TaskID != uuid.Nil && edge.ContentDigest != [sha256.Size]byte{}
}

type GoldenFailureExpectation struct {
	Scope                GoldenSubmissionScope
	State                GoldenStateExpectation
	Execution            GoldenWaveExecutionExpectation
	StartedAt            time.Time
	Deadline             time.Time
	Submissions          GoldenSubmissionLedgerExpectation
	Positions            GoldenPositionLedgerExpectation
	Plan                 GoldenPlanStateBinding
	SwissPoints          GoldenSwissPointLedgerSentinel
	ClassificationDigest [sha256.Size]byte
}

func (e GoldenFailureExpectation) Equal(other GoldenFailureExpectation) bool {
	return e.Scope == other.Scope && e.State.Equal(other.State) && e.Execution.Equal(other.Execution) &&
		e.StartedAt.Equal(other.StartedAt) && e.Deadline.Equal(other.Deadline) && e.Submissions.Equal(other.Submissions) &&
		e.Positions.Equal(other.Positions) && e.Plan == other.Plan && e.SwissPoints == other.SwissPoints &&
		e.ClassificationDigest == other.ClassificationDigest
}

type GoldenFailureAuthority struct {
	State          GoldenState
	Active         GoldenFailureActiveExecution
	Submissions    GoldenSubmissionLedger
	Positions      GoldenPositionLedger
	Plan           GoldenExactPlan
	SwissPoints    GoldenSwissPointLedgerSentinel
	Classification GoldenFailureClassification
	Current        *GoldenFailureRecord
}

func (a GoldenFailureAuthority) Snapshot() GoldenFailureAuthority {
	clone := a
	clone.State = a.State.Snapshot()
	clone.Active = a.Active.Snapshot()
	clone.Submissions = a.Submissions.Snapshot()
	clone.Positions = a.Positions.Snapshot()
	clone.Plan = a.Plan.Snapshot()
	clone.Classification = a.Classification.Snapshot()
	if a.Current != nil {
		current := a.Current.Snapshot()
		clone.Current = &current
	}
	return clone
}

func (a GoldenFailureAuthority) Expectation() GoldenFailureExpectation {
	return GoldenFailureExpectation{
		Scope: a.Active.Scope, State: a.State.Expectation(), Execution: cloneGoldenExecutionExpectationValue(a.Active.Expectation),
		StartedAt: a.Active.StartedAt, Deadline: a.Active.Deadline,
		Submissions: a.Submissions.Expectation(), Positions: a.Positions.Expectation(),
		Plan: a.State.Plan, SwissPoints: a.SwissPoints, ClassificationDigest: a.Classification.PayloadDigest,
	}
}

func (a GoldenFailureAuthority) Validate() error {
	if !goldenFailureAuthorityCollectionsWithinBounds(a) {
		return goldenFailureError("authority collection is out of bounds")
	}
	if !validGoldenFailureAuthorityDocuments(a) {
		return goldenFailureError("malformed failure authority")
	}
	if !validGoldenFailureAuthorityHeads(a) {
		return goldenFailureError("failure heads do not share one authority")
	}
	if err := validateGoldenFailureSubmissionMembership(a); err != nil {
		return err
	}
	classification, err := classifyGoldenFailureValidatedPlan(a.Plan, a.Active)
	if err != nil || !reflect.DeepEqual(classification, a.Classification) {
		return goldenFailureError("reserve classification does not match the locked plan")
	}
	if err := validateGoldenFailurePriorPositions(a.Active.Group, a.Positions); err != nil {
		return err
	}
	if a.Current != nil && (a.Current.Validate() != nil || !a.Current.Expected.Equal(a.Expectation())) {
		return goldenFailureError("invalid current failure receipt")
	}
	return nil
}

func validGoldenFailureAuthorityDocuments(authority GoldenFailureAuthority) bool {
	return authority.State.Validate() == nil && authority.Active.Validate() == nil &&
		authority.Submissions.Validate() == nil && authority.Positions.Validate() == nil &&
		authority.SwissPoints.Validate() == nil && authority.Classification.Validate() == nil
}

func validGoldenFailureAuthorityHeads(authority GoldenFailureAuthority) bool {
	if !authority.State.Expectation().Equal(authority.Active.Expectation.Source) ||
		authority.Submissions.Scope != authority.Active.Scope || authority.Positions.Scope != authority.Active.Scope.State {
		return false
	}
	if authority.State.Plan != authority.Active.Expectation.Source.Plan ||
		authority.State.Plan.PlanID != authority.Plan.PlanID ||
		authority.State.Plan.RevisionID != authority.Plan.PlanRevisionID ||
		authority.Active.Assignment.Plan != authority.State.Plan || !reflect.DeepEqual(authority.Plan, authority.State.ExactPlan) {
		return false
	}
	return authority.Positions.PositionFrom == authority.Active.Group.PositionFrom &&
		authority.Positions.PositionTo == authority.Active.Group.PositionTo
}

func validateGoldenFailureSubmissionMembership(authority GoldenFailureAuthority) error {
	for _, submission := range authority.Submissions.Submissions {
		if !goldenIDsContain(authority.Active.ParticipantIDs, submission.ParticipantID) ||
			submission.AssignmentDigest != authority.Active.Expectation.AssignmentDigest {
			return goldenFailureError("current submissions do not belong to the active assignment")
		}
	}
	for _, receipt := range authority.Submissions.Receipts {
		if !goldenIDsContain(authority.Active.ParticipantIDs, receipt.ParticipantID) {
			return goldenFailureError("current submission receipt belongs to another membership")
		}
	}
	return nil
}

func goldenFailureAuthorityCollectionsWithinBounds(a GoldenFailureAuthority) bool {
	if !goldenFailureCoreCollectionsWithinBounds(a) || !goldenFailurePlanCollectionsWithinBounds(a.Plan) {
		return false
	}
	return true
}

func goldenFailureCoreCollectionsWithinBounds(authority GoldenFailureAuthority) bool {
	return len(authority.Active.ParticipantIDs) >= 2 &&
		len(authority.Active.ParticipantIDs) <= domain.ArenaMaxParticipants &&
		len(authority.Active.Group.Members) <= domain.ArenaMaxParticipants &&
		len(authority.Active.Group.Attempts) <= goldenFailureAttemptLimit &&
		len(authority.State.Group.Members) <= domain.ArenaMaxParticipants &&
		len(authority.State.Group.Attempts) <= goldenFailureAttemptLimit &&
		len(authority.Submissions.Submissions) <= domain.ArenaMaxParticipants &&
		len(authority.Submissions.Receipts) <= goldenFailureReceiptLimit &&
		len(authority.Positions.Positions) <= domain.ArenaMaxParticipants &&
		len(authority.Positions.Attempts) <= goldenFailureAttemptLimit
}

func goldenFailurePlanCollectionsWithinBounds(plan GoldenExactPlan) bool {
	if len(plan.Groups) == 0 || len(plan.Groups) > domain.ArenaMaxParticipants ||
		len(plan.Authority.Groups) > domain.ArenaMaxParticipants ||
		len(plan.Authority.ParticipantReservations) > domain.ArenaMaxParticipants {
		return false
	}
	for _, group := range plan.Groups {
		if len(group.ParticipantIDs) < 2 || len(group.ParticipantIDs) > domain.ArenaMaxParticipants ||
			len(group.Edges) != goldenFailureAttemptLimit {
			return false
		}
	}
	for _, group := range plan.Authority.Groups {
		if len(group.ActiveParticipantIDs) < 2 || len(group.ActiveParticipantIDs) > domain.ArenaMaxParticipants {
			return false
		}
	}
	return true
}

func validateGoldenFailurePriorPositions(
	group domain.ArenaGoldenGroupState,
	positions GoldenPositionLedger,
) error {
	if positions.PositionFrom != group.PositionFrom || positions.PositionTo != group.PositionTo {
		return goldenFailureError("prior position interval changed")
	}
	members := goldenFailureGroupMemberSet(group)
	byID := goldenFailureAttemptSet(group)
	for _, ordering := range positions.Attempts {
		if err := validateGoldenFailureAttemptOrdering(ordering, members, byID); err != nil {
			return err
		}
	}
	for _, position := range positions.Positions {
		if err := validateGoldenFailureCommittedPosition(position, members, byID); err != nil {
			return err
		}
	}
	return nil
}

func goldenFailureGroupMemberSet(group domain.ArenaGoldenGroupState) map[uuid.UUID]struct{} {
	members := make(map[uuid.UUID]struct{}, len(group.Members))
	for _, member := range group.Members {
		members[member.ParticipantID] = struct{}{}
	}
	return members
}

func goldenFailureAttemptSet(group domain.ArenaGoldenGroupState) map[uuid.UUID]domain.ArenaGoldenAttempt {
	attempts := make(map[uuid.UUID]domain.ArenaGoldenAttempt, len(group.Attempts))
	for _, attempt := range group.Attempts {
		attempts[attempt.ID] = attempt
	}
	return attempts
}

func validateGoldenFailureAttemptOrdering(
	ordering GoldenAttemptOrderingEvidence,
	members map[uuid.UUID]struct{},
	attempts map[uuid.UUID]domain.ArenaGoldenAttempt,
) error {
	attempt, found := attempts[ordering.AttemptID]
	if !found || attempt.State != domain.ArenaGoldenAttemptStateCompleted || attempt.AttemptNo != ordering.AttemptNo {
		return goldenFailureError("prior position does not belong to a completed attempt")
	}
	for _, entry := range ordering.Order {
		if _, found := members[entry.ParticipantID]; !found {
			return goldenFailureError("prior ordering contains a foreign participant")
		}
	}
	return nil
}

func validateGoldenFailureCommittedPosition(
	position GoldenCommittedPosition,
	members map[uuid.UUID]struct{},
	attempts map[uuid.UUID]domain.ArenaGoldenAttempt,
) error {
	attempt, found := attempts[position.AttemptID]
	_, member := members[position.ParticipantID]
	if !member || !found || attempt.State != domain.ArenaGoldenAttemptStateCompleted ||
		attempt.AttemptNo != position.AttemptNo {
		return goldenFailureError("committed position does not belong to a completed group attempt")
	}
	return nil
}

type GoldenDiscardedOrderEntry struct {
	SubmissionID   uint64
	ParticipantID  uuid.UUID
	CommittedAt    time.Time
	EvidenceDigest [sha256.Size]byte
}

type GoldenFailureReplacement struct {
	Group      domain.ArenaGoldenGroupState
	Attempt    domain.ArenaGoldenAttempt
	Assignment GoldenReserveAttemptAssignment
	Membership GoldenWaveMembershipBinding
	Wave       domain.ArenaWave
	Window     GoldenReadyWindow
	OpenedAt   time.Time
	Deadline   time.Time
}

func (r GoldenFailureReplacement) Snapshot() GoldenFailureReplacement {
	clone := r
	clone.Group = cloneGoldenStateGroup(r.Group)
	clone.Attempt = cloneGoldenAttempt(r.Attempt)
	clone.Assignment = r.Assignment.Snapshot()
	clone.Membership = cloneGoldenMembershipBinding(r.Membership)
	clone.Wave = cloneArenaWaveExecution(r.Wave)
	clone.Window = cloneGoldenWindow(r.Window)
	return clone
}

func (r GoldenFailureReplacement) Validate() error {
	if !validGoldenFailureReplacementAttempt(r) || !validGoldenFailureReplacementAssignment(r) {
		return goldenFailureError("invalid replacement attempt or assignment")
	}
	if !validGoldenFailureReplacementWave(r) || !validGoldenFailureReplacementWindow(r) ||
		!validGoldenFailureReplacementMembership(r) {
		return goldenFailureError("invalid unstarted replacement Wave")
	}
	return nil
}

func validGoldenFailureReplacementAttempt(replacement GoldenFailureReplacement) bool {
	if _, err := domain.NewArenaGoldenGroup(replacement.Group); err != nil ||
		replacement.Attempt.Validate() != nil || len(replacement.Group.Attempts) == 0 {
		return false
	}
	return replacement.Attempt.State == domain.ArenaGoldenAttemptStatePlanned &&
		reflect.DeepEqual(replacement.Group.Attempts[len(replacement.Group.Attempts)-1], replacement.Attempt)
}

func validGoldenFailureReplacementAssignment(replacement GoldenFailureReplacement) bool {
	assignment := replacement.Assignment
	return assignment.Validate() == nil && assignment.AttemptID == replacement.Attempt.ID &&
		assignment.Scope.GroupID == replacement.Attempt.GroupID &&
		assignment.Scope.GroupRevisionID == replacement.Attempt.GroupRevisionID &&
		equalGoldenIDs(replacement.Attempt.ParticipantIDs, goldenPrivateAssignmentIDs(assignment.Private))
}

func validGoldenFailureReplacementWave(replacement GoldenFailureReplacement) bool {
	wave := replacement.Wave
	if wave.Validate() != nil || wave.State != domain.ArenaWaveStateReadyWindowOpen || wave.StartedAt != nil ||
		wave.ReadyWindow == nil || wave.ReadyWindow.RevisionID.IsZero() {
		return false
	}
	return wave.ReadyWindow.ID == replacement.Window.ID &&
		wave.ReadyWindow.OpenedAt.Equal(replacement.OpenedAt) && wave.ReadyWindow.Deadline.Equal(replacement.Deadline) &&
		equalGoldenIDs(goldenWaveMemberIDs(wave), replacement.Attempt.ParticipantIDs) &&
		len(goldenReadyWaveMemberIDs(wave)) == 0
}

func validGoldenFailureReplacementWindow(replacement GoldenFailureReplacement) bool {
	window := replacement.Window
	return validArenaServerTime(replacement.OpenedAt) && validArenaServerTime(replacement.Deadline) &&
		replacement.Deadline.Equal(replacement.OpenedAt.Add(goldenReadyWindowDuration)) &&
		window.State == GoldenReadyWindowOpen && validateGoldenWindowIdentity(window) == nil &&
		validateGoldenWindowParticipantSets(window) == nil && len(window.ReadyParticipantIDs) == 0 &&
		equalGoldenIDs(window.PresentParticipantIDs, replacement.Attempt.ParticipantIDs) &&
		equalGoldenIDs(window.BasePresentParticipantIDs, replacement.Attempt.ParticipantIDs)
}

func validGoldenFailureReplacementMembership(replacement GoldenFailureReplacement) bool {
	membership := replacement.Membership
	return membership.ID != uuid.Nil && membership.RevisionID != uuid.Nil && membership.Revision == 1 &&
		membership.Source.RevisionID != uuid.Nil && membership.Source.Revision >= 1 &&
		membership.PayloadDigest == goldenParticipantSetDigest(membership.ParticipantIDs) &&
		equalGoldenIDs(membership.ParticipantIDs, replacement.Attempt.ParticipantIDs)
}

type GoldenGroupOperationalState string

const GoldenGroupStateTechnicalPause GoldenGroupOperationalState = "technical_pause"

type GoldenGroupTechnicalPause struct {
	GroupID                uuid.UUID
	GroupRevisionID        domain.ArenaDerivedRevisionID
	State                  GoldenGroupOperationalState
	RequiredOperatorAction string
	PausedAt               time.Time
}

type GoldenFailureRecord struct {
	ID                   uuid.UUID
	CommandID            uuid.UUID
	CommandDigest        [sha256.Size]byte
	Route                GoldenFailureRoute
	Scope                GoldenSubmissionScope
	Expected             GoldenFailureExpectation
	Classification       GoldenFailureClassification
	FailedAssignment     GoldenAttemptAssignmentEvidence
	FailedAt             time.Time
	Attempt              domain.ArenaGoldenAttempt
	Group                domain.ArenaGoldenGroupState
	OldWave              domain.ArenaWave
	DiscardedSubmissions GoldenSubmissionLedgerExpectation
	DiscardedOrder       []GoldenSubmissionRecord
	PriorPositions       GoldenPositionLedger
	Positions            GoldenPositionLedger
	Replacement          *GoldenFailureReplacement
	TechnicalPause       *GoldenGroupTechnicalPause
	NewIdentityIDs       []uuid.UUID
	PayloadDigest        [sha256.Size]byte
}

func (r GoldenFailureRecord) Snapshot() GoldenFailureRecord {
	clone := r
	clone.Expected.State = cloneGoldenStateExpectation(r.Expected.State)
	clone.Expected.Execution = cloneGoldenExecutionExpectationValue(r.Expected.Execution)
	clone.Classification = r.Classification.Snapshot()
	clone.FailedAssignment = r.FailedAssignment.Snapshot()
	clone.Attempt = cloneGoldenAttempt(r.Attempt)
	clone.Group = cloneGoldenStateGroup(r.Group)
	clone.OldWave = cloneArenaWaveExecution(r.OldWave)
	clone.DiscardedOrder = append([]GoldenSubmissionRecord(nil), r.DiscardedOrder...)
	clone.PriorPositions = r.PriorPositions.Snapshot()
	clone.Positions = r.Positions.Snapshot()
	if r.Replacement != nil {
		replacement := r.Replacement.Snapshot()
		clone.Replacement = &replacement
	}
	if r.TechnicalPause != nil {
		pause := *r.TechnicalPause
		clone.TechnicalPause = &pause
	}
	clone.NewIdentityIDs = append([]uuid.UUID(nil), r.NewIdentityIDs...)
	return clone
}

func (r GoldenFailureRecord) Validate() error {
	if !validGoldenFailureRecordIdentity(r) || !validGoldenFailureRecordExpectedHeads(r) ||
		!validGoldenFailureRecordRetainedHeads(r) || !validGoldenFailureRecordLedgers(r) ||
		!validGoldenFailureRecordIdentitySet(r) {
		return goldenFailureError("invalid failure receipt header or retained heads")
	}
	if !validGoldenFailureRecordAttempt(r) {
		return goldenFailureError("failed attempt was not retained as void")
	}
	if !validGoldenFailureRecordOldWave(r) {
		return goldenFailureError("old Wave was not completed")
	}
	if err := validateGoldenFailurePriorPositions(r.Group, r.Positions); err != nil {
		return err
	}
	if !validGoldenFailureRecordAssignment(r) {
		return goldenFailureError("failed assignment does not match locked edge")
	}
	if err := validateGoldenDiscardedOrder(r); err != nil {
		return err
	}
	if err := validateGoldenFailureRecordRoute(r); err != nil {
		return err
	}
	payload, err := goldenFailureRecordPayload(r)
	if err != nil || r.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != r.PayloadDigest {
		return goldenFailureError("failure receipt digest changed")
	}
	return nil
}

func validGoldenFailureRecordIdentity(record GoldenFailureRecord) bool {
	return record.ID != uuid.Nil && record.CommandID != uuid.Nil && record.ID != record.CommandID &&
		record.CommandDigest != [sha256.Size]byte{} && record.Scope.IsValid() &&
		record.Expected.Scope == record.Scope && validArenaServerTime(record.FailedAt)
}

func validGoldenFailureRecordExpectedHeads(record GoldenFailureRecord) bool {
	expected := record.Expected
	if !validArenaServerTime(expected.StartedAt) || !validArenaServerTime(expected.Deadline) ||
		!expected.StartedAt.Before(expected.Deadline) || expected.State.Scope != record.Scope.State ||
		expected.Execution.Scope != record.Scope.State || !expected.Execution.Started {
		return false
	}
	return expected.Execution.AttemptID == record.Scope.AttemptID && expected.Execution.WaveID == record.Scope.WaveID &&
		expected.Execution.AssignmentID == record.Scope.AssignmentID && expected.Plan == expected.State.Plan &&
		expected.Submissions.Scope == record.Scope
}

func validGoldenFailureRecordRetainedHeads(record GoldenFailureRecord) bool {
	return record.Classification.Validate() == nil &&
		record.Classification.PayloadDigest == record.Expected.ClassificationDigest &&
		record.Classification.Plan == record.Expected.Plan && record.FailedAssignment.Validate() == nil &&
		record.DiscardedSubmissions == record.Expected.Submissions
}

func validGoldenFailureRecordLedgers(record GoldenFailureRecord) bool {
	return record.PriorPositions.Validate() == nil && record.Positions.Validate() == nil &&
		record.PriorPositions.Expectation().Equal(record.Expected.Positions) &&
		reflect.DeepEqual(record.PriorPositions, record.Positions)
}

func validGoldenFailureRecordIdentitySet(record GoldenFailureRecord) bool {
	return goldenIDsAreCanonical(record.NewIdentityIDs) &&
		equalGoldenIDs(record.NewIdentityIDs, goldenFailureRecordIdentityIDs(record))
}

func validGoldenFailureRecordAttempt(record GoldenFailureRecord) bool {
	attempt := record.Attempt
	if attempt.Validate() != nil || attempt.State != domain.ArenaGoldenAttemptStateVoid ||
		attempt.ID != record.Scope.AttemptID || attempt.FinishedAt == nil || attempt.StartedAt == nil ||
		len(record.Group.Attempts) == 0 {
		return false
	}
	return attempt.FinishedAt.Equal(record.FailedAt) && attempt.StartedAt.Equal(record.Expected.StartedAt) &&
		attempt.AttemptNo == record.Classification.FailedAttemptNo &&
		equalGoldenIDs(attempt.ParticipantIDs, record.Classification.ParticipantIDs) &&
		reflect.DeepEqual(record.Group.Attempts[len(record.Group.Attempts)-1], attempt)
}

func validGoldenFailureRecordOldWave(record GoldenFailureRecord) bool {
	if _, err := domain.NewArenaGoldenGroup(record.Group); err != nil || record.OldWave.Validate() != nil ||
		record.OldWave.StartedAt == nil {
		return false
	}
	return record.OldWave.State == domain.ArenaWaveStateCompleted && record.OldWave.ID == record.Scope.WaveID &&
		record.OldWave.StartedAt.Equal(record.Expected.StartedAt) &&
		!record.FailedAt.Before(*record.OldWave.StartedAt) &&
		equalGoldenIDs(goldenWaveMemberIDs(record.OldWave), record.Classification.ParticipantIDs)
}

func validGoldenFailureRecordAssignment(record GoldenFailureRecord) bool {
	assignment := record.FailedAssignment
	failed := record.Classification.FailedEdge
	if assignment.ID != record.Scope.AssignmentID || assignment.AttemptID != record.Scope.AttemptID ||
		assignment.WaveID != record.Scope.WaveID || assignment.EdgeID != failed.ID || assignment.Plan != record.Expected.Plan {
		return false
	}
	return assignment.ReservationID == failed.ReservationID && assignment.SnapshotID == failed.SnapshotID &&
		assignment.TaskID == failed.TaskID && assignment.ContentDigest == failed.ContentDigest &&
		assignment.ExecutionPayloadDigest == record.Expected.Execution.AssignmentDigest &&
		equalGoldenIDs(goldenPrivateAssignmentIDs(assignment.Private), record.Classification.ParticipantIDs)
}

func validateGoldenFailureRecordRoute(record GoldenFailureRecord) error {
	switch record.Route {
	case GoldenFailureRouteReplay:
		return validateGoldenFailureReplayRoute(record)
	case GoldenFailureRouteExhausted:
		return validateGoldenFailureExhaustedRoute(record)
	default:
		return goldenFailureError("unknown failure route")
	}
}

func validateGoldenFailureReplayRoute(record GoldenFailureRecord) error {
	if record.Classification.Exhausted || record.Replacement == nil || record.TechnicalPause != nil ||
		record.Classification.NextEdge == nil || record.Replacement.Validate() != nil {
		return goldenFailureError("replay receipt does not match next reserve")
	}
	if !validGoldenFailureReplayReplacement(record) {
		return goldenFailureError("replay receipt does not match next reserve")
	}
	prefix := cloneGoldenStateGroup(record.Replacement.Group)
	prefix.Attempts = prefix.Attempts[:len(prefix.Attempts)-1]
	if !validGoldenFailureReplayMembership(record, prefix) {
		return goldenFailureError("replacement changed void group or membership authority")
	}
	return nil
}

func validGoldenFailureReplayReplacement(record GoldenFailureRecord) bool {
	replacement := record.Replacement
	next := record.Classification.NextEdge
	return replacement.Assignment.EdgeID == next.ID && replacement.OpenedAt.Equal(record.FailedAt) &&
		replacement.Window.OpenedAt.Equal(record.FailedAt) && replacement.Assignment.Scope == record.Scope.State &&
		replacement.Assignment.ReservationID == next.ReservationID && replacement.Assignment.SnapshotID == next.SnapshotID &&
		replacement.Assignment.TaskID == next.TaskID && replacement.Assignment.ContentDigest == next.ContentDigest &&
		equalGoldenIDs(replacement.Attempt.ParticipantIDs, record.Classification.ParticipantIDs)
}

func validGoldenFailureReplayMembership(record GoldenFailureRecord, prefix domain.ArenaGoldenGroupState) bool {
	replacement := record.Replacement
	return reflect.DeepEqual(prefix, record.Group) &&
		goldenMembershipRevisionsEqual(replacement.Membership.Source, record.Expected.State.Membership) &&
		goldenIDsAreCanonical(replacement.Membership.ParticipantIDs) &&
		replacement.Membership.PayloadDigest == goldenParticipantSetDigest(replacement.Attempt.ParticipantIDs)
}

func validateGoldenFailureExhaustedRoute(record GoldenFailureRecord) error {
	if !record.Classification.Exhausted || record.Replacement != nil || record.TechnicalPause == nil {
		return goldenFailureError("exhaustion receipt lacks affected-group operator action")
	}
	pause := record.TechnicalPause
	action := strings.TrimSpace(pause.RequiredOperatorAction)
	if pause.State != GoldenGroupStateTechnicalPause || pause.GroupID != record.Scope.State.GroupID ||
		pause.GroupRevisionID != record.Scope.State.GroupRevisionID || action == "" ||
		action != pause.RequiredOperatorAction || len(action) > goldenFailureOperatorActionLimit ||
		!pause.PausedAt.Equal(record.FailedAt) {
		return goldenFailureError("exhaustion receipt lacks affected-group operator action")
	}
	return nil
}

func goldenFailureRecordIdentityIDs(record GoldenFailureRecord) []uuid.UUID {
	identities := []uuid.UUID{record.CommandID, record.ID, record.OldWave.RevisionID.UUID()}
	if record.Replacement != nil {
		replacement := record.Replacement
		identities = append(identities,
			replacement.Attempt.ID, replacement.Assignment.ID, replacement.Assignment.RevisionID,
			replacement.Wave.ID, replacement.Wave.RevisionID.UUID(), replacement.Window.ID,
			replacement.Wave.ReadyWindow.RevisionID.UUID(), replacement.Window.RevisionID,
			replacement.Window.ReadinessRevisionID, replacement.Window.PresenceRevisionID,
			replacement.Membership.ID, replacement.Membership.RevisionID,
		)
		for _, assignment := range replacement.Assignment.Private {
			identities = append(identities, assignment.ID)
		}
	}
	canonicalGoldenIDs(identities)
	return identities
}

func validateGoldenDiscardedOrder(record GoldenFailureRecord) error {
	if record.DiscardedSubmissions.NextSubmissionID != uint64(len(record.DiscardedOrder))+1 {
		return goldenFailureError("discarded order does not cover current submissions")
	}
	for index, submission := range record.DiscardedOrder {
		if !validGoldenSubmissionRecord(submission, record.Scope, uint64(index+1)) {
			return goldenFailureError("invalid discarded provisional position")
		}
		if index > 0 {
			prior := record.DiscardedOrder[index-1]
			if submission.CommittedAt.Before(prior.CommittedAt) ||
				(submission.CommittedAt.Equal(prior.CommittedAt) && submission.ID <= prior.ID) {
				return goldenFailureError("discarded provisional order changed")
			}
		}
	}
	return nil
}

type GoldenFailureCommit struct {
	Expected       GoldenFailureExpectation
	Route          GoldenFailureRoute
	NewIdentityIDs []uuid.UUID
	Record         GoldenFailureRecord
}

// GoldenFailureRepository owns one transaction for both replay and exhaustion.
// It locks and compares every head in Expected, globally reserves all new IDs,
// archives the failed execution and stores exactly one mutually exclusive route.
type GoldenFailureRepository interface {
	FindGoldenFailure(ctx context.Context, tournamentID, commandID uuid.UUID) (*GoldenFailureRecord, error)
	LoadGoldenFailureAuthority(ctx context.Context, scope GoldenSubmissionScope) (GoldenFailureAuthority, error)
	CommitGoldenFailure(ctx context.Context, commit GoldenFailureCommit) (*GoldenFailureRecord, bool, error)
}

type GoldenFailureReplayCommand struct {
	Scope                    GoldenSubmissionScope
	CommandID                uuid.UUID
	FailureID                uuid.UUID
	Expected                 GoldenFailureExpectation
	ClosedWaveRevisionID     domain.ArenaWaveRevisionID
	NextAttemptID            uuid.UUID
	NextAssignmentID         uuid.UUID
	NextAssignmentRevisionID uuid.UUID
	NextWaveID               uuid.UUID
	NextWaveRevisionID       domain.ArenaWaveRevisionID
	NextWindowID             uuid.UUID
	NextWaveWindowRevisionID domain.ArenaReadyWindowRevisionID
	NextWindowRevisionID     uuid.UUID
	NextReadinessRevisionID  uuid.UUID
	NextPresenceRevisionID   uuid.UUID
	NextMembershipID         uuid.UUID
	NextMembershipRevisionID uuid.UUID
	PrivateAssignments       []GoldenPrivateAssignmentCommand
}

type GoldenFailureReplayUseCase struct {
	repository GoldenFailureRepository
	clock      Clock
}

func NewGoldenFailureReplayUseCase(repository GoldenFailureRepository, clock Clock) *GoldenFailureReplayUseCase {
	return &GoldenFailureReplayUseCase{repository: repository, clock: clock}
}

func (u *GoldenFailureReplayUseCase) Replay(
	ctx context.Context,
	command GoldenFailureReplayCommand,
) (*GoldenFailureRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil || !validGoldenFailureReplayCommand(command) {
		return nil, false, domain.ErrValidation
	}
	command.PrivateAssignments = append([]GoldenPrivateAssignmentCommand(nil), command.PrivateAssignments...)
	digest := goldenFailureReplayCommandDigest(command)
	if replay, err := u.repository.FindGoldenFailure(ctx, command.Scope.State.TournamentID, command.CommandID); err != nil {
		return nil, false, fmt.Errorf("GoldenFailureReplayUseCase - find replay: %w", err)
	} else if replay != nil {
		return reconcileGoldenFailure(*replay, command.Scope, command.CommandID, GoldenFailureRouteReplay, digest)
	}
	failedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(failedAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenFailureCommitAttempts {
		record, changed, retry, err := u.replayAttempt(ctx, command, digest, failedAt)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrGoldenFailureConflict
}

func (u *GoldenFailureReplayUseCase) replayAttempt(
	ctx context.Context,
	command GoldenFailureReplayCommand,
	digest [sha256.Size]byte,
	failedAt time.Time,
) (*GoldenFailureRecord, bool, bool, error) {
	if replay, err := u.repository.FindGoldenFailure(ctx, command.Scope.State.TournamentID, command.CommandID); err != nil {
		return nil, false, false, fmt.Errorf("GoldenFailureReplayUseCase - find attempt replay: %w", err)
	} else if replay != nil {
		result, changed, replayErr := reconcileGoldenFailure(
			*replay, command.Scope, command.CommandID, GoldenFailureRouteReplay, digest,
		)
		return result, changed, false, replayErr
	}
	authority, err := u.repository.LoadGoldenFailureAuthority(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenFailureReplayUseCase - load authority: %w", err)
	}
	if authority.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	if !authority.Expectation().Equal(command.Expected) {
		return nil, false, false, ErrGoldenFailureAuthorityConflict
	}
	if authority.Current != nil || authority.Classification.Exhausted {
		return nil, false, false, ErrGoldenFailureRouteConflict
	}
	if failedAt.Before(authority.Active.StartedAt) {
		return nil, false, false, goldenFailureError("failure time precedes start")
	}
	record, err := buildGoldenFailureReplayRecord(authority, command, digest, failedAt)
	if err != nil {
		return nil, false, false, err
	}
	return commitGoldenFailure(ctx, u.repository, authority.Expectation(), record)
}

func buildGoldenFailureReplayRecord(
	authority GoldenFailureAuthority,
	command GoldenFailureReplayCommand,
	digest [sha256.Size]byte,
	failedAt time.Time,
) (GoldenFailureRecord, error) {
	base, err := buildGoldenFailureBase(
		authority, command.CommandID, command.FailureID, command.ClosedWaveRevisionID,
		GoldenFailureRouteReplay, digest, failedAt,
	)
	if err != nil {
		return GoldenFailureRecord{}, err
	}
	replacement, ids, err := buildGoldenFailureReplacement(authority, base.Group, command, failedAt)
	if err != nil {
		return GoldenFailureRecord{}, err
	}
	base.Replacement = &replacement
	base.NewIdentityIDs = append(base.NewIdentityIDs, ids...)
	canonicalGoldenIDs(base.NewIdentityIDs)
	if !uniqueNonZeroUUIDs(base.NewIdentityIDs) || goldenFailureAliasesAuthority(authority, base.NewIdentityIDs) {
		return GoldenFailureRecord{}, goldenFailureError("failure identity is reused")
	}
	return sealGoldenFailureRecord(base)
}

func buildGoldenFailureBase(
	authority GoldenFailureAuthority,
	commandID uuid.UUID,
	failureID uuid.UUID,
	closedWaveRevisionID domain.ArenaWaveRevisionID,
	route GoldenFailureRoute,
	digest [sha256.Size]byte,
	failedAt time.Time,
) (GoldenFailureRecord, error) {
	if closedWaveRevisionID.IsZero() || closedWaveRevisionID == authority.Active.Wave.RevisionID {
		return GoldenFailureRecord{}, goldenFailureError("invalid completed Wave revision")
	}
	attempt := cloneGoldenAttempt(authority.Active.Attempt)
	attempt.State = domain.ArenaGoldenAttemptStateVoid
	attempt.FinishedAt = cloneGoldenTime(&failedAt)
	group := cloneGoldenStateGroup(authority.Active.Group)
	group.Attempts[len(group.Attempts)-1] = cloneGoldenAttempt(attempt)
	if _, err := domain.NewArenaGoldenGroup(group); err != nil {
		return GoldenFailureRecord{}, goldenFailureError("void group is invalid")
	}
	wave := cloneArenaWaveExecution(authority.Active.Wave)
	wave.State = domain.ArenaWaveStateCompleted
	wave.RevisionID = closedWaveRevisionID
	if err := wave.Validate(); err != nil {
		return GoldenFailureRecord{}, goldenFailureError("complete old Wave")
	}
	record := GoldenFailureRecord{
		ID: failureID, CommandID: commandID, CommandDigest: digest, Route: route,
		Scope: authority.Active.Scope, Expected: authority.Expectation(),
		Classification: authority.Classification.Snapshot(), FailedAssignment: authority.Active.Assignment.Snapshot(),
		FailedAt: failedAt, Attempt: attempt, Group: group, OldWave: wave,
		DiscardedSubmissions: authority.Submissions.Expectation(),
		DiscardedOrder:       authority.Submissions.ProvisionalOrder(),
		PriorPositions:       authority.Positions.Snapshot(), Positions: authority.Positions.Snapshot(),
		NewIdentityIDs: []uuid.UUID{commandID, failureID, closedWaveRevisionID.UUID()},
	}
	return record.Snapshot(), nil
}

func buildGoldenFailureReplacement(
	authority GoldenFailureAuthority,
	voidGroup domain.ArenaGoldenGroupState,
	command GoldenFailureReplayCommand,
	openedAt time.Time,
) (GoldenFailureReplacement, []uuid.UUID, error) {
	nextEdge := authority.Classification.NextEdge
	if nextEdge == nil {
		return GoldenFailureReplacement{}, nil, ErrGoldenFailureRouteConflict
	}
	participants := append([]uuid.UUID(nil), authority.Classification.ParticipantIDs...)
	private, err := buildGoldenFailurePrivateAssignments(command.PrivateAssignments, participants, *nextEdge)
	if err != nil {
		return GoldenFailureReplacement{}, nil, err
	}
	attempt := domain.ArenaGoldenAttempt{
		ID: command.NextAttemptID, GroupID: authority.Active.Scope.State.GroupID,
		GroupRevisionID: authority.Active.Scope.State.GroupRevisionID,
		AttemptNo:       authority.Active.Attempt.AttemptNo + 1, PreviousAttemptID: goldenUUID(authority.Active.Attempt.ID),
		State: domain.ArenaGoldenAttemptStatePlanned, ParticipantIDs: participants,
	}
	group := cloneGoldenStateGroup(voidGroup)
	group.Attempts = append(group.Attempts, cloneGoldenAttempt(attempt))
	if _, err := domain.NewArenaGoldenGroup(group); err != nil {
		return GoldenFailureReplacement{}, nil, goldenFailureError("replacement group is invalid")
	}
	assignment := GoldenReserveAttemptAssignment{
		ID: command.NextAssignmentID, RevisionID: command.NextAssignmentRevisionID,
		Scope: authority.Active.Scope.State, AttemptID: command.NextAttemptID,
		EdgeID: nextEdge.ID, EdgePosition: nextEdge.Position, ReservationID: nextEdge.ReservationID,
		SnapshotID: nextEdge.SnapshotID, TaskID: nextEdge.TaskID, ContentDigest: nextEdge.ContentDigest, Private: private,
	}
	payload, err := goldenReserveAssignmentPayload(assignment)
	if err != nil {
		return GoldenFailureReplacement{}, nil, goldenFailureError("encode reserve assignment")
	}
	assignment.PayloadDigest = sha256.Sum256(payload)
	deadline := openedAt.Add(goldenReadyWindowDuration)
	wave := domain.ArenaWave{
		ID: command.NextWaveID, TournamentID: authority.Active.Scope.State.TournamentID,
		RevisionID: command.NextWaveRevisionID, State: domain.ArenaWaveStatePlanned,
		Members: goldenWaveMembers(participants),
	}
	if err := wave.OpenReadyWindow(command.NextWindowID, command.NextWaveWindowRevisionID, openedAt, deadline); err != nil {
		return GoldenFailureReplacement{}, nil, goldenFailureError("open replacement ready window")
	}
	membership := GoldenWaveMembershipBinding{
		ID: command.NextMembershipID, RevisionID: command.NextMembershipRevisionID, Revision: 1,
		Source: authority.State.Membership, ParticipantIDs: append([]uuid.UUID(nil), participants...),
		PayloadDigest: goldenParticipantSetDigest(participants),
	}
	window := GoldenReadyWindow{
		ID: command.NextWindowID, RevisionID: command.NextWindowRevisionID, Revision: 1,
		AttemptID: attempt.ID, AttemptNo: attempt.AttemptNo, OpenedAt: openedAt, Deadline: deadline,
		State: GoldenReadyWindowOpen, ReadinessRevisionID: command.NextReadinessRevisionID, ReadinessRevision: 1,
		PresenceRevisionID: command.NextPresenceRevisionID, PresenceRevision: 1,
		BasePresentParticipantIDs: append([]uuid.UUID(nil), participants...),
		PresentParticipantIDs:     append([]uuid.UUID(nil), participants...),
		ReadinessDigest:           goldenParticipantSetDigest(nil), PresenceDigest: goldenParticipantSetDigest(participants),
	}
	replacement := GoldenFailureReplacement{
		Group: group, Attempt: attempt, Assignment: assignment, Membership: membership,
		Wave: wave, Window: window, OpenedAt: openedAt, Deadline: deadline,
	}
	if err := replacement.Validate(); err != nil {
		return GoldenFailureReplacement{}, nil, err
	}
	ids := []uuid.UUID{
		command.NextAttemptID, command.NextAssignmentID, command.NextAssignmentRevisionID,
		command.NextWaveID, command.NextWaveRevisionID.UUID(), command.NextWindowID,
		command.NextWaveWindowRevisionID.UUID(), command.NextWindowRevisionID,
		command.NextReadinessRevisionID, command.NextPresenceRevisionID,
		command.NextMembershipID, command.NextMembershipRevisionID,
	}
	for _, assignment := range command.PrivateAssignments {
		ids = append(ids, assignment.AssignmentID)
	}
	canonicalGoldenIDs(ids)
	return replacement.Snapshot(), ids, nil
}

func buildGoldenFailurePrivateAssignments(
	requested []GoldenPrivateAssignmentCommand,
	participants []uuid.UUID,
	edge GoldenFailureEdge,
) ([]GoldenPrivateAssignment, error) {
	if len(requested) != len(participants) || len(requested) > goldenIndividualParticipantLimit {
		return nil, goldenFailureError("private assignments do not cover current membership")
	}
	byParticipant := make(map[uuid.UUID]uuid.UUID, len(requested))
	for _, item := range requested {
		if item.ParticipantID == uuid.Nil || item.AssignmentID == uuid.Nil {
			return nil, goldenFailureError("invalid private assignment identity")
		}
		if _, duplicate := byParticipant[item.ParticipantID]; duplicate {
			return nil, goldenFailureError("private assignment participant is duplicated")
		}
		byParticipant[item.ParticipantID] = item.AssignmentID
	}
	private := make([]GoldenPrivateAssignment, len(participants))
	for index, participantID := range participants {
		assignmentID, found := byParticipant[participantID]
		if !found {
			return nil, goldenFailureError("private assignments do not cover current membership")
		}
		private[index] = GoldenPrivateAssignment{
			ID: assignmentID, ParticipantID: participantID, SnapshotID: edge.SnapshotID, ContentDigest: edge.ContentDigest,
		}
	}
	return private, nil
}

func validGoldenFailureReplayCommand(command GoldenFailureReplayCommand) bool {
	if !command.Scope.IsValid() || command.CommandID == uuid.Nil || command.FailureID == uuid.Nil ||
		command.Expected.Scope != command.Scope || command.ClosedWaveRevisionID.IsZero() ||
		len(command.PrivateAssignments) < 2 || len(command.PrivateAssignments) > goldenIndividualParticipantLimit {
		return false
	}
	ids := []uuid.UUID{
		command.CommandID, command.FailureID, command.ClosedWaveRevisionID.UUID(), command.NextAttemptID,
		command.NextAssignmentID, command.NextAssignmentRevisionID, command.NextWaveID,
		command.NextWaveRevisionID.UUID(), command.NextWindowID, command.NextWaveWindowRevisionID.UUID(),
		command.NextWindowRevisionID, command.NextReadinessRevisionID, command.NextPresenceRevisionID,
		command.NextMembershipID, command.NextMembershipRevisionID,
	}
	for _, assignment := range command.PrivateAssignments {
		ids = append(ids, assignment.AssignmentID)
	}
	return uniqueNonZeroUUIDs(ids)
}

func commitGoldenFailure(
	ctx context.Context,
	repository GoldenFailureRepository,
	expected GoldenFailureExpectation,
	record GoldenFailureRecord,
) (*GoldenFailureRecord, bool, bool, error) {
	commit := GoldenFailureCommit{
		Expected: expected, Route: record.Route,
		NewIdentityIDs: append([]uuid.UUID(nil), record.NewIdentityIDs...), Record: record.Snapshot(),
	}
	committed, changed, err := repository.CommitGoldenFailure(ctx, commit)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("golden failure - commit route: %w", err)
	}
	if committed == nil || committed.Validate() != nil || committed.CommandID != record.CommandID ||
		committed.CommandDigest != record.CommandDigest || committed.Scope != record.Scope ||
		committed.Route != record.Route ||
		(changed && committed.PayloadDigest != record.PayloadDigest) {
		return nil, false, false, domain.ErrInternal
	}
	clone := committed.Snapshot()
	return &clone, changed, false, nil
}

func reconcileGoldenFailure(
	record GoldenFailureRecord,
	scope GoldenSubmissionScope,
	commandID uuid.UUID,
	route GoldenFailureRoute,
	digest [sha256.Size]byte,
) (*GoldenFailureRecord, bool, error) {
	if record.Validate() != nil {
		return nil, false, domain.ErrInternal
	}
	if record.CommandID != commandID {
		return nil, false, domain.ErrInternal
	}
	if record.Scope != scope || record.Route != route || record.CommandDigest != digest {
		return nil, false, ErrGoldenFailureCommandReuse
	}
	clone := record.Snapshot()
	return &clone, false, nil
}

func goldenFailureAliasesAuthority(authority GoldenFailureAuthority, identities []uuid.UUID) bool {
	reserved := goldenExecutionIdentitySetFromExpectation(authority.Active.Expectation)
	roles := goldenCoreIdentityRoles(authority.State)
	roles = append(roles, goldenPlanIdentityRoles(authority.State)...)
	roles = append(roles, goldenWindowIdentityRoles(authority.State)...)
	roles = append(roles, goldenTransitionIdentityRoles(authority.State)...)
	for _, role := range roles {
		reserved[role.value] = struct{}{}
	}
	values := []uuid.UUID{
		authority.Active.Scope.State.TournamentID, authority.Active.Scope.State.GroupID,
		authority.Active.Scope.State.GroupRevisionID.UUID(), authority.Active.Scope.AttemptID,
		authority.Active.Scope.WaveID, authority.Active.Scope.AssignmentID,
		authority.Active.Scope.SnapshotID, authority.Active.Scope.TaskID,
		authority.Submissions.RevisionID, authority.Positions.RevisionID, authority.SwissPoints.RevisionID,
		authority.Plan.PlanID, authority.Plan.PlanRevisionID,
		authority.Classification.FailedEdge.ID, authority.Classification.FailedEdge.ReservationID,
		authority.Classification.FailedEdge.SnapshotID, authority.Classification.FailedEdge.TaskID,
	}
	for _, group := range authority.Plan.Groups {
		for _, edge := range group.Edges {
			values = append(values, edge.ID, edge.ReservationID, edge.Snapshot.SnapshotID, edge.Snapshot.TaskID)
		}
	}
	if authority.Classification.NextEdge != nil {
		values = append(values, authority.Classification.NextEdge.ID, authority.Classification.NextEdge.ReservationID,
			authority.Classification.NextEdge.SnapshotID, authority.Classification.NextEdge.TaskID)
	}
	for _, value := range values {
		if value != uuid.Nil {
			reserved[value] = struct{}{}
		}
	}
	for _, identity := range identities {
		if _, found := reserved[identity]; found {
			return true
		}
	}
	return false
}

func goldenExecutionIdentitySetFromExpectation(expectation GoldenWaveExecutionExpectation) map[uuid.UUID]struct{} {
	values := []uuid.UUID{
		expectation.RevisionID, expectation.AttemptID, expectation.WaveID, expectation.WaveRevisionID.UUID(),
		expectation.Window.WindowID, expectation.Window.RevisionID, expectation.Window.ReadinessRevisionID,
		expectation.Window.PresenceRevisionID, expectation.MembershipID, expectation.MembershipRevisionID,
		expectation.AssignmentID, expectation.AssignmentRevisionID,
	}
	result := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value != uuid.Nil {
			result[value] = struct{}{}
		}
	}
	return result
}

func sealGoldenFailureRecord(record GoldenFailureRecord) (GoldenFailureRecord, error) {
	record.PayloadDigest = [sha256.Size]byte{}
	payload, err := goldenFailureRecordPayload(record)
	if err != nil {
		return GoldenFailureRecord{}, goldenFailureError("encode failure receipt")
	}
	record.PayloadDigest = sha256.Sum256(payload)
	if err := record.Validate(); err != nil {
		return GoldenFailureRecord{}, err
	}
	return record.Snapshot(), nil
}

func goldenFailureClassificationPayload(classification GoldenFailureClassification) ([]byte, error) {
	clone := classification.Snapshot()
	clone.PayloadDigest = [sha256.Size]byte{}
	return goldenEncode(clone)
}

func goldenFailureRecordPayload(record GoldenFailureRecord) ([]byte, error) {
	clone := record.Snapshot()
	clone.PayloadDigest = [sha256.Size]byte{}
	return goldenEncode(clone)
}

func goldenFailureReplayCommandDigest(command GoldenFailureReplayCommand) [sha256.Size]byte {
	payload, _ := goldenEncode(command)
	return sha256.Sum256(payload)
}

func goldenFailureError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenFailure, message)
}
