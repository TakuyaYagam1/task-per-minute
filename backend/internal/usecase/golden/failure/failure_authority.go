package golden

import (
	"crypto/sha256"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

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
	Plan           ExactPlan
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
		Scope: a.Active.Scope, State: a.State.Expectation(), Execution: CloneExecutionExpectation(a.Active.Expectation),
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
		if !ContainsID(authority.Active.ParticipantIDs, submission.ParticipantID) ||
			submission.AssignmentDigest != authority.Active.Expectation.AssignmentDigest {
			return goldenFailureError("current submissions do not belong to the active assignment")
		}
	}
	for _, receipt := range authority.Submissions.Receipts {
		if !ContainsID(authority.Active.ParticipantIDs, receipt.ParticipantID) {
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
		len(authority.Active.ParticipantIDs) <= domain.TournamentMaxParticipants &&
		len(authority.Active.Group.Members) <= domain.TournamentMaxParticipants &&
		len(authority.Active.Group.Attempts) <= goldenFailureAttemptLimit &&
		len(authority.State.Group.Members) <= domain.TournamentMaxParticipants &&
		len(authority.State.Group.Attempts) <= goldenFailureAttemptLimit &&
		len(authority.Submissions.Submissions) <= domain.TournamentMaxParticipants &&
		len(authority.Submissions.Receipts) <= goldenFailureReceiptLimit &&
		len(authority.Positions.Positions) <= domain.TournamentMaxParticipants &&
		len(authority.Positions.Attempts) <= goldenFailureAttemptLimit
}

func goldenFailurePlanCollectionsWithinBounds(plan ExactPlan) bool {
	if len(plan.Groups) == 0 || len(plan.Groups) > domain.TournamentMaxParticipants ||
		len(plan.Authority.Groups) > domain.TournamentMaxParticipants ||
		len(plan.Authority.ParticipantReservations) > domain.TournamentMaxParticipants {
		return false
	}
	for _, group := range plan.Groups {
		if len(group.ParticipantIDs) < 2 || len(group.ParticipantIDs) > domain.TournamentMaxParticipants ||
			len(group.Edges) != goldenFailureAttemptLimit {
			return false
		}
	}
	for _, group := range plan.Authority.Groups {
		if len(group.ActiveParticipantIDs) < 2 || len(group.ActiveParticipantIDs) > domain.TournamentMaxParticipants {
			return false
		}
	}
	return true
}

func validateGoldenFailurePriorPositions(
	group domain.GoldenGroupState,
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

func goldenFailureGroupMemberSet(group domain.GoldenGroupState) map[uuid.UUID]struct{} {
	members := make(map[uuid.UUID]struct{}, len(group.Members))
	for _, member := range group.Members {
		members[member.ParticipantID] = struct{}{}
	}
	return members
}

func goldenFailureAttemptSet(group domain.GoldenGroupState) map[uuid.UUID]domain.GoldenAttempt {
	attempts := make(map[uuid.UUID]domain.GoldenAttempt, len(group.Attempts))
	for _, attempt := range group.Attempts {
		attempts[attempt.ID] = attempt
	}
	return attempts
}

func validateGoldenFailureAttemptOrdering(
	ordering GoldenAttemptOrderingEvidence,
	members map[uuid.UUID]struct{},
	attempts map[uuid.UUID]domain.GoldenAttempt,
) error {
	attempt, found := attempts[ordering.AttemptID]
	if !found || attempt.State != domain.GoldenAttemptStateCompleted || attempt.AttemptNo != ordering.AttemptNo {
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
	attempts map[uuid.UUID]domain.GoldenAttempt,
) error {
	attempt, found := attempts[position.AttemptID]
	_, member := members[position.ParticipantID]
	if !member || !found || attempt.State != domain.GoldenAttemptStateCompleted ||
		attempt.AttemptNo != position.AttemptNo {
		return goldenFailureError("committed position does not belong to a completed group attempt")
	}
	return nil
}
