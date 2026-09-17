package golden

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/execution"
	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

func Encode(value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
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

func IDsSubset(values, superset []uuid.UUID) bool {
	for _, value := range values {
		if !ContainsID(superset, value) {
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

func ContainsID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func WithoutID(values []uuid.UUID, target uuid.UUID) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func DigestIDs(values []uuid.UUID) [sha256.Size]byte {
	canonical := append([]uuid.UUID(nil), values...)
	SortIDs(canonical)
	payload, _ := Encode(canonical)
	return sha256.Sum256(payload)
}

func UUIDPointer(value uuid.UUID) *uuid.UUID {
	return &value
}

func CloneGroup(group domain.GoldenGroupState) domain.GoldenGroupState {
	return goldenstate.CloneGroup(group)
}

func CloneTaskSnapshot(snapshot domain.AssignmentTaskSnapshot) domain.AssignmentTaskSnapshot {
	return goldenplan.CloneTaskSnapshot(snapshot)
}

func BuildMembers(participants []uuid.UUID) []domain.WaveMember {
	return goldenexecution.BuildMembers(participants)
}

func PrivateAssignmentParticipantIDs(assignments []GoldenPrivateAssignment) []uuid.UUID {
	return goldenexecution.PrivateAssignmentParticipantIDs(assignments)
}

func ExecutionGroupBindingDigest(
	group domain.GoldenGroupState,
	openingParticipationEstablished bool,
) ([sha256.Size]byte, error) {
	return goldenexecution.ExecutionGroupBindingDigest(group, openingParticipationEstablished)
}

func goldenAssignmentDigest(assignment GoldenAttemptAssignment) [sha256.Size]byte {
	return goldenexecution.AssignmentDigest(assignment)
}

func goldenWavePayloadDigest(execution GoldenWaveExecution) [sha256.Size]byte {
	return goldenexecution.WavePayloadDigest(execution)
}

func goldenWaveReceiptsDigest(receipts []GoldenWaveCommandReceipt) ([sha256.Size]byte, error) {
	return goldenexecution.WaveReceiptsDigest(receipts)
}

func goldenStartRecordDigest(start GoldenStartRecord) [sha256.Size]byte {
	return goldenexecution.GoldenStartRecordDigest(start)
}

func cloneGoldenAttempt(input domain.GoldenAttempt) domain.GoldenAttempt {
	return goldenexecution.CloneAttempt(input)
}

func cloneGoldenExecutionExpectation(input *GoldenWaveExecutionExpectation) *GoldenWaveExecutionExpectation {
	if input == nil {
		return nil
	}
	clone := goldenexecution.CloneExecutionExpectation(*input)
	return &clone
}

func goldenExecutionHasReceipt(execution GoldenWaveExecution, expected GoldenWaveCommandReceipt) bool {
	return goldenexecution.ExecutionHasReceipt(execution, expected)
}

func goldenExecutionReceiptByCommand(
	execution GoldenWaveExecution,
	commandID uuid.UUID,
) (GoldenWaveCommandReceipt, bool) {
	return goldenexecution.ExecutionReceiptByCommand(execution, commandID)
}

func waveCloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func RetainedIdentitySet(execution GoldenWaveExecution) map[uuid.UUID]struct{} {
	return goldenexecution.RetainedIdentitySet(execution)
}

func CloneExpectation(input GoldenStateExpectation) GoldenStateExpectation {
	return goldenstate.CloneExpectation(input)
}

func ValidStateScope(scope GoldenStateScope) bool {
	return goldenstate.ValidStateScope(scope)
}

func ValidateReadyWindowParticipants(window GoldenReadyWindow) error {
	return goldenexecution.ValidateReadyWindowParticipants(window)
}

func ReadyMemberIDs(wave domain.Wave) []uuid.UUID {
	return goldenexecution.ReadyMemberIDs(wave)
}

func ValidateFreshIdentityIDs(state GoldenState, identities ...uuid.UUID) error {
	if err := validateWaveFreshIdentityIDs(state, identities...); err != nil {
		return goldenWaveError("identity ownership: %v", err)
	}
	reserved := make(map[uuid.UUID]struct{})
	for _, identity := range goldenplan.AuthorityIdentityIDs(state.ExactPlan.Authority) {
		if identity != uuid.Nil {
			reserved[identity] = struct{}{}
		}
	}
	for _, reservation := range state.ExactPlan.Authority.ExistingTaskReservations {
		reserved[reservation.PlanID] = struct{}{}
		reserved[reservation.PlanRevisionID] = struct{}{}
	}
	for _, identity := range identities {
		if _, exists := reserved[identity]; exists {
			return goldenWaveError("identity aliases exact plan authority")
		}
	}
	return nil
}

func validateWaveFreshIdentityIDs(state GoldenState, candidates ...uuid.UUID) error {
	reserved := make(map[uuid.UUID]struct{})
	for _, identity := range goldenstate.RetainedIdentityValues(state) {
		reserved[identity] = struct{}{}
	}
	seen := make(map[uuid.UUID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == uuid.Nil {
			continue
		}
		if _, exists := reserved[candidate]; exists {
			return goldenWaveError("command identity aliases retained state authority")
		}
		if _, exists := seen[candidate]; exists {
			return goldenWaveError("new identities alias each other")
		}
		seen[candidate] = struct{}{}
	}
	return nil
}
