package arena

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type DraftRoute string

const (
	DraftRouteNone     DraftRoute = ""
	DraftRouteRecovery DraftRoute = "draft_recovery"
)

var (
	ErrDraftTimeoutEarly    = errors.New("arena draft timeout is early")
	ErrDraftTimeoutConflict = errors.New("arena draft timeout conflict")
)

type DraftTimeoutCommand struct {
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

type DraftTimeoutResult struct {
	Draft       DraftExecution
	Changed     bool
	Route       DraftRoute
	NextTimeout *DraftTimeoutArm
}

type DraftTimeoutUseCase struct {
	repository DraftRepository
	clock      Clock
}

func NewDraftTimeoutUseCase(repository DraftRepository, clock Clock) *DraftTimeoutUseCase {
	return &DraftTimeoutUseCase{repository: repository, clock: clock}
}

func (u *DraftTimeoutUseCase) Resolve(
	ctx context.Context,
	command DraftTimeoutCommand,
) (DraftTimeoutResult, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return DraftTimeoutResult{}, domain.ErrValidation
	}
	if err := validateDraftTimeoutCommand(command); err != nil {
		return DraftTimeoutResult{}, err
	}
	if result, found, err := findRecordedDraftTimeout(ctx, u.repository, command); found || err != nil {
		return result, err
	}

	current, err := loadDraftExecution(ctx, u.repository, command.DraftID, "DraftTimeoutUseCase")
	if err != nil {
		return DraftTimeoutResult{}, err
	}
	if !draftTimeoutAuthorityMatches(current, command) {
		return reconcileDraftTimeoutConflict(ctx, u.repository, command)
	}
	if command.CurrentServiceEpoch != current.ServiceEpoch {
		return DraftTimeoutResult{
			Draft: cloneDraftExecution(current),
			Route: DraftRouteRecovery,
		}, nil
	}

	now := u.clock.Now()
	if !validArenaServerTime(now) {
		return DraftTimeoutResult{}, domain.ErrValidation
	}
	if now.Before(command.ExpectedDeadline) {
		return DraftTimeoutResult{}, ErrDraftTimeoutEarly
	}

	next, err := buildDraftTimeoutRevision(current, command)
	if err != nil {
		return DraftTimeoutResult{}, err
	}
	return commitDraftTimeout(ctx, u.repository, current, next, command)
}

func findRecordedDraftTimeout(
	ctx context.Context,
	repository DraftRepository,
	command DraftTimeoutCommand,
) (DraftTimeoutResult, bool, error) {
	recorded, err := repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return DraftTimeoutResult{}, true, fmt.Errorf("DraftTimeoutUseCase - find command: %w", err)
	}
	if recorded == nil {
		return DraftTimeoutResult{}, false, nil
	}
	result, err := reconcileDraftTimeout(*recorded, command)
	return result, true, err
}

func draftTimeoutAuthorityMatches(current DraftExecution, command DraftTimeoutCommand) bool {
	return current.State == DraftExecutionStateActive &&
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
	current DraftExecution,
	command DraftTimeoutCommand,
) (DraftExecution, error) {
	evidence, err := domain.NewArenaDecisionEvidence(
		command.DecisionEvidenceID,
		domain.ArenaDecisionPurposeCategory,
		domain.ArenaDecisionAlgorithmV1,
		categoryDecisionInputs(current.LegalCategories),
		current.ID,
		command.ExpectedDeadline,
	)
	if err != nil {
		return DraftExecution{}, fmt.Errorf("DraftTimeoutUseCase - category decision: %w", err)
	}
	actionCommand := DraftPlayerActionCommand{
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

func validateDraftTimeoutCommand(command DraftTimeoutCommand) error {
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
		!validArenaServerTime(command.ExpectedDeadline) {
		return domain.ErrValidation
	}
	return nil
}

func commitDraftTimeout(
	ctx context.Context,
	repository DraftRepository,
	current DraftExecution,
	next DraftExecution,
	command DraftTimeoutCommand,
) (DraftTimeoutResult, error) {
	committed, changed, err := repository.CommitDraftRevisions(
		ctx,
		draftExpectation(current),
		[]DraftExecution{next},
	)
	if errors.Is(err, domain.ErrConflict) {
		return reconcileDraftTimeoutConflict(ctx, repository, command)
	}
	if err != nil {
		return DraftTimeoutResult{}, fmt.Errorf("DraftTimeoutUseCase - commit timeout: %w", err)
	}
	if committed == nil {
		return reconcileDraftTimeoutConflict(ctx, repository, command)
	}
	result, reconcileErr := reconcileDraftTimeout(*committed, command)
	if reconcileErr != nil {
		return DraftTimeoutResult{}, domain.ErrInternal
	}
	result.Changed = changed
	return result, nil
}

func reconcileDraftTimeoutConflict(
	ctx context.Context,
	repository DraftRepository,
	command DraftTimeoutCommand,
) (DraftTimeoutResult, error) {
	recorded, err := repository.FindDraftCommand(ctx, command.DraftID, command.CommandID)
	if err != nil {
		return DraftTimeoutResult{}, fmt.Errorf("DraftTimeoutUseCase - reconcile command: %w", err)
	}
	if recorded == nil {
		return DraftTimeoutResult{}, ErrDraftTimeoutConflict
	}
	return reconcileDraftTimeout(*recorded, command)
}

func reconcileDraftTimeout(
	recorded DraftExecution,
	command DraftTimeoutCommand,
) (DraftTimeoutResult, error) {
	if err := recorded.Validate(); err != nil || recorded.CommandID != command.CommandID ||
		recorded.RevisionID != command.ResultRevisionID || recorded.ServiceEpoch != command.ExpectedServiceEpoch ||
		len(recorded.Actions) == 0 {
		return DraftTimeoutResult{}, ErrDraftTimeoutConflict
	}
	action := recorded.Actions[len(recorded.Actions)-1]
	if action.ID != command.ActionID || action.CommandID != command.CommandID ||
		action.ResultRevisionID != command.ResultRevisionID || action.Turn != command.ExpectedTurn ||
		action.ScheduledDeadline != command.ExpectedDeadline || action.OccurredAt != command.ExpectedDeadline ||
		!action.Automatic || action.DecisionEvidence == nil ||
		action.DecisionEvidence.ID != command.DecisionEvidenceID {
		return DraftTimeoutResult{}, ErrDraftTimeoutConflict
	}
	return DraftTimeoutResult{
		Draft:       cloneDraftExecution(recorded),
		NextTimeout: draftTimeoutArm(recorded),
	}, nil
}
