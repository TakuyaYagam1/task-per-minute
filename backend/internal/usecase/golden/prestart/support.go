package golden

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenexecution "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/execution"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
	goldenwave "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/wave"
)

func Encode(value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
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

func SortIDs(values []uuid.UUID) {
	goldenwave.SortIDs(values)
}

func IDsCanonical(values []uuid.UUID) bool {
	return goldenwave.IDsCanonical(values)
}

func EqualIDs(first, second []uuid.UUID) bool {
	return goldenwave.EqualIDs(first, second)
}

func DigestIDs(values []uuid.UUID) [sha256.Size]byte {
	return goldenwave.DigestIDs(values)
}

func UUIDPointer(value uuid.UUID) *uuid.UUID {
	return goldenwave.UUIDPointer(value)
}

func CloneGroup(group domain.GoldenGroupState) domain.GoldenGroupState {
	return goldenstate.CloneGroup(group)
}

func CloneAttempt(attempt domain.GoldenAttempt) domain.GoldenAttempt {
	return goldenstate.CloneAttempt(attempt)
}

func CloneReadyWindow(window GoldenReadyWindow) GoldenReadyWindow {
	return goldenstate.CloneReadyWindow(window)
}

func CloneExpectation(input GoldenStateExpectation) GoldenStateExpectation {
	return goldenstate.CloneExpectation(input)
}

func ValidStateScope(scope GoldenStateScope) bool {
	return goldenstate.ValidStateScope(scope)
}

func ValidateReadyWindowIdentity(window GoldenReadyWindow) error {
	return goldenstate.ValidateReadyWindowIdentity(window)
}

func ValidateReadyWindowParticipants(window GoldenReadyWindow) error {
	return goldenexecution.ValidateReadyWindowParticipants(window)
}

func MembershipRevisionsEqual(first, second goldenstate.GoldenMembershipRevision) bool {
	return goldenstate.MembershipRevisionsEqual(first, second)
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

func PrivateAssignmentParticipantIDs(assignments []goldenexecution.GoldenPrivateAssignment) []uuid.UUID {
	return goldenexecution.PrivateAssignmentParticipantIDs(assignments)
}

func ExecutionGroupBindingDigest(
	group domain.GoldenGroupState,
	openingParticipationEstablished bool,
) ([sha256.Size]byte, error) {
	return goldenexecution.ExecutionGroupBindingDigest(group, openingParticipationEstablished)
}

func BuildMembers(participants []uuid.UUID) []domain.WaveMember {
	return goldenexecution.BuildMembers(participants)
}

func SealExecution(execution *GoldenWaveExecution) error {
	return goldenwave.SealExecution(execution)
}

func retainedGoldenRecordIdentityIDs(record RetainedGoldenPrestartRecord) []uuid.UUID {
	identities := []uuid.UUID{record.CommandID, record.RevisionID}
	if record.State == RetainedGoldenPrestartPaused {
		identities = append(identities, record.SessionID)
	} else if record.FreshWindow != nil {
		identities = append(identities,
			record.FreshExecutionRevisionID, record.FreshWaveRevisionID.UUID(), record.FreshWindow.ID,
			record.FreshWaveWindowRevisionID.UUID(), record.FreshWindow.RevisionID,
			record.FreshWindow.ReadinessRevisionID, record.FreshWindow.PresenceRevisionID,
		)
	}
	SortIDs(identities)
	return identities
}

func sealRetainedGoldenPrestartRecord(record RetainedGoldenPrestartRecord) (RetainedGoldenPrestartRecord, error) {
	record.PayloadDigest = [sha256.Size]byte{}
	payload, err := goldenPrestartRecordPayload(record)
	if err != nil {
		return RetainedGoldenPrestartRecord{}, goldenPrestartError("encode retained pre-start receipt")
	}
	record.PayloadDigest = sha256.Sum256(payload)
	if err := record.Validate(); err != nil {
		return RetainedGoldenPrestartRecord{}, err
	}
	return record.Snapshot(), nil
}

func goldenPrestartAuthorizationPayload(authorization GoldenPrestartOperatorAuthorization) ([]byte, error) {
	authorization.PayloadDigest = [sha256.Size]byte{}
	return Encode(authorization)
}

func goldenPrestartRecordPayload(record RetainedGoldenPrestartRecord) ([]byte, error) {
	clone := record.Snapshot()
	clone.PayloadDigest = [sha256.Size]byte{}
	return Encode(clone)
}

func retainedGoldenPauseCommandDigest(command RetainedGoldenPrestartPauseCommand) [sha256.Size]byte {
	payload, _ := Encode(command)
	return sha256.Sum256(payload)
}

func retainedGoldenResumeCommandDigest(command RetainedGoldenPrestartResumeCommand) [sha256.Size]byte {
	payload, _ := Encode(command)
	return sha256.Sum256(payload)
}

func goldenPrestartError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenPrestartPause, message)
}
