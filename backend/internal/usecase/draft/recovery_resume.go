package draft

import (
	"context"
	"errors"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (u *RecoveryUseCase) Resume(
	ctx context.Context,
	command ResumeCommand,
) (RecoveryResult, error) {
	if !u.available() {
		return RecoveryResult{}, domain.ErrValidation
	}
	if err := validateDraftResumeCommand(command); err != nil {
		return RecoveryResult{}, err
	}
	if recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.CommandID); err != nil {
		return RecoveryResult{}, fmt.Errorf("RecoveryUseCase - find resume: %w", err)
	} else if recorded != nil {
		return reconcileDraftResume(*recorded, command)
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
			return u.reconcileResumeConflict(ctx, command)
		}
		return RecoveryResult{}, err
	}
	if current.State != ExecutionStatePaused || current.Recovery == nil ||
		current.Recovery.Policy != RecoveryPolicyShiftRemaining || current.PausedRemaining <= 0 {
		return RecoveryResult{}, ErrRecoveryState
	}
	now, err := u.serverTime()
	if err != nil {
		return RecoveryResult{}, err
	}
	deadline := now.Add(current.PausedRemaining)

	next := cloneDraftExecution(current)
	advanceDraftRevision(&next, command.ResultRevisionID, command.CommandID, current.ServiceEpoch)
	next.State = ExecutionStateActive
	next.TurnDeadline = deadline
	next.AbsoluteDeadline = cloneDraftTime(&deadline)
	next.PausedRemaining = 0
	next.Recovery = nil
	next.Transition = &TransitionEvidence{
		Operation: TransitionResume, ActorID: command.ActorID,
		Reason: command.Reason, OccurredAt: now,
	}
	if err := next.Validate(); err != nil {
		return RecoveryResult{}, err
	}
	return u.commitResume(ctx, current, next, command)
}

func (u *RecoveryUseCase) commitResume(
	ctx context.Context,
	current Execution,
	next Execution,
	command ResumeCommand,
) (RecoveryResult, error) {
	committed, changed, err := u.repository.CommitDraftRevisions(
		ctx,
		draftExpectation(current),
		[]Execution{next},
	)
	if errors.Is(err, domain.ErrConflict) {
		return u.reconcileResumeConflict(ctx, command)
	}
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("RecoveryUseCase - commit resume: %w", err)
	}
	if committed == nil {
		return u.reconcileResumeConflict(ctx, command)
	}
	result, reconcileErr := reconcileDraftResume(*committed, command)
	if reconcileErr != nil {
		return RecoveryResult{}, domain.ErrInternal
	}
	result.Changed = changed
	return result, nil
}

func (u *RecoveryUseCase) reconcileResumeConflict(
	ctx context.Context,
	command ResumeCommand,
) (RecoveryResult, error) {
	recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("RecoveryUseCase - reconcile resume: %w", err)
	}
	if recorded == nil {
		return RecoveryResult{}, ErrRecoveryConflict
	}
	return reconcileDraftResume(*recorded, command)
}

func reconcileDraftResume(
	recorded Execution,
	command ResumeCommand,
) (RecoveryResult, error) {
	if err := recorded.Validate(); err != nil || recorded.State != ExecutionStateActive ||
		recorded.RevisionID != command.ResultRevisionID || recorded.CommandID != command.CommandID ||
		recorded.Transition == nil || recorded.Transition.Operation != TransitionResume ||
		recorded.Transition.ActorID != command.ActorID || recorded.Transition.Reason != command.Reason {
		return RecoveryResult{}, ErrRecoveryConflict
	}
	return RecoveryResult{
		Draft: cloneDraftExecution(recorded), NextTimeout: draftTimeoutArm(recorded),
	}, nil
}
