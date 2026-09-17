package model

import (
	"github.com/google/uuid"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

// NormalPauseGraphAttempts is the bounded retry count for optimistic pause commits.
const NormalPauseGraphAttempts = normalPauseGraphAttempts

// MaxFrozenPauseDuration is the maximum amount of time a deadline can remain frozen.
const MaxFrozenPauseDuration = maxFrozenPauseDuration

// AllowsNormalPause reports whether a reason can create a normal pause record.
func AllowsNormalPause(reason PauseReason) bool {
	return reason == PauseReasonOperator || reason == PauseReasonPlatform || reason == PauseReasonExecutionEpoch
}

// IsPauseDeadlineKindValid reports whether kind identifies a supported deadline.
func IsPauseDeadlineKindValid(kind PauseDeadlineKind) bool {
	return kind == PauseDeadlineReadyWindow || kind == PauseDeadlineGame || kind == PauseDeadlineDraft
}

// ValidDraftResultRevisionIdentity validates the result revision identity carried by a pause command.
func ValidDraftResultRevisionIdentity(
	expected *draftusecase.RevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultID uuid.UUID,
	commandID uuid.UUID,
	pauseID uuid.UUID,
	actorID uuid.UUID,
) bool {
	if expected == nil {
		return resultID == uuid.Nil && expectedPreviousRevisionID == uuid.Nil
	}
	return resultID != uuid.Nil && resultID != commandID && resultID != pauseID && resultID != actorID &&
		resultID != expected.RevisionID && resultID != expectedPreviousRevisionID && resultID != expected.ServiceEpoch &&
		validDraftPreviousRevision(*expected, expectedPreviousRevisionID)
}

func validDraftResultRevisionIdentity(
	expected *draftusecase.RevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultID uuid.UUID,
	commandID uuid.UUID,
	pauseID uuid.UUID,
	actorID uuid.UUID,
) bool {
	return ValidDraftResultRevisionIdentity(expected, expectedPreviousRevisionID, resultID, commandID, pauseID, actorID)
}

// ValidatePausePresence validates one durable pause presence row.
func ValidatePausePresence(value pausedomain.PausePresence) error {
	return validatePausePresence(value)
}

// ValidatePauseReconnect validates one durable reconnect interval.
func ValidatePauseReconnect(value pausedomain.PauseReconnectInterval) error {
	return validatePauseReconnect(value)
}

// ValidPauseReconnectCounter validates one durable reconnect counter.
func ValidPauseReconnectCounter(value pausedomain.PauseReconnectCounter) bool {
	return validPauseReconnectCounter(value)
}
