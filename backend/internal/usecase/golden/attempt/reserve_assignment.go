package golden

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var ErrInvalidGoldenReserveAssignment = errors.New("invalid Golden reserve assignment")

type GoldenReserveAttemptAssignment struct {
	ID            uuid.UUID
	RevisionID    uuid.UUID
	Scope         GoldenStateScope
	AttemptID     uuid.UUID
	EdgeID        uuid.UUID
	EdgePosition  int
	ReservationID uuid.UUID
	SnapshotID    uuid.UUID
	TaskID        uuid.UUID
	ContentDigest [sha256.Size]byte
	Private       []GoldenPrivateAssignment
	PayloadDigest [sha256.Size]byte
}

func (a GoldenReserveAttemptAssignment) Snapshot() GoldenReserveAttemptAssignment {
	clone := a
	clone.Private = append([]GoldenPrivateAssignment(nil), a.Private...)
	return clone
}

func (a GoldenReserveAttemptAssignment) Seal() (GoldenReserveAttemptAssignment, error) {
	clone := a.Snapshot()
	payload, err := reserveAssignmentPayload(clone)
	if err != nil {
		return GoldenReserveAttemptAssignment{}, reserveAssignmentError("encode payload")
	}
	clone.PayloadDigest = sha256.Sum256(payload)
	if err := clone.Validate(); err != nil {
		return GoldenReserveAttemptAssignment{}, err
	}
	return clone, nil
}

func (a GoldenReserveAttemptAssignment) Validate() error {
	if !validReserveAssignmentIdentity(a) {
		return reserveAssignmentError("invalid identity")
	}
	if !ValidIdentitySet(reserveAssignmentIDs(a)) {
		return reserveAssignmentError("identity is aliased")
	}
	if err := validateReservePrivateAssignments(a); err != nil {
		return err
	}
	payload, err := reserveAssignmentPayload(a)
	if err != nil || a.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != a.PayloadDigest {
		return reserveAssignmentError("payload digest changed")
	}
	return nil
}

func validReserveAssignmentIdentity(a GoldenReserveAttemptAssignment) bool {
	return a.ID != uuid.Nil && a.RevisionID != uuid.Nil && ValidStateScope(a.Scope) &&
		a.AttemptID != uuid.Nil && a.EdgeID != uuid.Nil && a.EdgePosition > 0 &&
		a.ReservationID != uuid.Nil && a.SnapshotID != uuid.Nil && a.TaskID != uuid.Nil &&
		a.ContentDigest != [sha256.Size]byte{} && len(a.Private) >= 2
}

func reserveAssignmentIDs(a GoldenReserveAttemptAssignment) []uuid.UUID {
	return []uuid.UUID{a.ID, a.RevisionID, a.AttemptID, a.EdgeID, a.ReservationID, a.SnapshotID, a.TaskID}
}

func validateReservePrivateAssignments(a GoldenReserveAttemptAssignment) error {
	participants := make(map[uuid.UUID]struct{}, len(a.Private))
	assignments := make(map[uuid.UUID]struct{}, len(a.Private))
	for _, private := range a.Private {
		if private.ID == uuid.Nil || private.ParticipantID == uuid.Nil || private.SnapshotID != a.SnapshotID ||
			private.ContentDigest != a.ContentDigest {
			return reserveAssignmentError("invalid private assignment")
		}
		if _, duplicate := participants[private.ParticipantID]; duplicate {
			return reserveAssignmentError("repeated participant")
		}
		if _, duplicate := assignments[private.ID]; duplicate {
			return reserveAssignmentError("private identity is reused")
		}
		participants[private.ParticipantID] = struct{}{}
		assignments[private.ID] = struct{}{}
	}
	return nil
}

func reserveAssignmentPayload(assignment GoldenReserveAttemptAssignment) ([]byte, error) {
	return Encode(struct {
		ID            uuid.UUID
		RevisionID    uuid.UUID
		Scope         GoldenStateScope
		AttemptID     uuid.UUID
		EdgeID        uuid.UUID
		EdgePosition  int
		ReservationID uuid.UUID
		SnapshotID    uuid.UUID
		TaskID        uuid.UUID
		ContentDigest [sha256.Size]byte
		Private       []GoldenPrivateAssignment
	}{
		ID: assignment.ID, RevisionID: assignment.RevisionID, Scope: assignment.Scope,
		AttemptID: assignment.AttemptID, EdgeID: assignment.EdgeID, EdgePosition: assignment.EdgePosition,
		ReservationID: assignment.ReservationID, SnapshotID: assignment.SnapshotID,
		TaskID: assignment.TaskID, ContentDigest: assignment.ContentDigest, Private: assignment.Private,
	})
}

func reserveAssignmentError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenReserveAssignment, message)
}
