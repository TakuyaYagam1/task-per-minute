package draft

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func cloneDraftExecution(draft Execution) Execution {
	cloned := draft
	cloned.Pool = append([]domain.Category(nil), draft.Pool...)
	cloned.CurrentActorID = cloneDraftUUID(draft.CurrentActorID)
	cloned.CurrentAction = cloneDraftActionType(draft.CurrentAction)
	cloned.AbsoluteDeadline = cloneDraftTime(draft.AbsoluteDeadline)
	cloned.LegalCategories = append([]domain.Category(nil), draft.LegalCategories...)
	cloned.Actions = cloneDraftActionRecords(draft.Actions)
	cloned.SelectedCategories = append([]domain.Category(nil), draft.SelectedCategories...)
	cloned.FirstActorDecision = cloneDraftDecisionEvidenceValue(draft.FirstActorDecision)
	if draft.Recovery != nil {
		recovery := *draft.Recovery
		cloned.Recovery = &recovery
	}
	if draft.Transition != nil {
		transition := *draft.Transition
		cloned.Transition = &transition
	}
	return cloned
}

func CloneExecution(draft Execution) Execution {
	return cloneDraftExecution(draft)
}

func cloneDraftActionRecords(actions []ActionRecord) []ActionRecord {
	cloned := append([]ActionRecord(nil), actions...)
	for index := range cloned {
		cloned[index].DecisionEvidence = cloneDraftDecisionEvidence(actions[index].DecisionEvidence)
	}
	return cloned
}

func cloneDraftDecisionEvidence(evidence *domain.DecisionEvidence) *domain.DecisionEvidence {
	if evidence == nil {
		return nil
	}
	cloned := cloneDraftDecisionEvidenceValue(*evidence)
	return &cloned
}

func cloneDraftDecisionEvidenceValue(evidence domain.DecisionEvidence) domain.DecisionEvidence {
	cloned := evidence
	cloned.NormalizedInputs = append([]string(nil), evidence.NormalizedInputs...)
	cloned.Result = append([]string(nil), evidence.Result...)
	return cloned
}

func cloneDraftUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneDraftActionType(value *domain.DraftActionType) *domain.DraftActionType {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneDraftTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func draftExecutionError(message string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidExecution, fmt.Sprintf(message, args...))
}
