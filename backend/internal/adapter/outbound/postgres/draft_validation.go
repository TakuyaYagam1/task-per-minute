package postgres

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validateDraftCreateInput(in DraftCreateInput) error {
	if !validDraftCreateIdentity(in) || !validDraftCreateEvidence(in) {
		return domain.ErrValidation
	}
	if _, err := domain.NewDraft(
		in.ID,
		in.SeriesID,
		in.Format,
		in.FirstParticipantID,
		in.SecondParticipantID,
		in.Pool,
		in.AbsoluteDeadline,
	); err != nil {
		return domain.WrapError(err, domain.ErrValidation)
	}
	return nil
}

func validDraftCreateIdentity(in DraftCreateInput) bool {
	return in.ID != uuid.Nil && in.SeriesID != uuid.Nil && in.RosterID != uuid.Nil &&
		in.CategoryRevisionID != uuid.Nil && in.CategoryRevision >= 1 && in.SourcePoolRevision != uuid.Nil &&
		in.InitialRevisionID != uuid.Nil && in.CommandID != uuid.Nil && in.ServiceEpoch != uuid.Nil
}

func validDraftCreateEvidence(in DraftCreateInput) bool {
	if !validServerTime(in.CreatedAt) || !validServerTime(in.AbsoluteDeadline) ||
		!in.AbsoluteDeadline.After(in.CreatedAt) {
		return false
	}
	if err := in.DecisionEvidence.Validate(); err != nil {
		return false
	}
	return in.DecisionEvidence.Purpose == domain.DecisionPurposeDraftOrder &&
		in.DecisionEvidence.OwnerID == in.ID && !in.DecisionEvidence.DecidedAt.After(in.CreatedAt)
}

func validateDraftRevisionInput(
	draftID uuid.UUID,
	expected DraftRevisionExpectation,
	in DraftRevisionInput,
) error {
	if !validDraftRevisionIdentity(draftID, expected, in) {
		return domain.ErrValidation
	}
	if !validDraftRevisionState(in) || !validDraftActionInput(in) ||
		!validDraftRevisionDecision(draftID, in) {
		return domain.ErrValidation
	}
	return nil
}

func validDraftRevisionIdentity(
	draftID uuid.UUID,
	expected DraftRevisionExpectation,
	in DraftRevisionInput,
) bool {
	return draftID != uuid.Nil && expected.ID != uuid.Nil && expected.Revision >= 1 &&
		expected.ServiceEpoch != uuid.Nil && in.ID != uuid.Nil && in.CommandID != uuid.Nil &&
		in.ServiceEpoch != uuid.Nil && in.State.IsValid() && in.TurnNumber >= 1 && in.TurnNumber <= 4 &&
		validServerTime(in.CreatedAt)
}

func validDraftRevisionState(in DraftRevisionInput) bool {
	if (in.CurrentActorID == nil) != (in.CurrentAction == nil) {
		return false
	}
	if in.CurrentActorID != nil && (*in.CurrentActorID == uuid.Nil || !in.CurrentAction.IsValid()) {
		return false
	}
	if in.AbsoluteDeadline != nil && !validServerTime(*in.AbsoluteDeadline) {
		return false
	}
	if in.PausedRemainingMS != nil && (*in.PausedRemainingMS < 1 || *in.PausedRemainingMS > 15000) {
		return false
	}
	for _, category := range in.SelectedCategories {
		if !category.IsValid() {
			return false
		}
	}
	return true
}

func validDraftActionInput(in DraftRevisionInput) bool {
	if in.Action == nil {
		return true
	}
	action := in.Action
	if action.ID == uuid.Nil || action.TurnNumber < 1 || action.TurnNumber > 4 || action.ActorID == uuid.Nil ||
		!action.Action.IsValid() || !action.Category.IsValid() || !validServerTime(action.ScheduledDeadline) ||
		!validServerTime(action.OccurredAt) || action.OccurredAt.After(action.ScheduledDeadline) {
		return false
	}
	if in.State == DraftPersistenceStateActive {
		return action.TurnNumber+1 == in.TurnNumber
	}
	return in.State == DraftPersistenceStateCompleted && action.TurnNumber == in.TurnNumber
}

func validDraftRevisionDecision(draftID uuid.UUID, in DraftRevisionInput) bool {
	if in.DecisionEvidence != nil {
		if err := in.DecisionEvidence.Validate(); err != nil || in.DecisionEvidence.OwnerID != draftID ||
			in.DecisionEvidence.DecidedAt.After(in.CreatedAt) {
			return false
		}
	}
	return in.Action == nil || !in.Action.Automatic ||
		(in.DecisionEvidence != nil && in.DecisionEvidence.Purpose == domain.DecisionPurposeCategory)
}

func (state DraftPersistenceState) IsValid() bool {
	switch state {
	case DraftPersistenceStateActive,
		DraftPersistenceStatePaused,
		DraftPersistenceStateRecoveryRequired,
		DraftPersistenceStateCompleted,
		DraftPersistenceStateSuperseded:
		return true
	default:
		return false
	}
}
