package draft

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func applyDraftAction(
	current Execution,
	command PlayerActionCommand,
	occurredAt time.Time,
	automatic bool,
	decision *domain.DecisionEvidence,
) (Execution, error) {
	draft, err := current.DomainDraft()
	if err != nil {
		return Execution{}, err
	}
	nextDeadline := occurredAt.Add(TurnDuration)
	if draftFinalTurn(draft) {
		nextDeadline = time.Time{}
	}
	action := ActionCommand{
		ExpectedTurn: command.ExpectedTurn, ActorID: command.ActorID, Action: command.Action,
		Category: command.Category, OccurredAt: occurredAt, NextDeadline: nextDeadline,
	}
	var nextDomain domain.Draft
	switch draft.Format {
	case domain.SeriesFormatBO1:
		nextDomain, err = ApplyBO1Action(draft, action)
	case domain.SeriesFormatBO3:
		nextDomain, err = ApplyBO3FinalAction(draft, action)
	default:
		err = domain.ErrValidation
	}
	if err != nil {
		return Execution{}, err
	}

	nextState := ExecutionStateActive
	if nextDomain.State == domain.DraftStateCompleted {
		nextState = ExecutionStateCompleted
	}
	nextActions := append(cloneDraftActionRecords(current.Actions), ActionRecord{
		ID: command.ActionID, ResultRevisionID: command.ResultRevisionID, CommandID: command.CommandID,
		Turn: command.ExpectedTurn, ActorID: command.ActorID, Action: command.Action, Category: command.Category,
		ScheduledDeadline: current.TurnDeadline, OccurredAt: occurredAt, Automatic: automatic,
		DecisionEvidence: cloneDraftDecisionEvidence(decision),
	})
	next, err := draftExecutionFromDomain(
		nextDomain,
		nextState,
		command.ResultRevisionID,
		current.RevisionID,
		current.Revision+1,
		command.CommandID,
		current.ServiceEpoch,
		current.FirstActorDecision,
		nextActions,
	)
	if err != nil {
		return Execution{}, err
	}
	return next, nil
}

func commitDraftPlayerAction(
	ctx context.Context,
	repository Repository,
	current Execution,
	next Execution,
	command PlayerActionCommand,
) (ActionResult, error) {
	committed, changed, err := repository.CommitDraftRevisions(
		ctx,
		draftExpectation(current),
		[]Execution{next},
	)
	if errors.Is(err, domain.ErrConflict) {
		return reconcileDraftActionConflict(ctx, repository, command)
	}
	if err != nil {
		return ActionResult{}, fmt.Errorf("ActionUseCase - commit action: %w", err)
	}
	if committed == nil {
		return reconcileDraftActionConflict(ctx, repository, command)
	}
	result, reconcileErr := reconcileDraftPlayerAction(*committed, command)
	if reconcileErr != nil {
		return ActionResult{}, domain.ErrInternal
	}
	result.Changed = changed
	return result, nil
}

func reconcileDraftActionConflict(
	ctx context.Context,
	repository Repository,
	command PlayerActionCommand,
) (ActionResult, error) {
	recorded, err := repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return ActionResult{}, fmt.Errorf("ActionUseCase - reconcile command: %w", err)
	}
	if recorded == nil {
		return ActionResult{}, ErrActionConflict
	}
	return reconcileDraftPlayerAction(*recorded, command)
}

func reconcileDraftPlayerAction(
	recorded Execution,
	command PlayerActionCommand,
) (ActionResult, error) {
	if err := recorded.Validate(); err != nil || recorded.CommandID != command.CommandID ||
		recorded.RevisionID != command.ResultRevisionID || len(recorded.Actions) == 0 {
		return ActionResult{}, ErrActionConflict
	}
	action := recorded.Actions[len(recorded.Actions)-1]
	if action.ID != command.ActionID || action.CommandID != command.CommandID ||
		action.ResultRevisionID != command.ResultRevisionID || action.Turn != command.ExpectedTurn ||
		action.ActorID != command.ActorID || action.Action != command.Action ||
		action.Category != command.Category || action.Automatic {
		return ActionResult{}, ErrActionConflict
	}
	return ActionResult{
		Draft:       cloneDraftExecution(recorded),
		NextTimeout: draftTimeoutArm(recorded),
	}, nil
}

func draftExecutionFromDomain(
	draft domain.Draft,
	state ExecutionState,
	revisionID uuid.UUID,
	previousRevisionID uuid.UUID,
	revision int64,
	commandID uuid.UUID,
	serviceEpoch uuid.UUID,
	firstActorDecision domain.DecisionEvidence,
	actions []ActionRecord,
) (Execution, error) {
	execution := Execution{
		ID: draft.ID, SeriesID: draft.SeriesID, Format: draft.Format,
		FirstParticipantID: draft.FirstParticipantID, SecondParticipantID: draft.SecondParticipantID,
		Pool: append([]domain.Category(nil), draft.Pool...), State: state,
		RevisionID: revisionID, PreviousRevisionID: previousRevisionID, Revision: revision,
		CommandID: commandID, ServiceEpoch: serviceEpoch, Turn: draft.Turn,
		TurnDeadline: draft.TurnDeadline, Actions: cloneDraftActionRecords(actions),
		SelectedCategories: append([]domain.Category(nil), draft.SelectedCategories...),
		FirstActorDecision: cloneDraftDecisionEvidenceValue(firstActorDecision),
	}
	if state == ExecutionStateActive {
		deadline := draft.TurnDeadline
		execution.AbsoluteDeadline = &deadline
	}
	if state == ExecutionStateActive {
		turn, err := draft.CurrentTurn()
		if err != nil {
			return Execution{}, draftExecutionError("current turn: %v", err)
		}
		execution.CurrentActorID = cloneDraftUUID(&turn.ActorID)
		execution.CurrentAction = cloneDraftActionType(&turn.Action)
		execution.LegalCategories = draftLegalCategories(draft)
	}
	if err := execution.Validate(); err != nil {
		return Execution{}, err
	}
	return cloneDraftExecution(execution), nil
}
