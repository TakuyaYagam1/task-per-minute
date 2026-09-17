package golden

import (
	"crypto/sha256"
	"math"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"

	"github.com/google/uuid"
)

func buildGoldenReadinessSuccessor(
	execution GoldenWaveExecution,
	operation goldenReadinessOperation,
	digest [sha256.Size]byte,
	occurredAt time.Time,
) (GoldenWaveExecution, error) {
	if execution.Revision == math.MaxInt64 {
		return GoldenWaveExecution{}, ErrGoldenWaveRevisionOverflow
	}
	next := execution.Snapshot()
	expected := execution.Expectation()
	next.PreviousRevisionID = UUIDPointer(next.RevisionID)
	next.RevisionID = operation.nextExecutionRevisionID
	next.Revision++
	var err error
	switch operation.kind {
	case GoldenWaveCommandReady:
		err = applyGoldenMarkReady(&next, operation, occurredAt)
	case GoldenWaveCommandDisconnected:
		err = applyGoldenDisconnect(&next, operation)
	case GoldenWaveCommandReconnected:
		err = applyGoldenReconnect(&next, operation)
	case GoldenWaveCommandOpened, GoldenWaveCommandStarted:
		err = goldenWaveError("readiness operation uses a non-readiness command kind")
	default:
		err = goldenWaveError("unknown readiness operation")
	}
	if err != nil {
		return GoldenWaveExecution{}, err
	}
	next.Window.ReadinessDigest = DigestIDs(next.Window.ReadyParticipantIDs)
	next.Window.PresenceDigest = DigestIDs(next.Window.PresentParticipantIDs)
	next.Receipts = append(next.Receipts, GoldenWaveCommandReceipt{
		CommandID: operation.commandID, Scope: operation.scope, Kind: operation.kind,
		CommandDigest: digest, Expected: &expected, Result: next.Expectation(),
		OccurredAt: occurredAt, ParticipantID: operation.participantID,
		UnusedIdentityIDs: goldenReadinessUnusedIdentities(operation, expected, next.Expectation()),
	})
	if err := SealExecution(&next); err != nil {
		return GoldenWaveExecution{}, err
	}
	if err := next.Validate(); err != nil {
		return GoldenWaveExecution{}, err
	}
	return next.Snapshot(), nil
}

func applyGoldenMarkReady(
	execution *GoldenWaveExecution,
	operation goldenReadinessOperation,
	occurredAt time.Time,
) error {
	if !ContainsID(execution.Window.PresentParticipantIDs, operation.participantID) {
		return ErrGoldenWaveAuthorityConflict
	}
	if ContainsID(execution.Window.ReadyParticipantIDs, operation.participantID) {
		return nil
	}
	if execution.Window.Revision == math.MaxInt64 || execution.Window.ReadinessRevision == math.MaxInt64 {
		return ErrGoldenWaveRevisionOverflow
	}
	changed, err := execution.Wave.MarkReady(execution.Window.ID, operation.participantID, occurredAt)
	if err != nil || !changed {
		return goldenWaveError("mark Wave ready: %v", err)
	}
	advanceGoldenWindowState(&execution.Window, operation.nextWindowRevisionID)
	advanceGoldenReadiness(&execution.Window, operation.nextReadinessRevisionID)
	execution.Window.ReadyParticipantIDs = append(execution.Window.ReadyParticipantIDs, operation.participantID)
	SortIDs(execution.Window.ReadyParticipantIDs)
	return nil
}

func applyGoldenDisconnect(execution *GoldenWaveExecution, operation goldenReadinessOperation) error {
	if !ContainsID(execution.Window.PresentParticipantIDs, operation.participantID) {
		return nil
	}
	if !ContainsID(execution.Window.ReadyParticipantIDs, operation.participantID) {
		return nil
	}
	if execution.Window.Revision == math.MaxInt64 || execution.Window.PresenceRevision == math.MaxInt64 {
		return ErrGoldenWaveRevisionOverflow
	}
	advanceGoldenWindowState(&execution.Window, operation.nextWindowRevisionID)
	execution.Window.PresencePreviousRevisionID = UUIDPointer(execution.Window.PresenceRevisionID)
	execution.Window.PresenceRevisionID = operation.nextPresenceRevisionID
	execution.Window.PresenceRevision++
	execution.Window.PresentParticipantIDs = WithoutID(
		execution.Window.PresentParticipantIDs,
		operation.participantID,
	)
	if execution.Window.ReadinessRevision == math.MaxInt64 {
		return ErrGoldenWaveRevisionOverflow
	}
	advanceGoldenReadiness(&execution.Window, operation.nextReadinessRevisionID)
	execution.Window.ReadyParticipantIDs = WithoutID(execution.Window.ReadyParticipantIDs, operation.participantID)
	for index := range execution.Wave.Members {
		if execution.Wave.Members[index].ParticipantID == operation.participantID {
			execution.Wave.Members[index].Ready = false
			execution.Wave.State = domain.WaveStateReadyWindowOpen
			return nil
		}
	}
	return domain.ErrAssignmentParticipant
}

func applyGoldenReconnect(execution *GoldenWaveExecution, operation goldenReadinessOperation) error {
	if ContainsID(execution.Window.PresentParticipantIDs, operation.participantID) {
		return nil
	}
	if ContainsID(execution.Window.ReadyParticipantIDs, operation.participantID) {
		return goldenWaveError("absent participant retained readiness")
	}
	if execution.Window.Revision == math.MaxInt64 || execution.Window.PresenceRevision == math.MaxInt64 {
		return ErrGoldenWaveRevisionOverflow
	}
	advanceGoldenWindowState(&execution.Window, operation.nextWindowRevisionID)
	execution.Window.PresencePreviousRevisionID = UUIDPointer(execution.Window.PresenceRevisionID)
	execution.Window.PresenceRevisionID = operation.nextPresenceRevisionID
	execution.Window.PresenceRevision++
	execution.Window.PresentParticipantIDs = append(execution.Window.PresentParticipantIDs, operation.participantID)
	SortIDs(execution.Window.PresentParticipantIDs)
	return nil
}

func advanceGoldenWindowState(window *GoldenReadyWindow, revisionID uuid.UUID) {
	window.PreviousRevisionID = UUIDPointer(window.RevisionID)
	window.RevisionID = revisionID
	window.Revision++
}

func advanceGoldenReadiness(window *GoldenReadyWindow, revisionID uuid.UUID) {
	window.ReadinessPreviousRevisionID = UUIDPointer(window.ReadinessRevisionID)
	window.ReadinessRevisionID = revisionID
	window.ReadinessRevision++
}
