package draft

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (u *RecoveryUseCase) RecoverEpoch(
	ctx context.Context,
	command EpochRecoveryCommand,
) (RecoveryResult, error) {
	if !u.available() {
		return RecoveryResult{}, domain.ErrValidation
	}
	if err := validateDraftEpochRecoveryCommand(command); err != nil {
		return RecoveryResult{}, err
	}
	if result, found, err := u.findRecordedEpochRecovery(ctx, command); found || err != nil {
		return result, err
	}

	current, err := u.loadExpectedDraft(
		ctx,
		command.DraftID,
		command.ExpectedRevisionID,
		command.ExpectedRevision,
		command.PreviousServiceEpoch,
	)
	if err != nil {
		if errors.Is(err, ErrRecoveryConflict) {
			return u.reconcileEpochRecoveryConflict(ctx, command)
		}
		return RecoveryResult{}, err
	}
	if current.State != ExecutionStateActive || command.CurrentServiceEpoch == current.ServiceEpoch {
		return RecoveryResult{}, ErrRecoveryState
	}
	now, err := u.serverTime()
	if err != nil {
		return RecoveryResult{}, err
	}

	recovery, resumed, err := buildDraftEpochRecovery(current, command, now)
	if err != nil {
		return RecoveryResult{}, err
	}
	return u.commitEpochRecovery(ctx, current, recovery, resumed, command)
}

func (u *RecoveryUseCase) findRecordedEpochRecovery(
	ctx context.Context,
	command EpochRecoveryCommand,
) (RecoveryResult, bool, error) {
	recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.ResumeCommandID)
	if err != nil {
		return RecoveryResult{}, true, fmt.Errorf("RecoveryUseCase - find epoch resume: %w", err)
	}
	if recorded != nil {
		result, reconcileErr := reconcileDraftEpochRecovery(*recorded, command)
		return result, true, reconcileErr
	}
	recorded, err = u.repository.FindDraftCommand(ctx, command.DraftID, command.RecoveryCommandID)
	if err != nil {
		return RecoveryResult{}, true, fmt.Errorf("RecoveryUseCase - find epoch recovery: %w", err)
	}
	if recorded != nil {
		return RecoveryResult{}, true, domain.ErrInternal
	}
	return RecoveryResult{}, false, nil
}

func buildDraftEpochRecovery(
	current Execution,
	command EpochRecoveryCommand,
	recoveredAt time.Time,
) (Execution, Execution, error) {
	const recoveryNote = "service epoch changed before timeout ownership was proven"
	recovery := cloneDraftExecution(current)
	advanceDraftRevision(
		&recovery,
		command.RecoveryRevisionID,
		command.RecoveryCommandID,
		command.CurrentServiceEpoch,
	)
	recovery.State = ExecutionStateRecoveryRequired
	recovery.AbsoluteDeadline = nil
	recovery.Recovery = &RecoveryEvidence{
		Policy: RecoveryPolicyFreshOnResume, Reason: RecoveryReasonEpochMismatch,
		PreviousState:        current.State,
		PreviousServiceEpoch: current.ServiceEpoch, CurrentServiceEpoch: command.CurrentServiceEpoch,
		PreviousDeadline: current.TurnDeadline, RecordedAt: recoveredAt,
		ActorID: command.RecoveryOwnerID, Note: recoveryNote,
	}
	recovery.Transition = &TransitionEvidence{
		Operation: TransitionEpochRecovery, ActorID: command.RecoveryOwnerID,
		Reason: recoveryNote, OccurredAt: recoveredAt,
	}
	if err := recovery.Validate(); err != nil {
		return Execution{}, Execution{}, err
	}

	deadline := recoveredAt.Add(TurnDuration)
	resumed := cloneDraftExecution(recovery)
	advanceDraftRevision(
		&resumed,
		command.ResumeRevisionID,
		command.ResumeCommandID,
		command.CurrentServiceEpoch,
	)
	resumed.State = ExecutionStateActive
	resumed.TurnDeadline = deadline
	resumed.AbsoluteDeadline = cloneDraftTime(&deadline)
	resumed.Recovery = nil
	resumed.Transition = &TransitionEvidence{
		Operation: TransitionFreshResume, ActorID: command.RecoveryOwnerID,
		Reason: "fresh turn after epoch recovery", OccurredAt: recoveredAt,
	}
	if err := resumed.Validate(); err != nil {
		return Execution{}, Execution{}, err
	}
	return recovery, resumed, nil
}

func (u *RecoveryUseCase) commitEpochRecovery(
	ctx context.Context,
	current Execution,
	recovery Execution,
	resumed Execution,
	command EpochRecoveryCommand,
) (RecoveryResult, error) {
	committed, changed, err := u.repository.CommitDraftRevisions(
		ctx,
		draftExpectation(current),
		[]Execution{recovery, resumed},
	)
	if errors.Is(err, domain.ErrConflict) {
		return u.reconcileEpochRecoveryConflict(ctx, command)
	}
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("RecoveryUseCase - commit epoch recovery: %w", err)
	}
	if committed == nil {
		return u.reconcileEpochRecoveryConflict(ctx, command)
	}
	result, reconcileErr := reconcileDraftEpochRecovery(*committed, command)
	if reconcileErr != nil {
		return RecoveryResult{}, domain.ErrInternal
	}
	result.Changed = changed
	return result, nil
}

func (u *RecoveryUseCase) reconcileEpochRecoveryConflict(
	ctx context.Context,
	command EpochRecoveryCommand,
) (RecoveryResult, error) {
	recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.ResumeCommandID)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("RecoveryUseCase - reconcile epoch recovery: %w", err)
	}
	if recorded == nil {
		return RecoveryResult{}, ErrRecoveryConflict
	}
	return reconcileDraftEpochRecovery(*recorded, command)
}

func reconcileDraftEpochRecovery(
	recorded Execution,
	command EpochRecoveryCommand,
) (RecoveryResult, error) {
	if err := recorded.Validate(); err != nil || recorded.State != ExecutionStateActive ||
		recorded.RevisionID != command.ResumeRevisionID || recorded.CommandID != command.ResumeCommandID ||
		recorded.ServiceEpoch != command.CurrentServiceEpoch || recorded.Transition == nil ||
		recorded.Transition.Operation != TransitionFreshResume ||
		recorded.Transition.ActorID != command.RecoveryOwnerID {
		return RecoveryResult{}, ErrRecoveryConflict
	}
	return RecoveryResult{
		Draft: cloneDraftExecution(recorded), NextTimeout: draftTimeoutArm(recorded),
	}, nil
}
