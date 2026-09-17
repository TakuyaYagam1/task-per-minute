package execution

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

func executionEncode(value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func executionError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenWaveExecution, message)
}

func executionValidStateScope(scope goldenstate.GoldenStateScope) bool {
	return goldenstate.ValidStateScope(scope)
}

func executionValidIdentitySet(values []uuid.UUID) bool {
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

func executionSortIDs(values []uuid.UUID) {
	sort.Slice(values, func(i, j int) bool { return bytes.Compare(values[i][:], values[j][:]) < 0 })
}

func executionIDsCanonical(values []uuid.UUID) bool {
	for index, value := range values {
		if value == uuid.Nil || (index > 0 && bytes.Compare(values[index-1][:], value[:]) >= 0) {
			return false
		}
	}
	return true
}

func executionIDsSubset(values, superset []uuid.UUID) bool {
	for _, value := range values {
		if !executionContainsID(superset, value) {
			return false
		}
	}
	return true
}

func executionEqualIDs(first, second []uuid.UUID) bool {
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

func executionContainsID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func executionCloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func executionCloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func CloneExecution(input domain.Wave) domain.Wave {
	clone := input
	clone.Members = append([]domain.WaveMember(nil), input.Members...)
	if input.ReadyWindow != nil {
		window := *input.ReadyWindow
		window.ConsumedAt = executionCloneTime(input.ReadyWindow.ConsumedAt)
		clone.ReadyWindow = &window
	}
	clone.StartedAt = executionCloneTime(input.StartedAt)
	clone.PausedAt = executionCloneTime(input.PausedAt)
	return clone
}

func CloneAttempt(input domain.GoldenAttempt) domain.GoldenAttempt {
	return goldenstate.CloneAttempt(input)
}

func CloneMembershipBinding(input GoldenWaveMembershipBinding) GoldenWaveMembershipBinding {
	return cloneMembershipBinding(input)
}

func CloneAttemptAssignment(input GoldenAttemptAssignment) GoldenAttemptAssignment {
	return cloneAttemptAssignment(input)
}

func CloneExecutionExpectation(input GoldenWaveExecutionExpectation) GoldenWaveExecutionExpectation {
	clone := input
	clone.Source = goldenstate.CloneExpectation(input.Source)
	return clone
}

func cloneWaveReceipts(input []GoldenWaveCommandReceipt) []GoldenWaveCommandReceipt {
	result := make([]GoldenWaveCommandReceipt, len(input))
	for index, receipt := range input {
		result[index] = receipt
		result[index].UnusedIdentityIDs = append([]uuid.UUID(nil), receipt.UnusedIdentityIDs...)
		result[index].Result.Source = goldenstate.CloneExpectation(receipt.Result.Source)
		if receipt.Expected != nil {
			expected := CloneExecutionExpectation(*receipt.Expected)
			result[index].Expected = &expected
		}
	}
	return result
}

func CloneWaveReceipts(input []GoldenWaveCommandReceipt) []GoldenWaveCommandReceipt {
	return cloneWaveReceipts(input)
}

func ExecutionGroupBindingDigest(
	group domain.GoldenGroupState,
	openingParticipationEstablished bool,
) ([sha256.Size]byte, error) {
	normalized := goldenstate.CloneGroup(group)
	normalized.ParticipationEstablished = openingParticipationEstablished
	if len(normalized.Attempts) > 0 {
		last := len(normalized.Attempts) - 1
		normalized.Attempts[last].State = domain.GoldenAttemptStateWaitingReady
		normalized.Attempts[last].StartedAt = nil
	}
	payload, err := executionEncode(normalized)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

func BuildMembers(participants []uuid.UUID) []domain.WaveMember {
	members := make([]domain.WaveMember, len(participants))
	for index, participantID := range participants {
		members[index] = domain.WaveMember{ParticipantID: participantID}
	}
	return members
}

func MemberIDs(wave domain.Wave) []uuid.UUID {
	result := make([]uuid.UUID, len(wave.Members))
	for index, member := range wave.Members {
		result[index] = member.ParticipantID
	}
	executionSortIDs(result)
	return result
}

func PrivateAssignmentParticipantIDs(assignments []GoldenPrivateAssignment) []uuid.UUID {
	result := make([]uuid.UUID, len(assignments))
	for index, assignment := range assignments {
		result[index] = assignment.ParticipantID
	}
	executionSortIDs(result)
	return result
}

func AssignmentDigest(assignment GoldenAttemptAssignment) [sha256.Size]byte {
	type document struct {
		ID            uuid.UUID
		RevisionID    uuid.UUID
		Revision      int64
		Scope         goldenstate.GoldenStateScope
		AttemptID     uuid.UUID
		WaveID        uuid.UUID
		MembershipID  uuid.UUID
		Plan          goldenstate.GoldenPlanStateBinding
		EdgeID        uuid.UUID
		ReservationID uuid.UUID
		Snapshot      domain.AssignmentTaskSnapshot
		ContentDigest [sha256.Size]byte
		Private       []GoldenPrivateAssignment
	}
	payload, _ := executionEncode(document{
		ID: assignment.ID, RevisionID: assignment.RevisionID, Revision: assignment.Revision,
		Scope: assignment.Scope, AttemptID: assignment.AttemptID, WaveID: assignment.WaveID,
		MembershipID: assignment.MembershipID, Plan: assignment.Plan, EdgeID: assignment.EdgeID,
		ReservationID: assignment.ReservationID, Snapshot: assignment.Snapshot,
		ContentDigest: assignment.ContentDigest, Private: assignment.Private,
	})
	return sha256.Sum256(payload)
}

func WavePayloadDigest(execution GoldenWaveExecution) [sha256.Size]byte {
	type document struct {
		Scope                           goldenstate.GoldenStateScope
		Source                          goldenstate.GoldenStateExpectation
		RevisionID                      uuid.UUID
		Revision                        int64
		PreviousRevisionID              *uuid.UUID
		Group                           domain.GoldenGroupState
		GroupBindingDigest              [sha256.Size]byte
		OpeningParticipationEstablished bool
		Attempt                         domain.GoldenAttempt
		Wave                            domain.Wave
		Membership                      GoldenWaveMembershipBinding
		Assignment                      GoldenAttemptAssignment
		Window                          goldenstate.GoldenReadyWindow
		OpenedAt                        time.Time
		Deadline                        time.Time
		ReceiptsDigest                  [sha256.Size]byte
		Start                           *GoldenStartRecord
	}
	payload, _ := executionEncode(document{
		Scope: execution.Scope, Source: execution.Source, RevisionID: execution.RevisionID,
		Revision: execution.Revision, PreviousRevisionID: execution.PreviousRevisionID,
		Group: execution.Group, GroupBindingDigest: execution.GroupBindingDigest,
		OpeningParticipationEstablished: execution.OpeningParticipationEstablished,
		Attempt:                         execution.Attempt, Wave: execution.Wave,
		Membership: execution.Membership, Assignment: execution.Assignment, Window: execution.Window,
		OpenedAt: execution.OpenedAt, Deadline: execution.Deadline,
		ReceiptsDigest: execution.ReceiptsDigest, Start: execution.Start,
	})
	return sha256.Sum256(payload)
}

func WaveReceiptsDigest(receipts []GoldenWaveCommandReceipt) ([sha256.Size]byte, error) {
	normalized := cloneWaveReceipts(receipts)
	if len(normalized) > 0 {
		normalized[len(normalized)-1].Result.PayloadDigest = [sha256.Size]byte{}
	}
	payload, err := executionEncode(normalized)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

func GoldenStartRecordDigest(start GoldenStartRecord) [sha256.Size]byte {
	clone := start
	clone.PayloadDigest = [sha256.Size]byte{}
	payload, _ := executionEncode(clone)
	return sha256.Sum256(payload)
}

func cloneStartRecord(input *GoldenStartRecord) *GoldenStartRecord {
	if input == nil {
		return nil
	}
	clone := *input
	clone.ExpectedState = goldenstate.CloneExpectation(input.ExpectedState)
	clone.ExpectedExecution = CloneExecutionExpectation(input.ExpectedExecution)
	clone.Assignments = append([]GoldenStartedAssignment(nil), input.Assignments...)
	return &clone
}

func CloneStartRecord(input *GoldenStartRecord) *GoldenStartRecord {
	return cloneStartRecord(input)
}
