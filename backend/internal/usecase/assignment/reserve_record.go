package assignment

import (
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (r ReserveAssignmentRecord) Validate() error {
	authority := ReserveAssignmentAuthority{
		Scope: r.Scope, Revisions: r.Revisions, CurrentSnapshotID: r.FromSnapshotID,
		RequiredCategory:        r.RequiredCategory,
		ParticipantIDs:          append([]uuid.UUID(nil), r.ParticipantIDs...),
		ParticipantReservations: cloneExactNormalParticipantReservations(r.ParticipantReservations),
		Pool:                    domain.CloneTaskPool(r.Pool),
		ReceiptHistory:          append([]TaskReceiptRef(nil), r.ReceiptHistory...),
		CandidateHealth:         r.CandidateHealth,
		CandidateSnapshot:       cloneTaskSnapshot(r.Snapshot),
		CandidateContentDigest:  r.ContentDigest,
		CategoryExhaustion:      cloneReserveCategoryExhaustion(r.CategoryExhaustion),
	}
	canonical, decision, err := normalizeReserveAssignmentAuthority(r.Scope, authority)
	if err != nil || !decision.Eligible {
		return reserveAssignmentError("invalid retained authority")
	}
	if canonical.CurrentSnapshotID == canonical.CandidateSnapshot.SnapshotID ||
		r.Evidence.TaskID != canonical.CandidateSnapshot.TaskID ||
		r.Evidence.Version != canonical.CandidateSnapshot.Version ||
		r.Evidence.SnapshotID != canonical.CandidateSnapshot.SnapshotID ||
		!r.PromotedAt.Equal(r.Evidence.DecidedAt) {
		return reserveAssignmentError("evidence does not match the promoted reserve")
	}
	if err := validateReserveCategoryTransition(canonical); err != nil {
		return err
	}
	if err := validateReserveAssignmentEvidence(r.Evidence); err != nil {
		return err
	}
	want, err := reserveAssignmentProofDigest(r)
	if err != nil || r.ProofDigest == [sha256.Size]byte{} || want != r.ProofDigest {
		return reserveAssignmentError("proof digest does not match")
	}
	return nil
}

func buildReserveAssignmentRecord(
	command ReserveAssignmentCommand,
	authority ReserveAssignmentAuthority,
) (ReserveAssignmentRecord, error) {
	canonical, decision, err := normalizeReserveAssignmentAuthority(command.Scope, authority)
	if err != nil {
		return ReserveAssignmentRecord{}, err
	}
	if !decision.Eligible {
		return ReserveAssignmentRecord{}, fmt.Errorf("%w: %v", ErrTaskIneligible, decision.Reasons)
	}
	if canonical.CurrentSnapshotID != command.ExpectedSnapshotID {
		return ReserveAssignmentRecord{}, reserveAssignmentError("active snapshot changed")
	}
	if err := validateReserveCategoryTransition(canonical); err != nil {
		return ReserveAssignmentRecord{}, err
	}
	evidence, err := newReserveAssignmentEvidence(command, canonical.CandidateSnapshot)
	if err != nil {
		return ReserveAssignmentRecord{}, err
	}
	record := ReserveAssignmentRecord{
		Scope: canonical.Scope, Revisions: canonical.Revisions,
		FromSnapshotID: canonical.CurrentSnapshotID, RequiredCategory: canonical.RequiredCategory,
		ParticipantIDs:          append([]uuid.UUID(nil), canonical.ParticipantIDs...),
		ParticipantReservations: cloneExactNormalParticipantReservations(canonical.ParticipantReservations),
		Pool:                    domain.CloneTaskPool(canonical.Pool),
		ReceiptHistory:          append([]TaskReceiptRef(nil), canonical.ReceiptHistory...),
		CandidateHealth:         canonical.CandidateHealth,
		Snapshot:                cloneTaskSnapshot(canonical.CandidateSnapshot),
		ContentDigest:           canonical.CandidateContentDigest,
		CategoryExhaustion:      cloneReserveCategoryExhaustion(canonical.CategoryExhaustion),
		Evidence:                evidence,
		PromotedAt:              command.PromotedAt,
	}
	record.ProofDigest, err = reserveAssignmentProofDigest(record)
	if err != nil {
		return ReserveAssignmentRecord{}, err
	}
	if err := record.Validate(); err != nil {
		return ReserveAssignmentRecord{}, err
	}
	return cloneReserveAssignmentRecord(record), nil
}

// BuildReserveAssignmentRecord builds the reserve part of a larger atomic
// tournament transition without committing it independently.
func BuildReserveAssignmentRecord(
	command ReserveAssignmentCommand,
	authority ReserveAssignmentAuthority,
) (ReserveAssignmentRecord, error) {
	return buildReserveAssignmentRecord(command, authority)
}
