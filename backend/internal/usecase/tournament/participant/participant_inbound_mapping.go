package participant

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
)

func readinessEventView(event readiness.ReadinessEvent) usecase.ReadinessEvent {
	return usecase.ReadinessEvent{
		CommandID: event.CommandID, WaveID: event.Scope.WaveID, WindowID: event.Scope.WindowID,
		ParticipantID: event.ParticipantID, Type: usecase.ReadinessEventType(event.Type), OccurredAt: event.OccurredAt,
	}
}

func draftExecutionView(execution draftusecase.Execution) usecase.DraftExecutionView {
	view := usecase.DraftExecutionView{
		ID: execution.ID, SeriesID: execution.SeriesID, Format: execution.Format,
		FirstParticipantID: execution.FirstParticipantID, SecondParticipantID: execution.SecondParticipantID,
		Pool: append([]domain.Category(nil), execution.Pool...), State: usecase.DraftExecutionState(execution.State),
		RevisionID: execution.RevisionID, PreviousRevisionID: execution.PreviousRevisionID, Revision: execution.Revision,
		CommandID: execution.CommandID, ServiceEpoch: execution.ServiceEpoch, Turn: execution.Turn,
		CurrentActorID: cloneUUID(execution.CurrentActorID), CurrentAction: cloneDraftAction(execution.CurrentAction),
		TurnDeadline: execution.TurnDeadline, AbsoluteDeadline: participantCloneTime(execution.AbsoluteDeadline),
		PausedRemaining: execution.PausedRemaining, LegalCategories: append([]domain.Category(nil), execution.LegalCategories...),
		SelectedCategories: append([]domain.Category(nil), execution.SelectedCategories...), FirstActorDecision: cloneDecisionEvidence(execution.FirstActorDecision),
		Actions: make([]usecase.DraftActionRecordView, len(execution.Actions)),
	}
	if execution.Recovery != nil {
		view.Recovery = &usecase.DraftRecoveryEvidenceView{Policy: usecase.DraftRecoveryPolicy(execution.Recovery.Policy), Reason: usecase.DraftRecoveryReason(execution.Recovery.Reason), PreviousState: usecase.DraftExecutionState(execution.Recovery.PreviousState), PreviousServiceEpoch: execution.Recovery.PreviousServiceEpoch, CurrentServiceEpoch: execution.Recovery.CurrentServiceEpoch, PreviousDeadline: execution.Recovery.PreviousDeadline, RecordedAt: execution.Recovery.RecordedAt, ActorID: execution.Recovery.ActorID, Note: execution.Recovery.Note}
	}
	if execution.Transition != nil {
		view.Transition = &usecase.DraftTransitionEvidenceView{Operation: usecase.DraftTransitionOperation(execution.Transition.Operation), ActorID: execution.Transition.ActorID, Reason: execution.Transition.Reason, OccurredAt: execution.Transition.OccurredAt}
	}
	for index, action := range execution.Actions {
		view.Actions[index] = usecase.DraftActionRecordView{ID: action.ID, ResultRevisionID: action.ResultRevisionID, CommandID: action.CommandID, Turn: action.Turn, ActorID: action.ActorID, Action: action.Action, Category: action.Category, ScheduledDeadline: action.ScheduledDeadline, OccurredAt: action.OccurredAt, Automatic: action.Automatic, DecisionEvidence: cloneDecisionEvidencePointer(action.DecisionEvidence)}
	}
	return view
}

func cloneDraftAction(value *domain.DraftActionType) *domain.DraftActionType {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
func cloneDecisionEvidence(value domain.DecisionEvidence) domain.DecisionEvidence {
	cloned := value
	cloned.NormalizedInputs = append([]string(nil), value.NormalizedInputs...)
	cloned.Result = append([]string(nil), value.Result...)
	return cloned
}
func cloneDecisionEvidencePointer(value *domain.DecisionEvidence) *domain.DecisionEvidence {
	if value == nil {
		return nil
	}
	cloned := cloneDecisionEvidence(*value)
	return &cloned
}

func cloneDraftExecutionView(value usecase.DraftExecutionView) usecase.DraftExecutionView {
	cloned := value
	cloned.Pool = append([]domain.Category(nil), value.Pool...)
	cloned.LegalCategories = append([]domain.Category(nil), value.LegalCategories...)
	cloned.SelectedCategories = append([]domain.Category(nil), value.SelectedCategories...)
	cloned.CurrentActorID = cloneUUID(value.CurrentActorID)
	cloned.CurrentAction = cloneDraftAction(value.CurrentAction)
	cloned.AbsoluteDeadline = participantCloneTime(value.AbsoluteDeadline)
	cloned.FirstActorDecision = cloneDecisionEvidence(value.FirstActorDecision)
	cloned.Actions = append([]usecase.DraftActionRecordView(nil), value.Actions...)
	for index := range cloned.Actions {
		cloned.Actions[index].DecisionEvidence = cloneDecisionEvidencePointer(value.Actions[index].DecisionEvidence)
	}
	if value.Recovery != nil {
		recovery := *value.Recovery
		cloned.Recovery = &recovery
	}
	if value.Transition != nil {
		transition := *value.Transition
		cloned.Transition = &transition
	}
	return cloned
}
