package draft

import (
	"context"
	"errors"
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (u *RecoveryUseCase) Pause(
	ctx context.Context,
	command PauseCommand,
) (RecoveryResult, error) {
	if !u.available() {
		return RecoveryResult{}, domain.ErrValidation
	}
	if err := validateDraftPauseCommand(command); err != nil {
		return RecoveryResult{}, err
	}
	if recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.CommandID); err != nil {
		return RecoveryResult{}, fmt.Errorf("RecoveryUseCase - find pause: %w", err)
	} else if recorded != nil {
		return reconcileDraftPause(*recorded, command)
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
			return u.reconcilePauseConflict(ctx, command)
		}
		return RecoveryResult{}, err
	}
	if current.State != ExecutionStateActive || current.AbsoluteDeadline == nil {
		return RecoveryResult{}, ErrRecoveryState
	}
	now, err := u.serverTime()
	if err != nil {
		return RecoveryResult{}, err
	}
	if !now.Before(*current.AbsoluteDeadline) {
		return RecoveryResult{}, ErrPauseCutoff
	}
	remaining := current.AbsoluteDeadline.Sub(now)
	if remaining <= 0 || remaining > TurnDuration {
		return RecoveryResult{}, ErrPauseCutoff
	}

	next := cloneDraftExecution(current)
	advanceDraftRevision(&next, command.ResultRevisionID, command.CommandID, current.ServiceEpoch)
	next.State = ExecutionStatePaused
	next.AbsoluteDeadline = nil
	next.PausedRemaining = remaining
	next.Recovery = &RecoveryEvidence{
		Policy: RecoveryPolicyShiftRemaining, Reason: RecoveryReasonOperatorPause,
		PreviousState:        current.State,
		PreviousServiceEpoch: current.ServiceEpoch, CurrentServiceEpoch: current.ServiceEpoch,
		PreviousDeadline: current.TurnDeadline, RecordedAt: now,
		ActorID: command.ActorID, Note: command.Reason,
	}
	next.Transition = &TransitionEvidence{
		Operation: TransitionPause, ActorID: command.ActorID,
		Reason: command.Reason, OccurredAt: now,
	}
	if err := next.Validate(); err != nil {
		return RecoveryResult{}, err
	}
	return u.commitPause(ctx, current, next, command)
}

func (u *RecoveryUseCase) commitPause(
	ctx context.Context,
	current Execution,
	next Execution,
	command PauseCommand,
) (RecoveryResult, error) {
	committed, changed, err := u.repository.CommitDraftRevisions(
		ctx,
		draftExpectation(current),
		[]Execution{next},
	)
	if errors.Is(err, domain.ErrConflict) {
		return u.reconcilePauseConflict(ctx, command)
	}
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("RecoveryUseCase - commit pause: %w", err)
	}
	if committed == nil {
		return u.reconcilePauseConflict(ctx, command)
	}
	result, reconcileErr := reconcileDraftPause(*committed, command)
	if reconcileErr != nil {
		return RecoveryResult{}, domain.ErrInternal
	}
	result.Changed = changed
	return result, nil
}

func (u *RecoveryUseCase) reconcilePauseConflict(
	ctx context.Context,
	command PauseCommand,
) (RecoveryResult, error) {
	recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("RecoveryUseCase - reconcile pause: %w", err)
	}
	if recorded == nil {
		return RecoveryResult{}, ErrRecoveryConflict
	}
	return reconcileDraftPause(*recorded, command)
}

func reconcileDraftPause(
	recorded Execution,
	command PauseCommand,
) (RecoveryResult, error) {
	if err := recorded.Validate(); err != nil || recorded.State != ExecutionStatePaused ||
		recorded.RevisionID != command.ResultRevisionID || recorded.CommandID != command.CommandID ||
		recorded.Transition == nil || recorded.Transition.Operation != TransitionPause ||
		recorded.Transition.ActorID != command.ActorID || recorded.Transition.Reason != command.Reason {
		return RecoveryResult{}, ErrRecoveryConflict
	}
	return RecoveryResult{Draft: cloneDraftExecution(recorded)}, nil
}
