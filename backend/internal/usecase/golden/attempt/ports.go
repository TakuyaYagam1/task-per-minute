package golden

import (
	"bytes"
	"context"
	"encoding/gob"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/execution"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
	goldensubmission "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/submission"
)

type GoldenStateScope = goldenstate.GoldenStateScope
type GoldenPlanStateBinding = goldenstate.GoldenPlanStateBinding

type GoldenSubmissionScope = goldensubmission.GoldenSubmissionScope
type GoldenSubmissionLedgerExpectation = goldensubmission.GoldenSubmissionLedgerExpectation
type GoldenSubmissionLedger = goldensubmission.GoldenSubmissionLedger

type GoldenPrivateAssignment = goldenexecution.GoldenPrivateAssignment
type GoldenAttemptAssignmentEvidence = goldenexecution.GoldenAttemptAssignmentEvidence
type GoldenWaveExecution = goldenexecution.GoldenWaveExecution
type GoldenWaveExecutionExpectation = goldenexecution.GoldenWaveExecutionExpectation

type AttemptClock interface {
	Now() time.Time
}

// AttemptRepository atomically commits the terminal attempt, submissions and positions.
type AttemptRepository interface {
	FindGoldenAttemptCommit(ctx context.Context, tournamentID, commandID uuid.UUID) (*GoldenAttemptCommitRecord, error)
	LoadGoldenAttemptCommitAuthority(ctx context.Context, scope GoldenSubmissionScope) (GoldenAttemptCommitAuthority, error)
	CommitGoldenAttempt(ctx context.Context, record GoldenAttemptCommitRecord) (*GoldenAttemptCommitRecord, bool, error)
}

func Encode(value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func ValidStateScope(scope GoldenStateScope) bool {
	return goldenstate.ValidStateScope(scope)
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

func ContainsID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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

func BuildAttemptAssignmentEvidence(input goldenexecution.GoldenAttemptAssignment) (GoldenAttemptAssignmentEvidence, error) {
	return goldenexecution.BuildAttemptAssignmentEvidence(input)
}

func PrivateAssignmentParticipantIDs(assignments []GoldenPrivateAssignment) []uuid.UUID {
	return goldenexecution.PrivateAssignmentParticipantIDs(assignments)
}

func RetainedIdentitySet(execution GoldenWaveExecution) map[uuid.UUID]struct{} {
	return goldenexecution.RetainedIdentitySet(execution)
}

func ScopeMatchesExecution(scope GoldenSubmissionScope, execution GoldenWaveExecution) bool {
	return goldensubmission.ScopeMatchesExecution(scope, execution)
}

func ReserveLedgerIdentities(reserved map[uuid.UUID]struct{}, ledger GoldenSubmissionLedger) {
	goldensubmission.ReserveLedgerIdentities(reserved, ledger)
}

func attemptCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func attemptCloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func attemptValidRevisionPredecessor(current uuid.UUID, revision int64, previous *uuid.UUID) bool {
	return revision == 1 && previous == nil ||
		revision > 1 && previous != nil && *previous != uuid.Nil && *previous != current
}
