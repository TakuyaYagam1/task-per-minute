package execution

import (
	"crypto/sha256"

	"github.com/google/uuid"

	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

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
	if !validAttemptAssignmentEvidenceHeader(e) {
		return executionError("invalid sanitized assignment evidence")
	}
	participants, privateIDs, validPrivate := attemptAssignmentPrivateEvidence(e)
	if !validPrivate {
		return executionError("invalid private assignment evidence")
	}
	ids = append(ids, privateIDs...)
	executionSortIDs(participants)
	executionSortIDs(privateIDs)
	if !executionValidIdentitySet(ids) || !executionIDsCanonical(participants) || !executionIDsCanonical(privateIDs) {
		return executionError("assignment evidence repeats a participant or identity")
	}
	payload, err := attemptAssignmentEvidencePayload(e)
	if err != nil || e.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != e.PayloadDigest {
		return executionError("assignment evidence digest changed")
	}
	return nil
}

func validAttemptAssignmentEvidenceHeader(e GoldenAttemptAssignmentEvidence) bool {
	return executionValidStateScope(e.Scope) && e.Revision == 1 && e.Plan.GroupID == e.Scope.GroupID &&
		e.Plan.GroupRevisionID == e.Scope.GroupRevisionID && e.ContentDigest != [sha256.Size]byte{} &&
		e.ExecutionPayloadDigest != [sha256.Size]byte{} && len(e.Private) >= 2
}

func attemptAssignmentPrivateEvidence(
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

func BuildAttemptAssignmentEvidence(
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
	payload, err := attemptAssignmentEvidencePayload(evidence)
	if err != nil {
		return GoldenAttemptAssignmentEvidence{}, executionError("encode assignment evidence")
	}
	evidence.PayloadDigest = sha256.Sum256(payload)
	if err := evidence.Validate(); err != nil {
		return GoldenAttemptAssignmentEvidence{}, err
	}
	return evidence.Snapshot(), nil
}

func attemptAssignmentEvidencePayload(evidence GoldenAttemptAssignmentEvidence) ([]byte, error) {
	return executionEncode(struct {
		ID                     uuid.UUID
		RevisionID             uuid.UUID
		Revision               int64
		Scope                  goldenstate.GoldenStateScope
		AttemptID              uuid.UUID
		WaveID                 uuid.UUID
		MembershipID           uuid.UUID
		Plan                   goldenstate.GoldenPlanStateBinding
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

func cloneAttemptAssignment(input GoldenAttemptAssignment) GoldenAttemptAssignment {
	clone := input
	clone.Snapshot = goldenplan.CloneTaskSnapshot(input.Snapshot)
	clone.Private = append([]GoldenPrivateAssignment(nil), input.Private...)
	return clone
}

func cloneMembershipBinding(input GoldenWaveMembershipBinding) GoldenWaveMembershipBinding {
	clone := input
	clone.Source.PreviousRevisionID = executionCloneUUID(input.Source.PreviousRevisionID)
	clone.ParticipantIDs = append([]uuid.UUID(nil), input.ParticipantIDs...)
	return clone
}
