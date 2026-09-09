package draft

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type Route string

const (
	RouteNone     Route = ""
	RouteRecovery Route = "draft_recovery"
)

var (
	ErrTimeoutEarly    = errors.New("draft timeout is early")
	ErrTimeoutConflict = errors.New("draft timeout conflict")
)

type TimeoutCommand struct {
	DraftID              uuid.UUID
	ExpectedRevisionID   uuid.UUID
	ExpectedRevision     int64
	ExpectedServiceEpoch uuid.UUID
	CurrentServiceEpoch  uuid.UUID
	ExpectedTurn         int
	ExpectedDeadline     time.Time
	CommandID            uuid.UUID
	ResultRevisionID     uuid.UUID
	ActionID             uuid.UUID
	DecisionEvidenceID   uuid.UUID
}

type TimeoutResult struct {
	Draft       Execution
	Changed     bool
	Route       Route
	NextTimeout *TimeoutArm
}

type TimeoutUseCase struct {
	repository Repository
	clock      Clock
}

func NewTimeoutUseCase(repository Repository, clock Clock) *TimeoutUseCase {
	return &TimeoutUseCase{repository: repository, clock: clock}
}

func (u *TimeoutUseCase) Resolve(
	ctx context.Context,
	command TimeoutCommand,
) (TimeoutResult, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return TimeoutResult{}, domain.ErrValidation
	}
	if err := validateDraftTimeoutCommand(command); err != nil {
		return TimeoutResult{}, err
	}
	if result, found, err := findRecordedDraftTimeout(ctx, u.repository, command); found || err != nil {
		return result, err
	}

	current, err := loadDraftExecution(ctx, u.repository, command.DraftID, "TimeoutUseCase")
	if err != nil {
		return TimeoutResult{}, err
	}
	if !draftTimeoutAuthorityMatches(current, command) {
		return reconcileDraftTimeoutConflict(ctx, u.repository, command)
	}
	if command.CurrentServiceEpoch != current.ServiceEpoch {
		return TimeoutResult{
			Draft: cloneDraftExecution(current),
			Route: RouteRecovery,
		}, nil
	}

	now := u.clock.Now()
	if !domain.IsValidServerTime(now) {
		return TimeoutResult{}, domain.ErrValidation
	}
	if now.Before(command.ExpectedDeadline) {
		return TimeoutResult{}, ErrTimeoutEarly
	}

	next, err := buildDraftTimeoutRevision(current, command)
	if err != nil {
		return TimeoutResult{}, err
	}
	return commitDraftTimeout(ctx, u.repository, current, next, command)
}

func findRecordedDraftTimeout(
	ctx context.Context,
	repository Repository,
	command TimeoutCommand,
) (TimeoutResult, bool, error) {
	recorded, err := repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return TimeoutResult{}, true, fmt.Errorf("TimeoutUseCase - find command: %w", err)
	}
	if recorded == nil {
		return TimeoutResult{}, false, nil
	}
	result, err := reconcileDraftTimeout(*recorded, command)
	return result, true, err
}

func draftTimeoutAuthorityMatches(current Execution, command TimeoutCommand) bool {
	return current.State == ExecutionStateActive &&
		draftExpectationMatches(
			current,
			command.ExpectedRevisionID,
			command.ExpectedRevision,
			command.ExpectedServiceEpoch,
		) &&
		current.Turn == command.ExpectedTurn && current.AbsoluteDeadline != nil &&
		current.AbsoluteDeadline.Equal(command.ExpectedDeadline)
}

func buildDraftTimeoutRevision(
	current Execution,
	command TimeoutCommand,
) (Execution, error) {
	evidence, err := domain.NewDecisionEvidence(
		command.DecisionEvidenceID,
		domain.DecisionPurposeCategory,
		domain.DecisionAlgorithmV1,
		categoryDecisionInputs(current.LegalCategories),
		current.ID,
		command.ExpectedDeadline,
	)
	if err != nil {
		return Execution{}, fmt.Errorf("TimeoutUseCase - category decision: %w", err)
	}
	actionCommand := PlayerActionCommand{
		DraftID: current.ID, ExpectedRevisionID: current.RevisionID,
		ExpectedRevision: current.Revision, ExpectedServiceEpoch: current.ServiceEpoch,
		ExpectedTurn: current.Turn, CommandID: command.CommandID,
		ResultRevisionID: command.ResultRevisionID, ActionID: command.ActionID,
		ActorID: *current.CurrentActorID, Action: *current.CurrentAction,
		Category: domain.Category(evidence.Result[0]),
	}
	return applyDraftAction(
		current,
		actionCommand,
		command.ExpectedDeadline,
		true,
		&evidence,
	)
}

func validateDraftTimeoutCommand(command TimeoutCommand) error {
	ids := []uuid.UUID{
		command.DraftID, command.ExpectedRevisionID, command.ExpectedServiceEpoch,
		command.CurrentServiceEpoch, command.CommandID, command.ResultRevisionID,
		command.ActionID, command.DecisionEvidenceID,
	}
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for index, id := range ids {
		if id == uuid.Nil {
			return domain.ErrValidation
		}
		if index == 3 && id == command.ExpectedServiceEpoch {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			return domain.ErrValidation
		}
		seen[id] = struct{}{}
	}
	if command.ExpectedRevision < 1 || command.ExpectedTurn < 1 ||
		!domain.IsValidServerTime(command.ExpectedDeadline) {
		return domain.ErrValidation
	}
	return nil
}

func commitDraftTimeout(
	ctx context.Context,
	repository Repository,
	current Execution,
	next Execution,
	command TimeoutCommand,
) (TimeoutResult, error) {
	committed, changed, err := repository.CommitDraftRevisions(
		ctx,
		draftExpectation(current),
		[]Execution{next},
	)
	if errors.Is(err, domain.ErrConflict) {
		return reconcileDraftTimeoutConflict(ctx, repository, command)
	}
	if err != nil {
		return TimeoutResult{}, fmt.Errorf("TimeoutUseCase - commit timeout: %w", err)
	}
	if committed == nil {
		return reconcileDraftTimeoutConflict(ctx, repository, command)
	}
	result, reconcileErr := reconcileDraftTimeout(*committed, command)
	if reconcileErr != nil {
		return TimeoutResult{}, domain.ErrInternal
	}
	result.Changed = changed
	return result, nil
}

func reconcileDraftTimeoutConflict(
	ctx context.Context,
	repository Repository,
	command TimeoutCommand,
) (TimeoutResult, error) {
	recorded, err := repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return TimeoutResult{}, fmt.Errorf("TimeoutUseCase - reconcile command: %w", err)
	}
	if recorded == nil {
		return TimeoutResult{}, ErrTimeoutConflict
	}
	return reconcileDraftTimeout(*recorded, command)
}

func reconcileDraftTimeout(
	recorded Execution,
	command TimeoutCommand,
) (TimeoutResult, error) {
	if err := recorded.Validate(); err != nil || recorded.CommandID != command.CommandID ||
		recorded.RevisionID != command.ResultRevisionID || recorded.ServiceEpoch != command.ExpectedServiceEpoch ||
		len(recorded.Actions) == 0 {
		return TimeoutResult{}, ErrTimeoutConflict
	}
	action := recorded.Actions[len(recorded.Actions)-1]
	if action.ID != command.ActionID || action.CommandID != command.CommandID ||
		action.ResultRevisionID != command.ResultRevisionID || action.Turn != command.ExpectedTurn ||
		action.ScheduledDeadline != command.ExpectedDeadline || action.OccurredAt != command.ExpectedDeadline ||
		!action.Automatic || action.DecisionEvidence == nil ||
		action.DecisionEvidence.ID != command.DecisionEvidenceID {
		return TimeoutResult{}, ErrTimeoutConflict
	}
	return TimeoutResult{
		Draft:       cloneDraftExecution(recorded),
		NextTimeout: draftTimeoutArm(recorded),
	}, nil
}
