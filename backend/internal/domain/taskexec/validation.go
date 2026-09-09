package taskexec

import (
	"crypto/subtle"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type FlagValidationInput struct {
	ParticipantID uuid.UUID
	Snapshot      domain.AssignmentTaskSnapshot
	SubmittedFlag string
}

func MatchesFlag(expected, submitted string) bool {
	return subtle.ConstantTimeCompare([]byte(expected), []byte(submitted)) == 1
}

func ValidateSnapshotFlag(input FlagValidationInput) (bool, error) {
	if input.ParticipantID == uuid.Nil {
		return false, fmt.Errorf("%w: missing participant identity", domain.ErrValidation)
	}
	if err := input.Snapshot.Validate(); err != nil {
		return false, fmt.Errorf("%w: invalid task snapshot: %w", domain.ErrValidation, err)
	}
	return MatchesFlag(input.Snapshot.Flag, input.SubmittedFlag), nil
}
