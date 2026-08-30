package arena

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrDraftPauseCutoff      = errors.New("arena draft pause cutoff reached")
	ErrDraftRecoveryConflict = errors.New("arena draft recovery conflict")
	ErrDraftRecoveryState    = errors.New("arena draft state does not allow recovery")
	ErrDraftCorrectionCutoff = errors.New("arena draft correction cutoff reached")
)

type DraftPauseCommand struct {
	DraftID              uuid.UUID
	ExpectedRevisionID   uuid.UUID
	ExpectedRevision     int64
	ExpectedServiceEpoch uuid.UUID
	CommandID            uuid.UUID
	ResultRevisionID     uuid.UUID
	ActorID              uuid.UUID
	Reason               string
}

type DraftResumeCommand struct {
	DraftID              uuid.UUID
	ExpectedRevisionID   uuid.UUID
	ExpectedRevision     int64
	ExpectedServiceEpoch uuid.UUID
	CommandID            uuid.UUID
	ResultRevisionID     uuid.UUID
	ActorID              uuid.UUID
	Reason               string
}

type DraftEpochRecoveryCommand struct {
	DraftID              uuid.UUID
	ExpectedRevisionID   uuid.UUID
	ExpectedRevision     int64
	PreviousServiceEpoch uuid.UUID
	CurrentServiceEpoch  uuid.UUID
	RecoveryCommandID    uuid.UUID
	RecoveryRevisionID   uuid.UUID
	ResumeCommandID      uuid.UUID
	ResumeRevisionID     uuid.UUID
	RecoveryOwnerID      uuid.UUID
}

type DraftSupersedeCommand struct {
	DraftID              uuid.UUID
	ExpectedRevisionID   uuid.UUID
	ExpectedRevision     int64
	ExpectedServiceEpoch uuid.UUID
	CommandID            uuid.UUID
	ResultRevisionID     uuid.UUID
	ActorID              uuid.UUID
	Reason               string
}

type DraftRecoveryResult struct {
	Draft       DraftExecution
	Changed     bool
	NextTimeout *DraftTimeoutArm
}

type DraftRecoveryUseCase struct {
	repository DraftRepository
	clock      Clock
}

func NewDraftRecoveryUseCase(repository DraftRepository, clock Clock) *DraftRecoveryUseCase {
	return &DraftRecoveryUseCase{repository: repository, clock: clock}
}

func (u *DraftRecoveryUseCase) Pause(
	ctx context.Context,
	command DraftPauseCommand,
) (DraftRecoveryResult, error) {
	if !u.available() {
		return DraftRecoveryResult{}, domain.ErrValidation
	}
	if err := validateDraftPauseCommand(command); err != nil {
		return DraftRecoveryResult{}, err
	}
	if recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.CommandID); err != nil {
		return DraftRecoveryResult{}, fmt.Errorf("DraftRecoveryUseCase - find pause: %w", err)
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
		if errors.Is(err, ErrDraftRecoveryConflict) {
			return u.reconcilePauseConflict(ctx, command)
		}
		return DraftRecoveryResult{}, err
	}
	if current.State != DraftExecutionStateActive || current.AbsoluteDeadline == nil {
		return DraftRecoveryResult{}, ErrDraftRecoveryState
	}
	now, err := u.serverTime()
	if err != nil {
		return DraftRecoveryResult{}, err
	}
	if !now.Before(*current.AbsoluteDeadline) {
		return DraftRecoveryResult{}, ErrDraftPauseCutoff
	}
	remaining := current.AbsoluteDeadline.Sub(now)
	if remaining <= 0 || remaining > DraftTurnDuration {
		return DraftRecoveryResult{}, ErrDraftPauseCutoff
	}

	next := cloneDraftExecution(current)
	advanceDraftRevision(&next, command.ResultRevisionID, command.CommandID, current.ServiceEpoch)
	next.State = DraftExecutionStatePaused
	next.AbsoluteDeadline = nil
	next.PausedRemaining = remaining
	next.Recovery = &DraftRecoveryEvidence{
		Policy: DraftRecoveryPolicyShiftRemaining, Reason: DraftRecoveryReasonOperatorPause,
		PreviousState:        current.State,
		PreviousServiceEpoch: current.ServiceEpoch, CurrentServiceEpoch: current.ServiceEpoch,
		PreviousDeadline: current.TurnDeadline, RecordedAt: now,
		ActorID: command.ActorID, Note: command.Reason,
	}
	next.Transition = &DraftTransitionEvidence{
		Operation: DraftTransitionPause, ActorID: command.ActorID,
		Reason: command.Reason, OccurredAt: now,
	}
	if err := next.Validate(); err != nil {
		return DraftRecoveryResult{}, err
	}
	return u.commitPause(ctx, current, next, command)
}

func (u *DraftRecoveryUseCase) Resume(
	ctx context.Context,
	command DraftResumeCommand,
) (DraftRecoveryResult, error) {
	if !u.available() {
		return DraftRecoveryResult{}, domain.ErrValidation
	}
	if err := validateDraftResumeCommand(command); err != nil {
		return DraftRecoveryResult{}, err
	}
	if recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.CommandID); err != nil {
		return DraftRecoveryResult{}, fmt.Errorf("DraftRecoveryUseCase - find resume: %w", err)
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
		if errors.Is(err, ErrDraftRecoveryConflict) {
			return u.reconcileResumeConflict(ctx, command)
		}
		return DraftRecoveryResult{}, err
	}
	if current.State != DraftExecutionStatePaused || current.Recovery == nil ||
		current.Recovery.Policy != DraftRecoveryPolicyShiftRemaining || current.PausedRemaining <= 0 {
		return DraftRecoveryResult{}, ErrDraftRecoveryState
	}
	now, err := u.serverTime()
	if err != nil {
		return DraftRecoveryResult{}, err
	}
	deadline := now.Add(current.PausedRemaining)

	next := cloneDraftExecution(current)
	advanceDraftRevision(&next, command.ResultRevisionID, command.CommandID, current.ServiceEpoch)
	next.State = DraftExecutionStateActive
	next.TurnDeadline = deadline
	next.AbsoluteDeadline = cloneDraftTime(&deadline)
	next.PausedRemaining = 0
	next.Recovery = nil
	next.Transition = &DraftTransitionEvidence{
		Operation: DraftTransitionResume, ActorID: command.ActorID,
		Reason: command.Reason, OccurredAt: now,
	}
	if err := next.Validate(); err != nil {
		return DraftRecoveryResult{}, err
	}
	return u.commitResume(ctx, current, next, command)
}

func (u *DraftRecoveryUseCase) RecoverEpoch(
	ctx context.Context,
	command DraftEpochRecoveryCommand,
) (DraftRecoveryResult, error) {
	if !u.available() {
		return DraftRecoveryResult{}, domain.ErrValidation
	}
	if err := validateDraftEpochRecoveryCommand(command); err != nil {
		return DraftRecoveryResult{}, err
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
		if errors.Is(err, ErrDraftRecoveryConflict) {
			return u.reconcileEpochRecoveryConflict(ctx, command)
		}
		return DraftRecoveryResult{}, err
	}
	if current.State != DraftExecutionStateActive || command.CurrentServiceEpoch == current.ServiceEpoch {
		return DraftRecoveryResult{}, ErrDraftRecoveryState
	}
	now, err := u.serverTime()
	if err != nil {
		return DraftRecoveryResult{}, err
	}

	recovery, resumed, err := buildDraftEpochRecovery(current, command, now)
	if err != nil {
		return DraftRecoveryResult{}, err
	}
	return u.commitEpochRecovery(ctx, current, recovery, resumed, command)
}

func (u *DraftRecoveryUseCase) findRecordedEpochRecovery(
	ctx context.Context,
	command DraftEpochRecoveryCommand,
) (DraftRecoveryResult, bool, error) {
	recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.ResumeCommandID)
	if err != nil {
		return DraftRecoveryResult{}, true, fmt.Errorf("DraftRecoveryUseCase - find epoch resume: %w", err)
	}
	if recorded != nil {
		result, reconcileErr := reconcileDraftEpochRecovery(*recorded, command)
		return result, true, reconcileErr
	}
	recorded, err = u.repository.FindDraftCommand(ctx, command.DraftID, command.RecoveryCommandID)
	if err != nil {
		return DraftRecoveryResult{}, true, fmt.Errorf("DraftRecoveryUseCase - find epoch recovery: %w", err)
	}
	if recorded != nil {
		return DraftRecoveryResult{}, true, domain.ErrInternal
	}
	return DraftRecoveryResult{}, false, nil
}

func buildDraftEpochRecovery(
	current DraftExecution,
	command DraftEpochRecoveryCommand,
	recoveredAt time.Time,
) (DraftExecution, DraftExecution, error) {
	const recoveryNote = "service epoch changed before timeout ownership was proven"
	recovery := cloneDraftExecution(current)
	advanceDraftRevision(
		&recovery,
		command.RecoveryRevisionID,
		command.RecoveryCommandID,
		command.CurrentServiceEpoch,
	)
	recovery.State = DraftExecutionStateRecoveryRequired
	recovery.AbsoluteDeadline = nil
	recovery.Recovery = &DraftRecoveryEvidence{
		Policy: DraftRecoveryPolicyFreshOnResume, Reason: DraftRecoveryReasonEpochMismatch,
		PreviousState:        current.State,
		PreviousServiceEpoch: current.ServiceEpoch, CurrentServiceEpoch: command.CurrentServiceEpoch,
		PreviousDeadline: current.TurnDeadline, RecordedAt: recoveredAt,
		ActorID: command.RecoveryOwnerID, Note: recoveryNote,
	}
	recovery.Transition = &DraftTransitionEvidence{
		Operation: DraftTransitionEpochRecovery, ActorID: command.RecoveryOwnerID,
		Reason: recoveryNote, OccurredAt: recoveredAt,
	}
	if err := recovery.Validate(); err != nil {
		return DraftExecution{}, DraftExecution{}, err
	}

	deadline := recoveredAt.Add(DraftTurnDuration)
	resumed := cloneDraftExecution(recovery)
	advanceDraftRevision(
		&resumed,
		command.ResumeRevisionID,
		command.ResumeCommandID,
		command.CurrentServiceEpoch,
	)
	resumed.State = DraftExecutionStateActive
	resumed.TurnDeadline = deadline
	resumed.AbsoluteDeadline = cloneDraftTime(&deadline)
	resumed.Recovery = nil
	resumed.Transition = &DraftTransitionEvidence{
		Operation: DraftTransitionFreshResume, ActorID: command.RecoveryOwnerID,
		Reason: "fresh turn after epoch recovery", OccurredAt: recoveredAt,
	}
	if err := resumed.Validate(); err != nil {
		return DraftExecution{}, DraftExecution{}, err
	}
	return recovery, resumed, nil
}

func (u *DraftRecoveryUseCase) commitEpochRecovery(
	ctx context.Context,
	current DraftExecution,
	recovery DraftExecution,
	resumed DraftExecution,
	command DraftEpochRecoveryCommand,
) (DraftRecoveryResult, error) {
	committed, changed, err := u.repository.CommitDraftRevisions(
		ctx,
		draftExpectation(current),
		[]DraftExecution{recovery, resumed},
	)
	if errors.Is(err, domain.ErrConflict) {
		return u.reconcileEpochRecoveryConflict(ctx, command)
	}
	if err != nil {
		return DraftRecoveryResult{}, fmt.Errorf("DraftRecoveryUseCase - commit epoch recovery: %w", err)
	}
	if committed == nil {
		return u.reconcileEpochRecoveryConflict(ctx, command)
	}
	result, reconcileErr := reconcileDraftEpochRecovery(*committed, command)
	if reconcileErr != nil {
		return DraftRecoveryResult{}, domain.ErrInternal
	}
	result.Changed = changed
	return result, nil
}

func (u *DraftRecoveryUseCase) Supersede(
	ctx context.Context,
	command DraftSupersedeCommand,
) (DraftRecoveryResult, error) {
	if !u.available() {
		return DraftRecoveryResult{}, domain.ErrValidation
	}
	if err := validateDraftSupersedeCommand(command); err != nil {
		return DraftRecoveryResult{}, err
	}
	if recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.CommandID); err != nil {
		return DraftRecoveryResult{}, fmt.Errorf("DraftRecoveryUseCase - find supersession: %w", err)
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
		if errors.Is(err, ErrDraftRecoveryConflict) {
			return u.reconcileSupersedeConflict(ctx, command)
		}
		return DraftRecoveryResult{}, err
	}
	if current.State != DraftExecutionStateActive && current.State != DraftExecutionStatePaused {
		return DraftRecoveryResult{}, ErrDraftRecoveryState
	}
	now, err := u.serverTime()
	if err != nil {
		return DraftRecoveryResult{}, err
	}
	if current.State == DraftExecutionStateActive && !now.Before(current.TurnDeadline) {
		return DraftRecoveryResult{}, ErrDraftCorrectionCutoff
	}

	next := cloneDraftExecution(current)
	advanceDraftRevision(&next, command.ResultRevisionID, command.CommandID, current.ServiceEpoch)
	next.State = DraftExecutionStateSuperseded
	next.AbsoluteDeadline = nil
	next.PausedRemaining = 0
	next.CurrentActorID = nil
	next.CurrentAction = nil
	next.LegalCategories = nil
	next.Recovery = &DraftRecoveryEvidence{
		Reason:               DraftRecoveryReasonCorrection,
		PreviousState:        current.State,
		PreviousServiceEpoch: current.ServiceEpoch, CurrentServiceEpoch: current.ServiceEpoch,
		PreviousDeadline: current.TurnDeadline, RecordedAt: now,
		ActorID: command.ActorID, Note: command.Reason,
	}
	next.Transition = &DraftTransitionEvidence{
		Operation: DraftTransitionSupersede, ActorID: command.ActorID,
		Reason: command.Reason, OccurredAt: now,
	}
	if err := next.Validate(); err != nil {
		return DraftRecoveryResult{}, err
	}
	return u.commitSupersede(ctx, current, next, command)
}

func (u *DraftRecoveryUseCase) available() bool {
	return u != nil && u.repository != nil && u.clock != nil
}

func (u *DraftRecoveryUseCase) serverTime() (time.Time, error) {
	now := u.clock.Now()
	if !validArenaServerTime(now) {
		return time.Time{}, domain.ErrValidation
	}
	return now, nil
}

func (u *DraftRecoveryUseCase) loadExpectedDraft(
	ctx context.Context,
	draftID uuid.UUID,
	revisionID uuid.UUID,
	revision int64,
	serviceEpoch uuid.UUID,
) (DraftExecution, error) {
	current, err := u.repository.LoadDraft(ctx, draftID)
	if err != nil {
		return DraftExecution{}, fmt.Errorf("DraftRecoveryUseCase - load draft: %w", err)
	}
	if current == nil {
		return DraftExecution{}, ErrDraftNotFound
	}
	if err := current.Validate(); err != nil {
		return DraftExecution{}, domain.ErrInternal
	}
	if !draftExpectationMatches(*current, revisionID, revision, serviceEpoch) {
		return DraftExecution{}, ErrDraftRecoveryConflict
	}
	return cloneDraftExecution(*current), nil
}

func validateDraftPauseCommand(command DraftPauseCommand) error {
	return validateDraftRecoveryMutation(
		command.DraftID,
		command.ExpectedRevisionID,
		command.ExpectedRevision,
		command.ExpectedServiceEpoch,
		command.CommandID,
		command.ResultRevisionID,
		command.ActorID,
		command.Reason,
	)
}

func validateDraftResumeCommand(command DraftResumeCommand) error {
	return validateDraftRecoveryMutation(
		command.DraftID,
		command.ExpectedRevisionID,
		command.ExpectedRevision,
		command.ExpectedServiceEpoch,
		command.CommandID,
		command.ResultRevisionID,
		command.ActorID,
		command.Reason,
	)
}

func validateDraftSupersedeCommand(command DraftSupersedeCommand) error {
	return validateDraftRecoveryMutation(
		command.DraftID,
		command.ExpectedRevisionID,
		command.ExpectedRevision,
		command.ExpectedServiceEpoch,
		command.CommandID,
		command.ResultRevisionID,
		command.ActorID,
		command.Reason,
	)
}

func validateDraftRecoveryMutation(
	draftID uuid.UUID,
	expectedRevisionID uuid.UUID,
	expectedRevision int64,
	expectedServiceEpoch uuid.UUID,
	commandID uuid.UUID,
	resultRevisionID uuid.UUID,
	actorID uuid.UUID,
	reason string,
) error {
	ids := []uuid.UUID{
		draftID, expectedRevisionID, expectedServiceEpoch, commandID, resultRevisionID, actorID,
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return domain.ErrValidation
		}
		if _, duplicate := seen[id]; duplicate {
			return domain.ErrValidation
		}
		seen[id] = struct{}{}
	}
	if expectedRevision < 1 || reason == "" || reason != strings.TrimSpace(reason) {
		return domain.ErrValidation
	}
	return nil
}

func validateDraftEpochRecoveryCommand(command DraftEpochRecoveryCommand) error {
	ids := []uuid.UUID{
		command.DraftID, command.ExpectedRevisionID, command.PreviousServiceEpoch,
		command.CurrentServiceEpoch, command.RecoveryCommandID, command.RecoveryRevisionID,
		command.ResumeCommandID, command.ResumeRevisionID, command.RecoveryOwnerID,
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return domain.ErrValidation
		}
		if _, duplicate := seen[id]; duplicate {
			return domain.ErrValidation
		}
		seen[id] = struct{}{}
	}
	if command.ExpectedRevision < 1 {
		return domain.ErrValidation
	}
	return nil
}

func advanceDraftRevision(
	draft *DraftExecution,
	revisionID uuid.UUID,
	commandID uuid.UUID,
	serviceEpoch uuid.UUID,
) {
	draft.PreviousRevisionID = draft.RevisionID
	draft.RevisionID = revisionID
	draft.Revision++
	draft.CommandID = commandID
	draft.ServiceEpoch = serviceEpoch
}

func (u *DraftRecoveryUseCase) commitPause(
	ctx context.Context,
	current DraftExecution,
	next DraftExecution,
	command DraftPauseCommand,
) (DraftRecoveryResult, error) {
	committed, changed, err := u.repository.CommitDraftRevisions(
		ctx,
		draftExpectation(current),
		[]DraftExecution{next},
	)
	if errors.Is(err, domain.ErrConflict) {
		return u.reconcilePauseConflict(ctx, command)
	}
	if err != nil {
		return DraftRecoveryResult{}, fmt.Errorf("DraftRecoveryUseCase - commit pause: %w", err)
	}
	if committed == nil {
		return u.reconcilePauseConflict(ctx, command)
	}
	result, reconcileErr := reconcileDraftPause(*committed, command)
	if reconcileErr != nil {
		return DraftRecoveryResult{}, domain.ErrInternal
	}
	result.Changed = changed
	return result, nil
}

func (u *DraftRecoveryUseCase) commitResume(
	ctx context.Context,
	current DraftExecution,
	next DraftExecution,
	command DraftResumeCommand,
) (DraftRecoveryResult, error) {
	committed, changed, err := u.repository.CommitDraftRevisions(
		ctx,
		draftExpectation(current),
		[]DraftExecution{next},
	)
	if errors.Is(err, domain.ErrConflict) {
		return u.reconcileResumeConflict(ctx, command)
	}
	if err != nil {
		return DraftRecoveryResult{}, fmt.Errorf("DraftRecoveryUseCase - commit resume: %w", err)
	}
	if committed == nil {
		return u.reconcileResumeConflict(ctx, command)
	}
	result, reconcileErr := reconcileDraftResume(*committed, command)
	if reconcileErr != nil {
		return DraftRecoveryResult{}, domain.ErrInternal
	}
	result.Changed = changed
	return result, nil
}

func (u *DraftRecoveryUseCase) commitSupersede(
	ctx context.Context,
	current DraftExecution,
	next DraftExecution,
	command DraftSupersedeCommand,
) (DraftRecoveryResult, error) {
	committed, changed, err := u.repository.CommitDraftRevisions(
		ctx,
		draftExpectation(current),
		[]DraftExecution{next},
	)
	if errors.Is(err, domain.ErrConflict) {
		return u.reconcileSupersedeConflict(ctx, command)
	}
	if err != nil {
		return DraftRecoveryResult{}, fmt.Errorf("DraftRecoveryUseCase - commit supersession: %w", err)
	}
	if committed == nil {
		return u.reconcileSupersedeConflict(ctx, command)
	}
	result, reconcileErr := reconcileDraftSupersede(*committed, command)
	if reconcileErr != nil {
		return DraftRecoveryResult{}, domain.ErrInternal
	}
	result.Changed = changed
	return result, nil
}

func (u *DraftRecoveryUseCase) reconcilePauseConflict(
	ctx context.Context,
	command DraftPauseCommand,
) (DraftRecoveryResult, error) {
	recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return DraftRecoveryResult{}, fmt.Errorf("DraftRecoveryUseCase - reconcile pause: %w", err)
	}
	if recorded == nil {
		return DraftRecoveryResult{}, ErrDraftRecoveryConflict
	}
	return reconcileDraftPause(*recorded, command)
}

func (u *DraftRecoveryUseCase) reconcileResumeConflict(
	ctx context.Context,
	command DraftResumeCommand,
) (DraftRecoveryResult, error) {
	recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return DraftRecoveryResult{}, fmt.Errorf("DraftRecoveryUseCase - reconcile resume: %w", err)
	}
	if recorded == nil {
		return DraftRecoveryResult{}, ErrDraftRecoveryConflict
	}
	return reconcileDraftResume(*recorded, command)
}

func (u *DraftRecoveryUseCase) reconcileEpochRecoveryConflict(
	ctx context.Context,
	command DraftEpochRecoveryCommand,
) (DraftRecoveryResult, error) {
	recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.ResumeCommandID)
	if err != nil {
		return DraftRecoveryResult{}, fmt.Errorf("DraftRecoveryUseCase - reconcile epoch recovery: %w", err)
	}
	if recorded == nil {
		return DraftRecoveryResult{}, ErrDraftRecoveryConflict
	}
	return reconcileDraftEpochRecovery(*recorded, command)
}

func (u *DraftRecoveryUseCase) reconcileSupersedeConflict(
	ctx context.Context,
	command DraftSupersedeCommand,
) (DraftRecoveryResult, error) {
	recorded, err := u.repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return DraftRecoveryResult{}, fmt.Errorf("DraftRecoveryUseCase - reconcile supersession: %w", err)
	}
	if recorded == nil {
		return DraftRecoveryResult{}, ErrDraftRecoveryConflict
	}
	return reconcileDraftSupersede(*recorded, command)
}

func reconcileDraftPause(
	recorded DraftExecution,
	command DraftPauseCommand,
) (DraftRecoveryResult, error) {
	if err := recorded.Validate(); err != nil || recorded.State != DraftExecutionStatePaused ||
		recorded.RevisionID != command.ResultRevisionID || recorded.CommandID != command.CommandID ||
		recorded.Transition == nil || recorded.Transition.Operation != DraftTransitionPause ||
		recorded.Transition.ActorID != command.ActorID || recorded.Transition.Reason != command.Reason {
		return DraftRecoveryResult{}, ErrDraftRecoveryConflict
	}
	return DraftRecoveryResult{Draft: cloneDraftExecution(recorded)}, nil
}

func reconcileDraftResume(
	recorded DraftExecution,
	command DraftResumeCommand,
) (DraftRecoveryResult, error) {
	if err := recorded.Validate(); err != nil || recorded.State != DraftExecutionStateActive ||
		recorded.RevisionID != command.ResultRevisionID || recorded.CommandID != command.CommandID ||
		recorded.Transition == nil || recorded.Transition.Operation != DraftTransitionResume ||
		recorded.Transition.ActorID != command.ActorID || recorded.Transition.Reason != command.Reason {
		return DraftRecoveryResult{}, ErrDraftRecoveryConflict
	}
	return DraftRecoveryResult{
		Draft: cloneDraftExecution(recorded), NextTimeout: draftTimeoutArm(recorded),
	}, nil
}

func reconcileDraftEpochRecovery(
	recorded DraftExecution,
	command DraftEpochRecoveryCommand,
) (DraftRecoveryResult, error) {
	if err := recorded.Validate(); err != nil || recorded.State != DraftExecutionStateActive ||
		recorded.RevisionID != command.ResumeRevisionID || recorded.CommandID != command.ResumeCommandID ||
		recorded.ServiceEpoch != command.CurrentServiceEpoch || recorded.Transition == nil ||
		recorded.Transition.Operation != DraftTransitionFreshResume ||
		recorded.Transition.ActorID != command.RecoveryOwnerID {
		return DraftRecoveryResult{}, ErrDraftRecoveryConflict
	}
	return DraftRecoveryResult{
		Draft: cloneDraftExecution(recorded), NextTimeout: draftTimeoutArm(recorded),
	}, nil
}

func reconcileDraftSupersede(
	recorded DraftExecution,
	command DraftSupersedeCommand,
) (DraftRecoveryResult, error) {
	if err := recorded.Validate(); err != nil || recorded.State != DraftExecutionStateSuperseded ||
		recorded.RevisionID != command.ResultRevisionID || recorded.CommandID != command.CommandID ||
		recorded.Transition == nil || recorded.Transition.Operation != DraftTransitionSupersede ||
		recorded.Transition.ActorID != command.ActorID || recorded.Transition.Reason != command.Reason {
		return DraftRecoveryResult{}, ErrDraftRecoveryConflict
	}
	return DraftRecoveryResult{Draft: cloneDraftExecution(recorded)}, nil
}
