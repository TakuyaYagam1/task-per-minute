package assignment

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func newReserveAssignmentEvidence(
	command ReserveAssignmentCommand,
	snapshot domain.AssignmentTaskSnapshot,
) (ReserveAssignmentEvidence, error) {
	evidence := ReserveAssignmentEvidence{
		ID: command.EvidenceID, Mode: command.Mode, OperatorID: command.OperatorID,
		Reason: command.Reason, TaskID: snapshot.TaskID, Version: snapshot.Version,
		SnapshotID: snapshot.SnapshotID, DecidedAt: command.PromotedAt,
	}
	digest, err := reserveAssignmentEvidenceDigest(evidence)
	if err != nil {
		return ReserveAssignmentEvidence{}, reserveAssignmentError("evidence digest: %v", err)
	}
	evidence.Digest = digest
	return evidence, nil
}

func validateReserveAssignmentEvidence(evidence ReserveAssignmentEvidence) error {
	if evidence.ID == uuid.Nil || evidence.TaskID == uuid.Nil || evidence.Version < 1 ||
		evidence.SnapshotID == uuid.Nil || evidence.DecidedAt.IsZero() ||
		evidence.DecidedAt.Location() != time.UTC {
		return reserveAssignmentError("invalid decision evidence identity")
	}
	if err := validateReserveAssignmentEvidenceMode(evidence); err != nil {
		return err
	}
	want, err := reserveAssignmentEvidenceDigest(evidence)
	if err != nil || evidence.Digest == [sha256.Size]byte{} || want != evidence.Digest {
		return reserveAssignmentError("decision evidence digest does not match")
	}
	return nil
}

func validateReserveAssignmentEvidenceMode(evidence ReserveAssignmentEvidence) error {
	switch evidence.Mode {
	case ReserveAssignmentModeAutomatic:
		if evidence.OperatorID != uuid.Nil || evidence.Reason != "" {
			return reserveAssignmentError("automatic evidence cannot claim an operator")
		}
	case ReserveAssignmentModeOperator:
		if evidence.OperatorID == uuid.Nil || strings.TrimSpace(evidence.Reason) == "" {
			return reserveAssignmentError("operator evidence requires actor and reason")
		}
	default:
		return reserveAssignmentError("unknown reserve assignment mode")
	}
	return nil
}

func validateReserveAssignmentCommand(command ReserveAssignmentCommand) error {
	if !validReserveAssignmentScope(command.Scope) || command.ExpectedSnapshotID == uuid.Nil ||
		command.EvidenceID == uuid.Nil || command.PromotedAt.IsZero() ||
		command.PromotedAt.Location() != time.UTC {
		return reserveAssignmentError("invalid command identity or timestamp")
	}
	switch command.Mode {
	case ReserveAssignmentModeAutomatic:
		if command.OperatorID != uuid.Nil || command.Reason != "" {
			return reserveAssignmentError("automatic promotion cannot claim an operator")
		}
	case ReserveAssignmentModeOperator:
		if command.OperatorID == uuid.Nil || command.Reason == "" {
			return reserveAssignmentError("operator promotion requires actor and reason")
		}
	default:
		return reserveAssignmentError("unknown promotion mode")
	}
	return nil
}

func reserveAssignmentEvidenceDigest(evidence ReserveAssignmentEvidence) ([sha256.Size]byte, error) {
	document := struct {
		ID         uuid.UUID             `json:"id"`
		Mode       ReserveAssignmentMode `json:"mode"`
		OperatorID uuid.UUID             `json:"operator_id"`
		Reason     string                `json:"reason"`
		TaskID     uuid.UUID             `json:"task_id"`
		Version    int                   `json:"version"`
		SnapshotID uuid.UUID             `json:"snapshot_id"`
		DecidedAt  time.Time             `json:"decided_at"`
	}{
		ID: evidence.ID, Mode: evidence.Mode, OperatorID: evidence.OperatorID,
		Reason: evidence.Reason, TaskID: evidence.TaskID, Version: evidence.Version,
		SnapshotID: evidence.SnapshotID, DecidedAt: evidence.DecidedAt,
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

func reserveAssignmentProofDigest(record ReserveAssignmentRecord) ([sha256.Size]byte, error) {
	document := map[string]any{
		"scope":                    record.Scope,
		"revisions":                record.Revisions,
		"from_snapshot_id":         record.FromSnapshotID,
		"required_category":        record.RequiredCategory,
		"participant_ids":          record.ParticipantIDs,
		"participant_reservations": record.ParticipantReservations,
		"pool":                     record.Pool,
		"receipt_history":          record.ReceiptHistory,
		"candidate_health":         record.CandidateHealth,
		"snapshot":                 record.Snapshot,
		"content_digest":           record.ContentDigest,
		"category_exhaustion":      record.CategoryExhaustion,
		"evidence":                 record.Evidence,
		"promoted_at":              record.PromotedAt,
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return [sha256.Size]byte{}, reserveAssignmentError("encode proof: %v", err)
	}
	return sha256.Sum256(payload), nil
}

func reserveAssignmentError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidReserveAssignment, fmt.Sprintf(format, arguments...))
}

// ValidateReserveAssignmentCommand validates a reserve command before a
// parent coordinator combines it with a larger atomic transition.
func ValidateReserveAssignmentCommand(command ReserveAssignmentCommand) error {
	return validateReserveAssignmentCommand(command)
}
