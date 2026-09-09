package draft

import (
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func (d Execution) Validate() error {
	if err := validateDraftExecutionIdentity(d); err != nil {
		return err
	}
	if err := validateDraftFirstActorDecision(d); err != nil {
		return err
	}
	domainDraft, err := d.DomainDraft()
	if err != nil {
		return err
	}
	if err := domainDraft.Validate(); err != nil {
		return draftExecutionError("domain snapshot: %v", err)
	}
	if err := validateDraftActionRecords(d, domainDraft); err != nil {
		return err
	}
	if err := validateDraftRecoveryEvidence(d); err != nil {
		return err
	}
	if err := validateDraftTransitionEvidence(d); err != nil {
		return err
	}
	return validateDraftExecutionState(d, domainDraft)
}

func validateDraftExecutionStartCommand(command ExecutionStartCommand) error {
	ids := []uuid.UUID{
		command.DraftID, command.InitialRevisionID, command.DecisionEvidenceID,
		command.ParticipantIDs[0], command.ParticipantIDs[1], command.ServiceEpoch, command.CommandID,
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if id == uuid.Nil {
			return draftExecutionError("missing identity")
		}
		if _, duplicate := seen[id]; duplicate {
			return draftExecutionError("reused identity")
		}
		seen[id] = struct{}{}
	}
	if !domain.IsValidServerTime(command.StartedAt) || command.StartedAt.Before(command.CategoryRevision.CreatedAt) {
		return draftExecutionError("start timestamp must be server UTC after category revision")
	}
	if err := command.CategoryRevision.Validate(); err != nil ||
		command.CategoryRevision.Mode != domain.CategoryModeDraft {
		return draftExecutionError("category revision must be valid draft mode")
	}
	return nil
}

func validateDraftPlayerActionCommand(command PlayerActionCommand) error {
	ids := []uuid.UUID{
		command.DraftID, command.ExpectedRevisionID, command.ExpectedServiceEpoch,
		command.CommandID, command.ResultRevisionID, command.ActionID, command.ActorID,
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
	if command.ExpectedRevision < 1 || command.ExpectedTurn < 1 ||
		!command.Action.IsValid() || !command.Category.IsValid() {
		return domain.ErrValidation
	}
	return nil
}

func validateDraftExecutionIdentity(d Execution) error {
	if d.ID == uuid.Nil || d.SeriesID == uuid.Nil || d.RevisionID == uuid.Nil || d.Revision < 1 ||
		d.CommandID == uuid.Nil || d.ServiceEpoch == uuid.Nil || !d.Format.IsValid() ||
		d.FirstParticipantID == uuid.Nil || d.SecondParticipantID == uuid.Nil ||
		d.FirstParticipantID == d.SecondParticipantID {
		return draftExecutionError("missing identity or revision")
	}
	if (d.Revision == 1) != (d.PreviousRevisionID == uuid.Nil) || d.PreviousRevisionID == d.RevisionID {
		return draftExecutionError("invalid revision predecessor")
	}
	return nil
}

func validateDraftFirstActorDecision(d Execution) error {
	evidence := d.FirstActorDecision
	if err := evidence.Validate(); err != nil || evidence.Purpose != domain.DecisionPurposeDraftOrder ||
		evidence.OwnerID != d.ID || len(evidence.Result) != 2 ||
		evidence.Result[0] != d.FirstParticipantID.String() ||
		evidence.Result[1] != d.SecondParticipantID.String() {
		return draftExecutionError("first actor evidence does not match participants")
	}
	return nil
}

func (d Execution) DomainDraft() (domain.Draft, error) {
	state := domain.DraftStateActive
	deadline := d.TurnDeadline
	selected := []domain.Category(nil)
	if d.State == ExecutionStateCompleted {
		state = domain.DraftStateCompleted
		deadline = time.Time{}
		selected = append([]domain.Category(nil), d.SelectedCategories...)
	}
	actions := make([]domain.DraftAction, len(d.Actions))
	for index, action := range d.Actions {
		actions[index] = domain.DraftAction{
			Turn: action.Turn, ActorID: action.ActorID, Action: action.Action, Category: action.Category,
			OccurredAt: action.OccurredAt, TurnDeadline: action.ScheduledDeadline,
		}
	}
	draft := domain.Draft{
		ID: d.ID, SeriesID: d.SeriesID, Format: d.Format,
		FirstParticipantID: d.FirstParticipantID, SecondParticipantID: d.SecondParticipantID,
		Pool: append([]domain.Category(nil), d.Pool...), State: state, Turn: d.Turn,
		TurnDeadline: deadline, Actions: actions, SelectedCategories: selected,
	}
	if err := draft.Validate(); err != nil {
		return domain.Draft{}, draftExecutionError("domain snapshot: %v", err)
	}
	return draft, nil
}

func validateDraftActionRecords(d Execution, draft domain.Draft) error {
	if len(d.Actions) != len(draft.Actions) {
		return draftExecutionError("action evidence count does not match")
	}
	seenCommands := make(map[uuid.UUID]struct{}, len(d.Actions))
	seenIDs := make(map[uuid.UUID]struct{}, len(d.Actions))
	available := append([]domain.Category(nil), d.Pool...)
	for index, action := range d.Actions {
		if err := validateDraftActionIdentity(action, index, seenCommands, seenIDs); err != nil {
			return err
		}
		seenCommands[action.CommandID] = struct{}{}
		seenIDs[action.ID] = struct{}{}
		if err := validateDraftActionDecision(d.ID, action, available); err != nil {
			return err
		}
		available = slices.DeleteFunc(available, func(category domain.Category) bool {
			return category == action.Category
		})
	}
	return nil
}

func validateDraftActionIdentity(
	action ActionRecord,
	index int,
	seenCommands map[uuid.UUID]struct{},
	seenIDs map[uuid.UUID]struct{},
) error {
	if action.ID == uuid.Nil || action.ResultRevisionID == uuid.Nil || action.CommandID == uuid.Nil ||
		action.Turn != index+1 || action.ID == action.ResultRevisionID || action.ID == action.CommandID ||
		action.ResultRevisionID == action.CommandID {
		return draftExecutionError("invalid action identity")
	}
	if _, duplicate := seenCommands[action.CommandID]; duplicate {
		return draftExecutionError("duplicate action command")
	}
	if _, duplicate := seenIDs[action.ID]; duplicate {
		return draftExecutionError("duplicate action identity")
	}
	return nil
}

func validateDraftActionDecision(
	draftID uuid.UUID,
	action ActionRecord,
	available []domain.Category,
) error {
	if !action.Automatic {
		if action.DecisionEvidence != nil {
			return draftExecutionError("player action contains automatic decision evidence")
		}
		return nil
	}
	if action.DecisionEvidence == nil {
		return draftExecutionError("automatic action decision evidence is missing")
	}
	evidence := action.DecisionEvidence
	if evidence.Purpose != domain.DecisionPurposeCategory || evidence.OwnerID != draftID ||
		evidence.Validate() != nil || !evidence.DecidedAt.Equal(action.ScheduledDeadline) ||
		!slices.Equal(evidence.NormalizedInputs, categoryDecisionInputs(available)) ||
		len(evidence.Result) != len(available) || domain.Category(evidence.Result[0]) != action.Category {
		return draftExecutionError("automatic action decision evidence is invalid")
	}
	return nil
}

func validateDraftExecutionState(d Execution, draft domain.Draft) error {
	switch d.State {
	case ExecutionStateActive:
		return validateActiveDraftExecution(d, draft)
	case ExecutionStatePaused:
		return validatePausedDraftExecution(d, draft)
	case ExecutionStateRecoveryRequired:
		return validateRecoveringDraftExecution(d, draft)
	case ExecutionStateCompleted:
		return validateCompletedDraftExecution(d, draft)
	case ExecutionStateSuperseded:
		return validateSupersededDraftExecution(d)
	default:
		return draftExecutionError("unknown state %q", d.State)
	}
}

func validateActiveDraftExecution(d Execution, draft domain.Draft) error {
	if d.AbsoluteDeadline == nil || !d.AbsoluteDeadline.Equal(d.TurnDeadline) ||
		d.PausedRemaining != 0 || d.Recovery != nil {
		return draftExecutionError("invalid active deadline evidence")
	}
	return validateDraftCurrentTurn(d, draft)
}

func validatePausedDraftExecution(d Execution, draft domain.Draft) error {
	if d.AbsoluteDeadline != nil || d.PausedRemaining <= 0 || d.PausedRemaining > TurnDuration ||
		d.Recovery == nil || d.Recovery.Policy != RecoveryPolicyShiftRemaining ||
		d.Recovery.Reason != RecoveryReasonOperatorPause {
		return draftExecutionError("invalid paused evidence")
	}
	return validateDraftCurrentTurn(d, draft)
}

func validateRecoveringDraftExecution(d Execution, draft domain.Draft) error {
	if d.AbsoluteDeadline != nil || d.PausedRemaining != 0 || d.Recovery == nil ||
		d.Recovery.Policy != RecoveryPolicyFreshOnResume ||
		(d.Recovery.Reason != RecoveryReasonEpochMismatch &&
			d.Recovery.Reason != RecoveryReasonServiceRestart) {
		return draftExecutionError("invalid recovery evidence")
	}
	return validateDraftCurrentTurn(d, draft)
}

func validateCompletedDraftExecution(d Execution, draft domain.Draft) error {
	if d.AbsoluteDeadline != nil || !d.TurnDeadline.IsZero() || d.PausedRemaining != 0 ||
		d.Recovery != nil || d.CurrentActorID != nil || d.CurrentAction != nil ||
		len(d.LegalCategories) != 0 || !slices.Equal(d.SelectedCategories, draft.SelectedCategories) {
		return draftExecutionError("invalid completed evidence")
	}
	return nil
}

func validateSupersededDraftExecution(d Execution) error {
	if d.AbsoluteDeadline != nil || d.PausedRemaining != 0 || d.Recovery == nil ||
		d.Recovery.Reason != RecoveryReasonCorrection || d.CurrentActorID != nil ||
		d.CurrentAction != nil || len(d.LegalCategories) != 0 || len(d.SelectedCategories) != 0 {
		return draftExecutionError("invalid superseded evidence")
	}
	return nil
}

func validateDraftTransitionEvidence(d Execution) error {
	if d.Transition == nil {
		return nil
	}
	transition := d.Transition
	if transition.ActorID == uuid.Nil || transition.Reason == "" ||
		transition.Reason != strings.TrimSpace(transition.Reason) ||
		!domain.IsValidServerTime(transition.OccurredAt) {
		return draftExecutionError("invalid transition evidence")
	}
	switch transition.Operation {
	case TransitionPause:
		if d.State != ExecutionStatePaused {
			return draftExecutionError("pause transition has wrong state")
		}
	case TransitionResume, TransitionFreshResume:
		if d.State != ExecutionStateActive {
			return draftExecutionError("resume transition has wrong state")
		}
	case TransitionEpochRecovery:
		if d.State != ExecutionStateRecoveryRequired {
			return draftExecutionError("epoch recovery transition has wrong state")
		}
	case TransitionSupersede:
		if d.State != ExecutionStateSuperseded {
			return draftExecutionError("supersede transition has wrong state")
		}
	default:
		return draftExecutionError("unknown transition operation %q", transition.Operation)
	}
	return nil
}

func validateDraftRecoveryEvidence(d Execution) error {
	if d.Recovery == nil {
		return nil
	}
	if err := validateDraftRecoveryMetadata(d); err != nil {
		return err
	}
	switch d.Recovery.Reason {
	case RecoveryReasonOperatorPause:
		return validateOperatorPauseRecovery(d)
	case RecoveryReasonEpochMismatch, RecoveryReasonServiceRestart:
		return validateEpochRecovery(d)
	case RecoveryReasonCorrection:
		return validateDraftCorrection(d)
	default:
		return draftExecutionError("unknown recovery reason %q", d.Recovery.Reason)
	}
}

func validateDraftRecoveryMetadata(d Execution) error {
	recovery := d.Recovery
	if recovery.PreviousState == "" || recovery.PreviousServiceEpoch == uuid.Nil ||
		recovery.CurrentServiceEpoch == uuid.Nil || recovery.ActorID == uuid.Nil ||
		!domain.IsValidServerTime(recovery.PreviousDeadline) || !domain.IsValidServerTime(recovery.RecordedAt) ||
		recovery.Note == "" || recovery.Note != strings.TrimSpace(recovery.Note) ||
		recovery.CurrentServiceEpoch != d.ServiceEpoch ||
		!recovery.PreviousDeadline.Equal(d.TurnDeadline) {
		return draftExecutionError("invalid recovery identity or timestamp evidence")
	}
	if d.Transition == nil || d.Transition.ActorID != recovery.ActorID ||
		d.Transition.Reason != recovery.Note || !d.Transition.OccurredAt.Equal(recovery.RecordedAt) {
		return draftExecutionError("recovery and transition evidence do not match")
	}
	return nil
}

func validateOperatorPauseRecovery(d Execution) error {
	recovery := d.Recovery
	if d.State != ExecutionStatePaused || recovery.PreviousState != ExecutionStateActive ||
		recovery.Policy != RecoveryPolicyShiftRemaining ||
		recovery.PreviousServiceEpoch != recovery.CurrentServiceEpoch ||
		!recovery.RecordedAt.Before(recovery.PreviousDeadline) ||
		recovery.PreviousDeadline.Sub(recovery.RecordedAt) != d.PausedRemaining {
		return draftExecutionError("operator pause evidence does not match")
	}
	return nil
}

func validateEpochRecovery(d Execution) error {
	recovery := d.Recovery
	if d.State != ExecutionStateRecoveryRequired ||
		recovery.PreviousState != ExecutionStateActive ||
		recovery.Policy != RecoveryPolicyFreshOnResume ||
		recovery.PreviousServiceEpoch == recovery.CurrentServiceEpoch {
		return draftExecutionError("epoch recovery evidence does not match")
	}
	return nil
}

func validateDraftCorrection(d Execution) error {
	recovery := d.Recovery
	if d.State != ExecutionStateSuperseded ||
		(recovery.PreviousState != ExecutionStateActive &&
			recovery.PreviousState != ExecutionStatePaused) ||
		recovery.Policy != "" || recovery.PreviousServiceEpoch != recovery.CurrentServiceEpoch ||
		(recovery.PreviousState == ExecutionStateActive &&
			!recovery.RecordedAt.Before(recovery.PreviousDeadline)) {
		return draftExecutionError("correction evidence does not match")
	}
	return nil
}

func validateDraftCurrentTurn(d Execution, draft domain.Draft) error {
	turn, err := draft.CurrentTurn()
	if err != nil || d.CurrentActorID == nil || d.CurrentAction == nil ||
		*d.CurrentActorID != turn.ActorID || *d.CurrentAction != turn.Action ||
		!slices.Equal(d.LegalCategories, draftLegalCategories(draft)) {
		return draftExecutionError("current turn evidence does not match")
	}
	return nil
}
