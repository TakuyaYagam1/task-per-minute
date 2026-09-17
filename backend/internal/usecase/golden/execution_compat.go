package golden

import (
	"crypto/sha256"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/execution"
)

type GoldenWaveCommandKind = goldenexecution.GoldenWaveCommandKind
type GoldenWaveMembershipBinding = goldenexecution.GoldenWaveMembershipBinding
type GoldenPrivateAssignment = goldenexecution.GoldenPrivateAssignment
type GoldenAttemptAssignment = goldenexecution.GoldenAttemptAssignment
type GoldenAttemptAssignmentEvidence = goldenexecution.GoldenAttemptAssignmentEvidence
type GoldenWaveExecutionExpectation = goldenexecution.GoldenWaveExecutionExpectation
type GoldenWaveCommandReceipt = goldenexecution.GoldenWaveCommandReceipt
type GoldenWaveCommandReplay = goldenexecution.GoldenWaveCommandReplay
type GoldenWaveExecution = goldenexecution.GoldenWaveExecution
type GoldenStartAuthority = goldenexecution.GoldenStartAuthority
type GoldenStartedAssignment = goldenexecution.GoldenStartedAssignment
type GoldenStartRecord = goldenexecution.GoldenStartRecord

var ErrInvalidGoldenWaveExecution = goldenexecution.ErrInvalidGoldenWaveExecution

const (
	GoldenWaveCommandOpened       = goldenexecution.GoldenWaveCommandOpened
	GoldenWaveCommandReady        = goldenexecution.GoldenWaveCommandReady
	GoldenWaveCommandDisconnected = goldenexecution.GoldenWaveCommandDisconnected
	GoldenWaveCommandReconnected  = goldenexecution.GoldenWaveCommandReconnected
	GoldenWaveCommandStarted      = goldenexecution.GoldenWaveCommandStarted
)

func CloneExecution(input domain.Wave) domain.Wave {
	return goldenexecution.CloneExecution(input)
}

func CloneMembershipBinding(input GoldenWaveMembershipBinding) GoldenWaveMembershipBinding {
	return goldenexecution.CloneMembershipBinding(input)
}

func CloneAttemptAssignment(input GoldenAttemptAssignment) GoldenAttemptAssignment {
	return goldenexecution.CloneAttemptAssignment(input)
}

func CloneExecutionExpectation(input GoldenWaveExecutionExpectation) GoldenWaveExecutionExpectation {
	return goldenexecution.CloneExecutionExpectation(input)
}

func IdentitySetFromExpectation(expectation GoldenWaveExecutionExpectation) map[uuid.UUID]struct{} {
	return goldenexecution.IdentitySetFromExpectation(expectation)
}

func RetainedIdentitySet(execution GoldenWaveExecution) map[uuid.UUID]struct{} {
	return goldenexecution.RetainedIdentitySet(execution)
}

func BuildAttemptAssignmentEvidence(
	assignment GoldenAttemptAssignment,
) (GoldenAttemptAssignmentEvidence, error) {
	return goldenexecution.BuildAttemptAssignmentEvidence(assignment)
}

func BuildMembers(participants []uuid.UUID) []domain.WaveMember {
	return goldenexecution.BuildMembers(participants)
}

func MemberIDs(wave domain.Wave) []uuid.UUID {
	return goldenexecution.MemberIDs(wave)
}

func PrivateAssignmentParticipantIDs(assignments []GoldenPrivateAssignment) []uuid.UUID {
	return goldenexecution.PrivateAssignmentParticipantIDs(assignments)
}

func ValidateReadyWindowParticipants(window GoldenReadyWindow) error {
	return goldenexecution.ValidateReadyWindowParticipants(window)
}

func ReadyMemberIDs(wave domain.Wave) []uuid.UUID {
	return goldenexecution.ReadyMemberIDs(wave)
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

func cloneGoldenWaveReceipts(input []GoldenWaveCommandReceipt) []GoldenWaveCommandReceipt {
	return goldenexecution.CloneWaveReceipts(input)
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

func goldenWaveReceiptsEqual(first, second GoldenWaveCommandReceipt) bool {
	return goldenexecution.WaveReceiptsEqual(first, second)
}

func cloneGoldenStartRecord(input *GoldenStartRecord) *GoldenStartRecord {
	return goldenexecution.CloneStartRecord(input)
}

func waveCloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func waveCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
