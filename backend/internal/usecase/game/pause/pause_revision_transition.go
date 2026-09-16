package pause

import (
	"math"
	"time"

	"github.com/google/uuid"

	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func pausedDraftRevisionMatches(
	current *draftusecase.Execution,
	expected *draftusecase.RevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	reason PauseReason,
	pausedAt time.Time,
) bool {
	if current == nil || expected == nil {
		return absentDraftRevisionMatches(current, expected, resultRevisionID)
	}
	if current.ServiceEpoch != expected.ServiceEpoch {
		return false
	}
	switch current.State {
	case draftusecase.ExecutionStatePaused:
		return pausedDraftMutationMatches(current, expected, expectedPreviousRevisionID,
			resultRevisionID, commandID, actorID, reason, pausedAt)
	case draftusecase.ExecutionStateRecoveryRequired, draftusecase.ExecutionStateCompleted, draftusecase.ExecutionStateSuperseded:
		return unchangedDraftRevisionMatches(current, expected, expectedPreviousRevisionID, resultRevisionID)
	case draftusecase.ExecutionStateActive:
		return false
	default:
		return false
	}
}

func absentDraftRevisionMatches(current *draftusecase.Execution, expected *draftusecase.RevisionExpectation, resultRevisionID uuid.UUID) bool {
	return current == nil && expected == nil && resultRevisionID == uuid.Nil
}

func pausedDraftMutationMatches(
	current *draftusecase.Execution,
	expected *draftusecase.RevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultRevisionID uuid.UUID,
	commandID uuid.UUID,
	actorID uuid.UUID,
	reason PauseReason,
	pausedAt time.Time,
) bool {
	lineageMatches := nextRevisionMatches(current.Revision, expected.Revision) &&
		current.PreviousRevisionID == expected.RevisionID && current.RevisionID == resultRevisionID
	identityMatches := current.CommandID == commandID && current.ID != resultRevisionID &&
		resultRevisionID != expectedPreviousRevisionID
	return lineageMatches && identityMatches && draftTransitionMatches(current.Transition,
		draftusecase.TransitionPause, actorID, string(reason), pausedAt)
}

func unchangedDraftRevisionMatches(
	current *draftusecase.Execution,
	expected *draftusecase.RevisionExpectation,
	expectedPreviousRevisionID uuid.UUID,
	resultRevisionID uuid.UUID,
) bool {
	return current.Revision == expected.Revision && current.RevisionID == expected.RevisionID &&
		current.PreviousRevisionID == expectedPreviousRevisionID && current.ID != resultRevisionID
}

func draftTransitionMatches(
	transition *draftusecase.TransitionEvidence,
	operation draftusecase.TransitionOperation,
	actorID uuid.UUID,
	reason string,
	occurredAt time.Time,
) bool {
	return transition != nil && transition.Operation == operation && transition.ActorID == actorID &&
		transition.Reason == reason && transition.OccurredAt.Equal(occurredAt)
}

func childRevision(values []PauseChildRevision, id uuid.UUID) (int64, bool) {
	for _, value := range values {
		if value.ID == id {
			return value.Revision, true
		}
	}
	return 0, false
}

func nextRevisionMatches(current, expected int64) bool {
	return expected < math.MaxInt64 && current == expected+1
}

func reconcileNormalPause(record NormalPauseRecord, command NormalPauseCommand) (*NormalPauseRecord, error) {
	if validateNormalPauseRecord(record) != nil || record.Scope != command.Scope || record.CommandID != command.CommandID ||
		record.PauseID != command.PauseID || record.ActorID != command.ActorID || record.Reason != command.Reason ||
		record.DraftResultRevisionID != command.DraftResultRevisionID ||
		!pauseGraphRevisionsEqual(record.Expected, command.Expected) {
		return nil, ErrNormalPauseCommandReuse
	}
	clone := cloneNormalPauseRecord(record)
	return &clone, nil
}
