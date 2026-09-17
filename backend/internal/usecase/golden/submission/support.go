package submission

import (
	"bytes"
	"encoding/gob"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/execution"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

const goldenSubmissionCommitAttempts = commitAttempts

func Encode(value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func submissionError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenSubmission, message)
}

func ValidStateScope(scope GoldenStateScope) bool {
	return goldenstate.ValidStateScope(scope)
}

func ValidIdentitySet(values []uuid.UUID) bool {
	return validSubmissionIdentitySet(values)
}

func validSubmissionIdentitySet(values []uuid.UUID) bool {
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

func ContainsID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func FindMember(members []domain.GoldenMember, participantID uuid.UUID) (domain.GoldenMember, bool) {
	for _, member := range members {
		if member.ParticipantID == participantID {
			return member, true
		}
	}
	return domain.GoldenMember{}, false
}

func UUIDPointer(value uuid.UUID) *uuid.UUID {
	return &value
}

func CloneExecutionExpectation(input GoldenWaveExecutionExpectation) GoldenWaveExecutionExpectation {
	return goldenexecution.CloneExecutionExpectation(input)
}

func RetainedIdentitySet(execution GoldenWaveExecution) map[uuid.UUID]struct{} {
	return goldenexecution.RetainedIdentitySet(execution)
}

func cloneSubmissionUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func submissionCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	return cloneSubmissionUUIDPointer(value)
}

func validSubmissionRevisionPredecessor(current uuid.UUID, revision int64, previous *uuid.UUID) bool {
	return revision == 1 && previous == nil ||
		revision > 1 && previous != nil && *previous != uuid.Nil && *previous != current
}

func submissionValidGoldenRevisionPredecessor(current uuid.UUID, revision int64, previous *uuid.UUID) bool {
	return validSubmissionRevisionPredecessor(current, revision, previous)
}

func buildSubmissionLedger(ledger GoldenSubmissionLedger) (GoldenSubmissionLedger, error) {
	return buildGoldenSubmissionLedger(ledger)
}

func submissionLedgerPayload(ledger GoldenSubmissionLedger) ([]byte, error) {
	return goldenSubmissionLedgerPayload(ledger)
}
