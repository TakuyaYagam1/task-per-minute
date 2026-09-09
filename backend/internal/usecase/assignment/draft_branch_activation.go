package assignment

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func (u *ExactDraftBranchPlanUseCase) ActivateCompletedBranch(
	ctx context.Context,
	command ExactDraftBranchActivationCommand,
) (*ExactDraftBranchPlan, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	command.ReleaseReason = strings.TrimSpace(command.ReleaseReason)
	if err := validateExactDraftBranchActivationCommand(command); err != nil {
		return nil, false, err
	}

	for range exactDraftBranchPlanAttempts {
		current, completed, err := u.repository.LoadExactDraftBranchActivation(ctx, command.PlanID)
		if err != nil {
			return nil, false, fmt.Errorf("ExactDraftBranchPlanUseCase - load activation: %w", err)
		}
		if current == nil || completed == nil {
			return nil, false, ErrExactDraftBranchPlanNotFound
		}
		if current.State == ExactDraftBranchPlanStateCommitted {
			result, err := reconcileExactDraftBranchActivation(*current, command)
			return result, false, err
		}
		next, err := activateExactDraftBranch(*current, *completed, command)
		if err != nil {
			return nil, false, err
		}
		committed, changed, err := u.repository.CommitExactDraftBranchActivation(ctx, next)
		if errors.Is(err, domain.ErrConflict) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("ExactDraftBranchPlanUseCase - commit activation: %w", err)
		}
		if committed == nil || committed.Validate() != nil {
			return nil, false, domain.ErrInternal
		}
		result, reconcileErr := reconcileExactDraftBranchActivation(*committed, command)
		if reconcileErr != nil {
			return nil, false, domain.ErrInternal
		}
		return result, changed, nil
	}
	return nil, false, ErrExactDraftBranchPlanConflict
}

func activateExactDraftBranch(
	current ExactDraftBranchPlan,
	completed draftusecase.Execution,
	command ExactDraftBranchActivationCommand,
) (ExactDraftBranchPlan, error) {
	if !validExactDraftPlanActivationSource(current, command) {
		return ExactDraftBranchPlan{}, ErrExactDraftBranchPlanConflict
	}
	if !validExactDraftCompletion(current.SourceDraft, completed, command) {
		return ExactDraftBranchPlan{}, ErrExactDraftBranchPlanConflict
	}
	completedPath := exactDraftBranchPathFromExecution(completed)
	activeIndex := exactDraftActiveBranchIndex(current.Branches, completedPath)
	if activeIndex < 0 {
		return ExactDraftBranchPlan{}, exactDraftBranchPlanError("completed branch was not reserved")
	}

	next := cloneExactDraftBranchPlan(current)
	applyExactDraftPlanCompletion(&next, completed, command, activeIndex)
	if err := next.Validate(); err != nil {
		return ExactDraftBranchPlan{}, err
	}
	return next, nil
}

func validExactDraftPlanActivationSource(
	current ExactDraftBranchPlan,
	command ExactDraftBranchActivationCommand,
) bool {
	return current.Validate() == nil && current.State == ExactDraftBranchPlanStatePlanned &&
		current.ID == command.PlanID && current.RevisionID == command.ExpectedPlanRevisionID
}

func validExactDraftCompletion(
	source draftusecase.Execution,
	completed draftusecase.Execution,
	command ExactDraftBranchActivationCommand,
) bool {
	if completed.Validate() != nil || completed.State != draftusecase.ExecutionStateCompleted {
		return false
	}
	if completed.ID != command.DraftID || completed.ID != source.ID ||
		completed.RevisionID != command.ExpectedDraftRevisionID ||
		completed.Revision != command.ExpectedDraftRevision {
		return false
	}
	lastAction := completed.Actions[len(completed.Actions)-1]
	return !command.CommittedAt.Before(lastAction.OccurredAt) &&
		exactDraftCompletionExtendsSource(source, completed)
}

func exactDraftActiveBranchIndex(
	branches []ExactDraftBranch,
	completed ExactDraftBranchPath,
) int {
	for index, branch := range branches {
		if equalExactDraftBranchPath(branch.Path, completed) {
			return index
		}
	}
	return -1
}

func applyExactDraftPlanCompletion(
	plan *ExactDraftBranchPlan,
	completed draftusecase.Execution,
	command ExactDraftBranchActivationCommand,
	activeIndex int,
) {
	if plan == nil {
		return
	}
	plan.State = ExactDraftBranchPlanStateCommitted
	plan.ActiveBranchID = plan.Branches[activeIndex].ID
	plan.CompletionDraftRevisionID = completed.RevisionID
	plan.CompletionDraftRevision = completed.Revision
	plan.ActivationCommandID = command.CommandID
	plan.CompletedCategories = append([]domain.Category(nil), completed.SelectedCategories...)
	plan.CommittedAt = command.CommittedAt
	for index := range plan.Branches {
		if index == activeIndex {
			activateExactDraftBranchReservations(&plan.Branches[index], command.CommittedAt)
			continue
		}
		releaseExactDraftBranchReservations(
			&plan.Branches[index],
			command.CommittedAt,
			command.ReleaseReason,
		)
	}
}

func activateExactDraftBranchReservations(branch *ExactDraftBranch, committedAt time.Time) {
	branch.State = ExactDraftBranchStateActive
	branch.ActivatedAt = committedAt
	for index := range branch.Assignments {
		branch.Assignments[index].State = ExactDraftReservationStateCommitted
		branch.Assignments[index].TransitionedAt = committedAt
	}
}

func releaseExactDraftBranchReservations(
	branch *ExactDraftBranch,
	committedAt time.Time,
	reason string,
) {
	branch.State = ExactDraftBranchStateReleased
	branch.ReleasedAt = committedAt
	branch.ReleaseReason = reason
	for index := range branch.Assignments {
		branch.Assignments[index].State = ExactDraftReservationStateReleased
		branch.Assignments[index].TransitionedAt = committedAt
	}
}

func validateExactDraftBranchActivationCommand(command ExactDraftBranchActivationCommand) error {
	ids := []uuid.UUID{
		command.PlanID, command.DraftID, command.ExpectedPlanRevisionID,
		command.ExpectedDraftRevisionID, command.CommandID,
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
	if command.ExpectedDraftRevision < 1 || !domain.IsValidServerTime(command.CommittedAt) ||
		command.ReleaseReason == "" || command.ReleaseReason != strings.TrimSpace(command.ReleaseReason) {
		return domain.ErrValidation
	}
	return nil
}

func reconcileExactDraftBranchActivation(
	recorded ExactDraftBranchPlan,
	command ExactDraftBranchActivationCommand,
) (*ExactDraftBranchPlan, error) {
	if err := recorded.Validate(); err != nil || recorded.State != ExactDraftBranchPlanStateCommitted ||
		recorded.ID != command.PlanID || recorded.RevisionID != command.ExpectedPlanRevisionID ||
		recorded.SourceDraft.ID != command.DraftID ||
		recorded.CompletionDraftRevisionID != command.ExpectedDraftRevisionID ||
		recorded.CompletionDraftRevision != command.ExpectedDraftRevision ||
		recorded.ActivationCommandID != command.CommandID || !recorded.CommittedAt.Equal(command.CommittedAt) {
		return nil, ErrExactDraftBranchPlanConflict
	}
	for _, branch := range recorded.Branches {
		if branch.State == ExactDraftBranchStateReleased && branch.ReleaseReason != command.ReleaseReason {
			return nil, ErrExactDraftBranchPlanConflict
		}
	}
	result := cloneExactDraftBranchPlan(recorded)
	return &result, nil
}

func exactDraftCompletionExtendsSource(source, completed draftusecase.Execution) bool {
	if !sameExactDraftIdentity(source, completed) || len(source.Actions) > len(completed.Actions) {
		return false
	}
	for index := range source.Actions {
		if !sameExactDraftAction(source.Actions[index], completed.Actions[index]) {
			return false
		}
	}
	return true
}

func sameExactDraftIdentity(first, second draftusecase.Execution) bool {
	return first.ID == second.ID && first.SeriesID == second.SeriesID && first.Format == second.Format &&
		first.FirstParticipantID == second.FirstParticipantID &&
		first.SecondParticipantID == second.SecondParticipantID &&
		slices.Equal(first.Pool, second.Pool) &&
		equalExactDraftDecisionEvidence(first.FirstActorDecision, second.FirstActorDecision)
}

func sameExactDraftAction(first, second draftusecase.ActionRecord) bool {
	if first.ID != second.ID || first.ResultRevisionID != second.ResultRevisionID ||
		first.CommandID != second.CommandID || first.Turn != second.Turn ||
		first.ActorID != second.ActorID || first.Action != second.Action || first.Category != second.Category {
		return false
	}
	return first.OccurredAt.Equal(second.OccurredAt) &&
		first.ScheduledDeadline.Equal(second.ScheduledDeadline) && first.Automatic == second.Automatic &&
		equalExactDraftDecisionEvidencePointer(first.DecisionEvidence, second.DecisionEvidence)
}

func equalExactDraftDecisionEvidencePointer(
	first *domain.DecisionEvidence,
	second *domain.DecisionEvidence,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return equalExactDraftDecisionEvidence(*first, *second)
}

func equalExactDraftDecisionEvidence(
	first domain.DecisionEvidence,
	second domain.DecisionEvidence,
) bool {
	return first.ID == second.ID && first.Purpose == second.Purpose &&
		first.AlgorithmVersion == second.AlgorithmVersion && first.Seed == second.Seed &&
		first.ReplayDigest == second.ReplayDigest && first.OwnerID == second.OwnerID &&
		first.DecidedAt.Equal(second.DecidedAt) &&
		slices.Equal(first.NormalizedInputs, second.NormalizedInputs) &&
		slices.Equal(first.Result, second.Result)
}

func exactDraftBranchPathFromExecution(draft draftusecase.Execution) ExactDraftBranchPath {
	actions := make([]ExactDraftBranchAction, len(draft.Actions))
	parts := make([]string, len(draft.Actions))
	for index, action := range draft.Actions {
		actions[index] = ExactDraftBranchAction{
			Turn: action.Turn, ActorID: action.ActorID, Action: action.Action, Category: action.Category,
		}
		parts[index] = fmt.Sprintf("%d:%s:%s", action.Turn, action.Action, action.Category)
	}
	return ExactDraftBranchPath{
		Key: strings.Join(parts, "/"), Actions: actions,
		Categories: append([]domain.Category(nil), draft.SelectedCategories...),
	}
}
