package game

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func cloneDraftExecution(current draft.Execution) draft.Execution {
	return draft.CloneExecution(current)
}

func draftExpectation(current draft.Execution) draft.RevisionExpectation {
	return draft.Expectation(current)
}

func advanceDraftRevision(
	current *draft.Execution,
	revisionID uuid.UUID,
	commandID uuid.UUID,
	serviceEpoch uuid.UUID,
) {
	draft.AdvanceRevision(current, revisionID, commandID, serviceEpoch)
}
