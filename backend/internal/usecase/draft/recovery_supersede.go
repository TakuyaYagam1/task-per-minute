package draft

import (
	"context"
	"errors"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (u *RecoveryUseCase) Supersede(
	ctx context.Context,
	command SupersedeCommand,
) (RecoveryResult, error) {
	if !u.available() {
		return RecoveryResult{}, domain.ErrValidation
	}
	if err := validateDraftSupersedeCommand(command); err != nil {
		return RecoveryResult{}, err
	}
	if recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.CommandID); err != nil {
		return RecoveryResult{}, fmt.Errorf("RecoveryUseCase - find supersession: %w", err)
	} else if recorded != nil {
		return reconcileDraftSupersede(*recorded, command)
	}

	current, err := u.loadExpectedDraft(
		ctx,
		command.DraftID,
		command.ExpectedRevisionID,
		command.ExpectedRevision,
		command.ExpectedServiceEpoch,
	)
	if err != nil {
		if errors.Is(err, ErrRecoveryConflict) {
			return u.reconcileSupersedeConflict(ctx, command)
		}
		return RecoveryResult{}, err
	}
	if current.State != ExecutionStateActive && current.State != ExecutionStatePaused {
		return RecoveryResult{}, ErrRecoveryState
	}
	now, err := u.serverTime()
	if err != nil {
		return RecoveryResult{}, err
	}
	if current.State == ExecutionStateActive && !now.Before(current.TurnDeadline) {
		return RecoveryResult{}, ErrCorrectionCutoff
	}

	next := cloneDraftExecution(current)
	advanceDraftRevision(&next, command.ResultRevisionID, command.CommandID, current.ServiceEpoch)
	next.State = ExecutionStateSuperseded
	next.AbsoluteDeadline = nil
	next.PausedRemaining = 0
	next.CurrentActorID = nil
	next.CurrentAction = nil
	next.LegalCategories = nil
	next.Recovery = &RecoveryEvidence{
		Reason:               RecoveryReasonCorrection,
		PreviousState:        current.State,
		PreviousServiceEpoch: current.ServiceEpoch, CurrentServiceEpoch: current.ServiceEpoch,
		PreviousDeadline: current.TurnDeadline, RecordedAt: now,
		ActorID: command.ActorID, Note: command.Reason,
	}
	next.Transition = &TransitionEvidence{
		Operation: TransitionSupersede, ActorID: command.ActorID,
		Reason: command.Reason, OccurredAt: now,
	}
	if err := next.Validate(); err != nil {
		return RecoveryResult{}, err
	}
	return u.commitSupersede(ctx, current, next, command)
}

func (u *RecoveryUseCase) commitSupersede(
	ctx context.Context,
	current Execution,
	next Execution,
	command SupersedeCommand,
) (RecoveryResult, error) {
	committed, changed, err := u.repository.CommitDraftRevisions(
		ctx,
		draftExpectation(current),
		[]Execution{next},
	)
	if errors.Is(err, domain.ErrConflict) {
		return u.reconcileSupersedeConflict(ctx, command)
	}
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("RecoveryUseCase - commit supersession: %w", err)
	}
	if committed == nil {
		return u.reconcileSupersedeConflict(ctx, command)
	}
	result, reconcileErr := reconcileDraftSupersede(*committed, command)
	if reconcileErr != nil {
		return RecoveryResult{}, domain.ErrInternal
	}
	result.Changed = changed
	return result, nil
}

func (u *RecoveryUseCase) reconcileSupersedeConflict(
	ctx context.Context,
	command SupersedeCommand,
) (RecoveryResult, error) {
	recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("RecoveryUseCase - reconcile supersession: %w", err)
	}
	if recorded == nil {
		return RecoveryResult{}, ErrRecoveryConflict
	}
	return reconcileDraftSupersede(*recorded, command)
}

func reconcileDraftSupersede(
	recorded Execution,
	command SupersedeCommand,
) (RecoveryResult, error) {
	if err := recorded.Validate(); err != nil || recorded.State != ExecutionStateSuperseded ||
		recorded.RevisionID != command.ResultRevisionID || recorded.CommandID != command.CommandID ||
		recorded.Transition == nil || recorded.Transition.Operation != TransitionSupersede ||
		recorded.Transition.ActorID != command.ActorID || recorded.Transition.Reason != command.Reason {
		return RecoveryResult{}, ErrRecoveryConflict
	}
	return RecoveryResult{Draft: cloneDraftExecution(recorded)}, nil
}
