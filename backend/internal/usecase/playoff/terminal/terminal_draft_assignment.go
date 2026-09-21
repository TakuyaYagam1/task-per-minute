package terminal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
)

// ExactDraftPlanWorkflow keeps the final coordinator independent from a
// concrete assignment implementation while preserving the exact branch plan
// lifecycle as the only task-selection authority.
type ExactDraftPlanWorkflow interface {
	PlanAndCommit(
		ctx context.Context,
		command assignmentusecase.ExactDraftBranchPlanCommand,
	) (*assignmentusecase.ExactDraftBranchPlan, bool, error)
	ActivateCompletedBranch(
		ctx context.Context,
		command assignmentusecase.ExactDraftBranchActivationCommand,
	) (*assignmentusecase.ExactDraftBranchPlan, bool, error)
}

// ExactDraftPlanAuthorityReader provides the locked active draft and all
// candidate normal-task authorities needed to build a deterministic command.
// The workflow reloads the same authority before it commits.
type ExactDraftPlanAuthorityReader interface {
	LoadExactDraftBranchPlanAuthority(
		ctx context.Context,
		draftID uuid.UUID,
	) (assignmentusecase.ExactDraftBranchPlanAuthority, error)
}

type FinalDraftAssignmentService struct {
	workflow  ExactDraftPlanWorkflow
	authority ExactDraftPlanAuthorityReader
	committed ExactDraftCommittedPlanReader
}

func NewFinalDraftAssignmentService(
	workflow ExactDraftPlanWorkflow,
	authority ExactDraftPlanAuthorityReader,
	committed ExactDraftCommittedPlanReader,
) *FinalDraftAssignmentService {
	return &FinalDraftAssignmentService{
		workflow: workflow, authority: authority, committed: committed,
	}
}

func (s *FinalDraftAssignmentService) PlanFinalDraft(
	ctx context.Context,
	plan FinalDraftPlan,
) (bool, error) {
	if ctx == nil || plan.valid() != nil || s == nil || s.workflow == nil || s.authority == nil {
		return false, domain.ErrValidation
	}
	authority, err := s.authority.LoadExactDraftBranchPlanAuthority(ctx, plan.Draft.ID)
	if err != nil {
		return false, fmt.Errorf("FinalDraftAssignmentService - load authority: %w", err)
	}
	command, err := finalDraftAssignmentCommand(plan, authority)
	if err != nil {
		return false, err
	}
	committed, changed, err := s.workflow.PlanAndCommit(ctx, command)
	if err != nil {
		return false, fmt.Errorf("FinalDraftAssignmentService - reserve branches: %w", err)
	}
	if committed == nil || committed.Validate() != nil || committed.ID != plan.IDs.DraftAssignmentPlanID ||
		committed.RevisionID != plan.IDs.DraftAssignmentRevisionID {
		return false, domain.ErrConflict
	}
	return changed, nil
}

func (s *FinalDraftAssignmentService) ActivateFinalDraft(
	ctx context.Context,
	authority FinalDraftAuthority,
	command TerminalDraftCommand,
) ([]FinalGameBinding, bool, error) {
	if ctx == nil || authority.valid(command) != nil || s == nil || s.workflow == nil {
		return nil, false, domain.ErrValidation
	}
	activation := assignmentusecase.ExactDraftBranchActivationCommand{
		PlanID:                  authority.IDs.DraftAssignmentPlanID,
		DraftID:                 command.DraftID,
		ExpectedPlanRevisionID:  authority.IDs.DraftAssignmentRevisionID,
		ExpectedDraftRevisionID: authority.Draft.RevisionID,
		ExpectedDraftRevision:   authority.Draft.Revision,
		CommandID:               command.CommandID,
		CommittedAt:             authority.RecordedAt,
		ReleaseReason:           "completed final draft selected another branch",
	}
	activated, changed, err := s.workflow.ActivateCompletedBranch(ctx, activation)
	if err != nil {
		return nil, false, fmt.Errorf("FinalDraftAssignmentService - activate branch: %w", err)
	}
	if activated == nil || activated.Validate() != nil {
		return nil, false, domain.ErrConflict
	}
	bindings, err := finalDraftGameBindings(authority.IDs, *activated)
	if err != nil {
		return nil, false, err
	}
	return bindings, changed, nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (s *FinalDraftAssignmentService) RehydrateFinalBindings(
	ctx context.Context,
	authority FinalSettlementAuthority,
) ([]FinalGameBinding, error) {
	if ctx == nil || s == nil || s.committed == nil {
		return nil, domain.ErrValidation
	}
	if authority.validBase() != nil {
		return nil, domain.ErrConflict
	}
	prefix, err := finalSettlementBindingPrefix(authority)
	if err != nil {
		return nil, err
	}
	if err := validateMaterializedFinalBindingPrefix(authority.IDs, authority.Bindings, prefix); err != nil {
		return nil, err
	}
	plan, err := s.committed.LoadCommittedExactDraftPlan(ctx, authority.IDs.DraftAssignmentPlanID)
	if err != nil {
		if errors.Is(err, assignmentusecase.ErrExactDraftBranchPlanNotFound) {
			return nil, domain.ErrConflict
		}
		return nil, fmt.Errorf("FinalDraftAssignmentService - load committed plan: %w", err)
	}
	if plan == nil || plan.Validate() != nil || plan.ID != authority.IDs.DraftAssignmentPlanID ||
		plan.RevisionID != authority.IDs.DraftAssignmentRevisionID ||
		plan.SourceDraft.ID != authority.Draft.ID || plan.SourceDraft.SeriesID != authority.IDs.FinalSeriesID ||
		plan.CompletionDraftRevisionID != authority.Draft.RevisionID ||
		plan.CompletionDraftRevision != authority.Draft.Revision ||
		plan.State != assignmentusecase.ExactDraftBranchPlanStateCommitted {
		return nil, domain.ErrConflict
	}
	bindings, err := finalDraftGameBindings(authority.IDs, *plan)
	if err != nil {
		return nil, err
	}
	return reconcileFinalBindings(authority.IDs, bindings, authority.Bindings, prefix)
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func finalDraftAssignmentCommand(
	plan FinalDraftPlan,
	authority assignmentusecase.ExactDraftBranchPlanAuthority,
) (assignmentusecase.ExactDraftBranchPlanCommand, error) {
	if authority.Draft.Validate() != nil || authority.Draft.ID != plan.Draft.ID ||
		authority.Draft.RevisionID != plan.Draft.RevisionID || authority.Draft.Revision != plan.Draft.Revision ||
		authority.Draft.SeriesID != plan.Series.ID {
		return assignmentusecase.ExactDraftBranchPlanCommand{}, domain.ErrConflict
	}
	command := assignmentusecase.ExactDraftBranchPlanCommand{
		PlanID:                  plan.IDs.DraftAssignmentPlanID,
		PlanRevisionID:          plan.IDs.DraftAssignmentRevisionID,
		DraftID:                 plan.Draft.ID,
		ExpectedDraftRevisionID: plan.Draft.RevisionID,
		ExpectedDraftRevision:   plan.Draft.Revision,
		Branches:                make([]assignmentusecase.ExactDraftBranchCommand, len(authority.Branches)),
		CreatedAt:               plan.CreatedAt,
	}
	for branchIndex, branch := range authority.Branches {
		if branch.Key == "" || len(branch.Assignments) != 3 {
			return assignmentusecase.ExactDraftBranchPlanCommand{}, domain.ErrConflict
		}
		branchCommand := assignmentusecase.ExactDraftBranchCommand{
			BranchID:    plan.IDs.DraftAssignmentBranchID(branch.Key),
			Key:         branch.Key,
			Assignments: make([]assignmentusecase.ExactNormalAssignmentCommand, 3),
		}
		for assignmentIndex, exactAuthority := range branch.Assignments {
			position := assignmentIndex + 1
			branchCommand.ChildBranchIDs[assignmentIndex] = plan.IDs.DraftAssignmentChildBranchID(
				branch.Key,
				position,
			)
			slotID, ok := finalSlotID(plan.IDs, position)
			if !ok || exactAuthority.Scope.TournamentID != plan.Series.TournamentID ||
				exactAuthority.Scope.RosterID != plan.RosterID || exactAuthority.Scope.SeriesID != plan.Series.ID ||
				exactAuthority.Scope.SlotID != slotID || exactAuthority.Scope.CategoryLockID != plan.Category.ID {
				return assignmentusecase.ExactDraftBranchPlanCommand{}, domain.ErrConflict
			}
			branchCommand.Assignments[assignmentIndex] = assignmentusecase.ExactNormalAssignmentCommand{
				Scope:              exactAuthority.Scope,
				PlanID:             plan.IDs.DraftAssignmentPlanID,
				PlanRevisionID:     plan.IDs.DraftAssignmentRevisionID,
				BranchID:           branchCommand.ChildBranchIDs[assignmentIndex],
				DecisionEvidenceID: plan.IDs.DraftAssignmentDecisionID(branch.Key, position),
				EdgeIDs:            finalDraftEdgeIDs(plan.IDs, branch.Key, position, plan.ReserveCount),
				ReservationIDs:     finalDraftReservationIDs(plan.IDs, branch.Key, position, plan.ReserveCount),
				SnapshotIDs:        finalDraftSnapshotIDs(plan.IDs, branch.Key, position, plan.ReserveCount),
				CreatedAt:          plan.CreatedAt,
			}
		}
		command.Branches[branchIndex] = branchCommand
	}
	return command, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func finalDraftGameBindings(
	ids FinalStageIDs,
	plan assignmentusecase.ExactDraftBranchPlan,
) ([]FinalGameBinding, error) {
	if !ids.Valid() || plan.Validate() != nil || plan.State != assignmentusecase.ExactDraftBranchPlanStateCommitted {
		return nil, domain.ErrConflict
	}
	for _, branch := range plan.Branches {
		if branch.ID != plan.ActiveBranchID {
			continue
		}
		if branch.State != assignmentusecase.ExactDraftBranchStateActive || len(branch.Assignments) != 3 {
			return nil, domain.ErrConflict
		}
		reserveCount := -1
		bindings := make([]FinalGameBinding, len(branch.Assignments))
		for index, assignment := range branch.Assignments {
			position := index + 1
			gameID, ok := finalGameID(ids, position)
			if !ok || assignment.Position != position || assignment.State != assignmentusecase.ExactDraftReservationStateCommitted ||
				assignment.Plan.Validate() != nil || len(assignment.Plan.SelectedEdges) < 1 || len(assignment.Plan.SelectedEdges) > domain.MaxAssignmentReserveCount+1 {
				return nil, domain.ErrConflict
			}
			if reserveCount == -1 {
				reserveCount = len(assignment.Plan.SelectedEdges) - 1
			} else if reserveCount != len(assignment.Plan.SelectedEdges)-1 {
				return nil, domain.ErrConflict
			}
			if assignment.Plan.BranchID != ids.DraftAssignmentChildBranchID(branch.Path.Key, position) {
				return nil, domain.ErrConflict
			}
			primary := assignment.Plan.SelectedEdges[0]
			if primary.Position != 1 || primary.Snapshot.Validate() != nil {
				return nil, domain.ErrConflict
			}
			bindings[index] = FinalGameBinding{
				GameID:             gameID,
				AssignmentID:       ids.GameAssignmentID(position),
				AssignmentRevision: 1,
				PlanID:             plan.ID,
				PlanRevisionID:     plan.RevisionID,
				BranchID:           assignment.Plan.BranchID,
				ReservationID:      primary.ReservationID,
				SnapshotID:         primary.Snapshot.SnapshotID,
				ContentDigest:      primary.ContentDigest,
				DeadlineSeconds:    int(domain.TournamentTaskDuration / time.Second),
			}
		}
		if !hasFinalBindings(bindings, ids) {
			return nil, domain.ErrConflict
		}
		return bindings, nil
	}
	return nil, domain.ErrConflict
}

func reconcileFinalBindings(
	ids FinalStageIDs,
	planned []FinalGameBinding,
	actual []FinalGameBinding,
	materializedPrefix int,
) ([]FinalGameBinding, error) {
	if !hasFinalBindings(planned, ids) || materializedPrefix < 1 || materializedPrefix > len(planned) ||
		len(actual) != materializedPrefix {
		return nil, domain.ErrConflict
	}
	for index, binding := range actual {
		if !binding.valid() || binding != planned[index] {
			return nil, domain.ErrConflict
		}
	}
	return append([]FinalGameBinding(nil), planned...), nil
}

func finalSettlementBindingPrefix(authority FinalSettlementAuthority) (int, error) {
	currentID := authority.Progression.Progression.Game.ID
	var prefix int
	switch currentID {
	case authority.IDs.FirstGameID:
		prefix = 1
	case authority.IDs.SecondGameID:
		prefix = 2
	case authority.IDs.ThirdGameID:
		prefix = 3
	default:
		return 0, domain.ErrConflict
	}
	if len(authority.History) != prefix-1 {
		return 0, domain.ErrConflict
	}
	for index, progression := range authority.History {
		expectedID, found := finalGameID(authority.IDs, index+1)
		if !found || progression.Progression.Game.ID != expectedID {
			return 0, domain.ErrConflict
		}
	}
	return prefix, nil
}

func validateMaterializedFinalBindingPrefix(
	ids FinalStageIDs,
	bindings []FinalGameBinding,
	prefix int,
) error {
	if prefix < 1 || prefix > 3 || len(bindings) != prefix {
		return domain.ErrConflict
	}
	gameIDs := [...]uuid.UUID{ids.FirstGameID, ids.SecondGameID, ids.ThirdGameID}
	for index, binding := range bindings {
		position := index + 1
		if !binding.valid() || binding.GameID != gameIDs[index] ||
			binding.AssignmentID != ids.GameAssignmentID(position) ||
			binding.PlanID != ids.DraftAssignmentPlanID ||
			binding.PlanRevisionID != ids.DraftAssignmentRevisionID {
			return domain.ErrConflict
		}
	}
	return nil
}

func finalSlotID(ids FinalStageIDs, position int) (uuid.UUID, bool) {
	switch position {
	case 1:
		return ids.FirstSlotID, true
	case 2:
		return ids.SecondSlotID, true
	case 3:
		return ids.ThirdSlotID, true
	default:
		return uuid.Nil, false
	}
}

func finalGameID(ids FinalStageIDs, position int) (uuid.UUID, bool) {
	switch position {
	case 1:
		return ids.FirstGameID, true
	case 2:
		return ids.SecondGameID, true
	case 3:
		return ids.ThirdGameID, true
	default:
		return uuid.Nil, false
	}
}

func finalDraftEdgeIDs(ids FinalStageIDs, key string, position, reserveCount int) [domain.AssignmentReserveCount + 1]uuid.UUID {
	var result [domain.AssignmentReserveCount + 1]uuid.UUID
	if !domain.IsValidAssignmentReserveCount(reserveCount) {
		return result
	}
	for index := 0; index <= reserveCount; index++ {
		result[index] = ids.DraftAssignmentEdgeID(key, position, index+1)
	}
	return result
}

func finalDraftReservationIDs(ids FinalStageIDs, key string, position, reserveCount int) [domain.AssignmentReserveCount + 1]uuid.UUID {
	var result [domain.AssignmentReserveCount + 1]uuid.UUID
	if !domain.IsValidAssignmentReserveCount(reserveCount) {
		return result
	}
	for index := 0; index <= reserveCount; index++ {
		result[index] = ids.DraftAssignmentReservationID(key, position, index+1)
	}
	return result
}

func finalDraftSnapshotIDs(ids FinalStageIDs, key string, position, reserveCount int) [domain.AssignmentReserveCount + 1]uuid.UUID {
	var result [domain.AssignmentReserveCount + 1]uuid.UUID
	if !domain.IsValidAssignmentReserveCount(reserveCount) {
		return result
	}
	for index := 0; index <= reserveCount; index++ {
		result[index] = ids.DraftAssignmentSnapshotID(key, position, index+1)
	}
	return result
}
