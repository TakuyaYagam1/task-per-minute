package assignment

import (
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"

	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func (p ExactDraftBranchPlan) Validate() error {
	if err := validateExactDraftBranchPlanIdentity(p); err != nil {
		return err
	}
	paths, err := ReachableExactDraftBranches(p.SourceDraft)
	if err != nil || len(paths) != len(p.Branches) || len(paths) == 0 {
		return exactDraftBranchPlanError("branch set does not cover the source draft")
	}
	if err := validateExactDraftBranchPlanContents(p, paths); err != nil {
		return err
	}
	if err := validateExactDraftBranchPlanLifecycle(p); err != nil {
		return err
	}
	want := exactDraftBranchPlanProofHash(p)
	if !capacity.ValidProofDigest(p.ProofHash) || p.ProofHash != want {
		return exactDraftBranchPlanError("proof hash does not match branch reservations")
	}
	return nil
}

func validateExactDraftBranchPlanIdentity(plan ExactDraftBranchPlan) error {
	if plan.ID == uuid.Nil || plan.RevisionID == uuid.Nil || plan.ID == plan.RevisionID {
		return exactDraftBranchPlanError("invalid plan identity")
	}
	if !domain.IsValidServerTime(plan.CreatedAt) || plan.SourceDraft.Validate() != nil {
		return exactDraftBranchPlanError("invalid plan timestamp or source draft")
	}
	if plan.SourceDraft.State == draftusecase.ExecutionStateCompleted ||
		plan.SourceDraft.State == draftusecase.ExecutionStateSuperseded ||
		plan.CreatedAt.Before(plan.SourceDraft.FirstActorDecision.DecidedAt) {
		return exactDraftBranchPlanError("source draft cannot be planned")
	}
	return nil
}

func validateExactDraftBranchPlanCommand(command ExactDraftBranchPlanCommand) error {
	if err := validateExactDraftBranchPlanCommandIdentity(command); err != nil {
		return err
	}
	seen := map[uuid.UUID]struct{}{command.PlanID: {}, command.PlanRevisionID: {}}
	for _, branch := range command.Branches {
		if err := validateExactDraftBranchCommand(command, branch, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateExactDraftBranchPlanCommandIdentity(command ExactDraftBranchPlanCommand) error {
	if command.PlanID == uuid.Nil || command.PlanRevisionID == uuid.Nil || command.DraftID == uuid.Nil ||
		command.ExpectedDraftRevisionID == uuid.Nil || command.ExpectedDraftRevision < 1 {
		return exactDraftBranchPlanError("invalid command identity")
	}
	if !domain.IsValidServerTime(command.CreatedAt) || len(command.Branches) == 0 {
		return exactDraftBranchPlanError("invalid command timestamp or empty branch set")
	}
	if command.PlanID == command.PlanRevisionID || command.PlanID == command.DraftID ||
		command.PlanRevisionID == command.DraftID {
		return exactDraftBranchPlanError("reused plan identity")
	}
	return nil
}

func validateExactDraftBranchCommand(
	command ExactDraftBranchPlanCommand,
	branch ExactDraftBranchCommand,
	seen map[uuid.UUID]struct{},
) error {
	if branch.BranchID == uuid.Nil || len(branch.Assignments) == 0 || len(branch.Assignments) > len(branch.ChildBranchIDs) {
		return exactDraftBranchPlanError("missing branch identity")
	}
	if _, duplicate := seen[branch.BranchID]; duplicate {
		return exactDraftBranchPlanError("reused branch identity")
	}
	seen[branch.BranchID] = struct{}{}
	for position, exact := range branch.Assignments {
		childID := branch.ChildBranchIDs[position]
		if childID == uuid.Nil || childID == branch.BranchID {
			return exactDraftBranchPlanError("missing child branch identity")
		}
		if _, duplicate := seen[childID]; duplicate {
			return exactDraftBranchPlanError("reused child branch identity")
		}
		seen[childID] = struct{}{}
		if err := validateExactDraftCommandIdentity(command, childID, exact, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateExactDraftCommandIdentity(
	command ExactDraftBranchPlanCommand,
	branchID uuid.UUID,
	exact ExactNormalAssignmentCommand,
	seen map[uuid.UUID]struct{},
) error {
	if exact.PlanID != command.PlanID || exact.PlanRevisionID != command.PlanRevisionID ||
		exact.BranchID != branchID || !exact.CreatedAt.Equal(command.CreatedAt) {
		return exactDraftBranchPlanError("exact assignment identity does not match plan")
	}
	for _, id := range exactDraftCommandEvidenceIDs(exact) {
		if id == uuid.Nil {
			return exactDraftBranchPlanError("missing exact assignment identity")
		}
		if _, duplicate := seen[id]; duplicate {
			return exactDraftBranchPlanError("reused exact assignment identity")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func exactDraftCommandEvidenceIDs(command ExactNormalAssignmentCommand) []uuid.UUID {
	reserveCount, err := command.ReserveCount()
	if err != nil {
		// Keep the nil identity visible to the caller. The surrounding validator
		// will fail closed without silently dropping malformed trailing slots.
		return []uuid.UUID{uuid.Nil}
	}
	capacity := 1 + (reserveCount+1)*3
	ids := make([]uuid.UUID, 0, capacity)
	ids = append(ids, command.DecisionEvidenceID)
	ids = append(ids, command.EdgeIDs[:reserveCount+1]...)
	ids = append(ids, command.ReservationIDs[:reserveCount+1]...)
	return append(ids, command.SnapshotIDs[:reserveCount+1]...)
}

func validateExactDraftBranchSource(
	command ExactDraftBranchPlanCommand,
	draft draftusecase.Execution,
) error {
	if err := draft.Validate(); err != nil || draft.State == draftusecase.ExecutionStateCompleted ||
		draft.State == draftusecase.ExecutionStateSuperseded ||
		draft.ID != command.DraftID || draft.RevisionID != command.ExpectedDraftRevisionID ||
		draft.Revision != command.ExpectedDraftRevision || command.CreatedAt.Before(draft.FirstActorDecision.DecidedAt) {
		return exactDraftBranchPlanError("source draft revision does not match command")
	}
	return nil
}

func validateExactDraftBranchAssignmentInput(
	command ExactDraftBranchPlanCommand,
	seriesID uuid.UUID,
	childBranchID uuid.UUID,
	category domain.Category,
	exactCommand ExactNormalAssignmentCommand,
	exactAuthority ExactNormalAssignmentAuthority,
) error {
	if exactCommand.PlanID != command.PlanID || exactCommand.PlanRevisionID != command.PlanRevisionID ||
		exactCommand.BranchID != childBranchID || !exactCommand.CreatedAt.Equal(command.CreatedAt) ||
		exactCommand.Scope.SeriesID != seriesID {
		return exactDraftBranchPlanError("exact assignment command does not match branch")
	}
	if exactAuthority.Scope != exactCommand.Scope || exactAuthority.Category != category {
		return exactDraftBranchPlanError("exact assignment authority does not match branch category")
	}
	return nil
}

func validateExactDraftBranchPlanContents(
	plan ExactDraftBranchPlan,
	paths []ExactDraftBranchPath,
) error {
	validation := exactDraftBranchPlanValidation{
		seenIDs: map[uuid.UUID]struct{}{plan.ID: {}, plan.RevisionID: {}},
		slots:   make(map[int]uuid.UUID),
	}
	for index, branch := range plan.Branches {
		if err := validation.validateBranch(plan, paths[index], branch, index); err != nil {
			return err
		}
	}
	return nil
}

type exactDraftBranchPlanValidation struct {
	seenIDs        map[uuid.UUID]struct{}
	commonScope    ExactNormalAssignmentScope
	commonScopeSet bool
	slots          map[int]uuid.UUID
}

func (v *exactDraftBranchPlanValidation) validateBranch(
	plan ExactDraftBranchPlan,
	path ExactDraftBranchPath,
	branch ExactDraftBranch,
	index int,
) error {
	if branch.ID == uuid.Nil || !equalExactDraftBranchPath(branch.Path, path) ||
		len(branch.Assignments) != len(branch.Path.Categories) {
		return exactDraftBranchPlanError("branch %d does not match reachable path", index+1)
	}
	if _, duplicate := v.seenIDs[branch.ID]; duplicate {
		return exactDraftBranchPlanError("duplicate branch identity")
	}
	v.seenIDs[branch.ID] = struct{}{}
	for position := range branch.Assignments {
		childID := branch.ChildBranchIDs[position]
		if childID == uuid.Nil || childID == branch.ID {
			return exactDraftBranchPlanError("branch %d is missing a child identity", index+1)
		}
		if _, duplicate := v.seenIDs[childID]; duplicate {
			return exactDraftBranchPlanError("duplicate child branch identity")
		}
		v.seenIDs[childID] = struct{}{}
	}
	selected := make(map[domain.TaskVersionRef]struct{})
	for position, assignment := range branch.Assignments {
		if err := v.validateAssignment(plan, branch, assignment, position, selected); err != nil {
			return err
		}
	}
	return nil
}

func (v *exactDraftBranchPlanValidation) validateAssignment(
	plan ExactDraftBranchPlan,
	branch ExactDraftBranch,
	assignment ExactDraftBranchAssignment,
	position int,
	selected map[domain.TaskVersionRef]struct{},
) error {
	exact := assignment.Plan
	if err := validateExactDraftAssignmentPlan(plan, branch, assignment, position); err != nil {
		return err
	}
	if !v.commonScopeSet {
		v.commonScope = exact.Scope
		v.commonScopeSet = true
	}
	if !sameExactDraftAssignmentScope(exact.Scope, v.commonScope) {
		return exactDraftBranchPlanError("assignment scopes do not share one locked Series")
	}
	if slotID, exists := v.slots[position]; exists && slotID != exact.Scope.SlotID {
		return exactDraftBranchPlanError("slot identity changed across draft branches")
	}
	v.slots[position] = exact.Scope.SlotID
	return v.recordAssignmentEvidence(exact, selected)
}

func validateExactDraftAssignmentPlan(
	plan ExactDraftBranchPlan,
	branch ExactDraftBranch,
	assignment ExactDraftBranchAssignment,
	position int,
) error {
	exact := assignment.Plan
	if assignment.Position != position+1 || exact.Validate() != nil {
		return exactDraftBranchPlanError("branch %q assignment %d is invalid", branch.Path.Key, position+1)
	}
	if exact.PlanID != plan.ID || exact.PlanRevisionID != plan.RevisionID ||
		exact.BranchID != branch.ChildBranchIDs[position] ||
		exact.Category != branch.Path.Categories[position] {
		return exactDraftBranchPlanError("branch %q assignment %d has wrong identity", branch.Path.Key, position+1)
	}
	if exact.Scope.SeriesID != plan.SourceDraft.SeriesID || !exact.CreatedAt.Equal(plan.CreatedAt) ||
		!exactParticipantsMatchDraft(exact.ParticipantIDs, plan.SourceDraft) {
		return exactDraftBranchPlanError("branch %q assignment %d has wrong authority", branch.Path.Key, position+1)
	}
	return nil
}

func sameExactDraftAssignmentScope(
	first ExactNormalAssignmentScope,
	second ExactNormalAssignmentScope,
) bool {
	return first.TournamentID == second.TournamentID && first.RosterID == second.RosterID &&
		first.SeriesID == second.SeriesID && first.CategoryLockID == second.CategoryLockID
}

func (v *exactDraftBranchPlanValidation) recordAssignmentEvidence(
	exact ExactNormalAssignmentPlan,
	selected map[domain.TaskVersionRef]struct{},
) error {
	if _, duplicate := v.seenIDs[exact.DecisionEvidence.ID]; duplicate {
		return exactDraftBranchPlanError("duplicate decision evidence identity")
	}
	v.seenIDs[exact.DecisionEvidence.ID] = struct{}{}
	for _, edge := range exact.SelectedEdges {
		for _, id := range []uuid.UUID{edge.ID, edge.ReservationID, edge.Snapshot.SnapshotID} {
			if _, duplicate := v.seenIDs[id]; duplicate {
				return exactDraftBranchPlanError("duplicate reservation evidence identity")
			}
			v.seenIDs[id] = struct{}{}
		}
		ref := domain.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
		if _, duplicate := selected[ref]; duplicate {
			return exactDraftBranchPlanError("task version is reused within one reachable draft branch")
		}
		selected[ref] = struct{}{}
	}
	return nil
}

func validateExactDraftBranchPlanLifecycle(plan ExactDraftBranchPlan) error {
	activeCount := 0
	for _, branch := range plan.Branches {
		if err := validateExactDraftBranchLifecycle(plan, branch); err != nil {
			return err
		}
		if branch.State == ExactDraftBranchStateActive {
			activeCount++
		}
	}
	return validateExactDraftPlanState(plan, activeCount)
}

func validateExactDraftBranchLifecycle(plan ExactDraftBranchPlan, branch ExactDraftBranch) error {
	switch branch.State {
	case ExactDraftBranchStateReserved:
		return validateReservedExactDraftBranch(branch)
	case ExactDraftBranchStateActive:
		return validateActiveExactDraftBranch(plan, branch)
	case ExactDraftBranchStateReleased:
		return validateReleasedExactDraftBranch(plan, branch)
	default:
		return exactDraftBranchPlanError("unknown branch state %q", branch.State)
	}
}

func validateReservedExactDraftBranch(branch ExactDraftBranch) error {
	if !branch.ActivatedAt.IsZero() || !branch.ReleasedAt.IsZero() || branch.ReleaseReason != "" {
		return exactDraftBranchPlanError("reserved branch contains transition evidence")
	}
	for _, assignment := range branch.Assignments {
		if assignment.State != ExactDraftReservationStateReserved || !assignment.TransitionedAt.IsZero() {
			return exactDraftBranchPlanError("reserved branch contains transitioned assignment")
		}
	}
	return nil
}

func validateActiveExactDraftBranch(plan ExactDraftBranchPlan, branch ExactDraftBranch) error {
	if branch.ID != plan.ActiveBranchID || branch.ActivatedAt.IsZero() ||
		!branch.ActivatedAt.Equal(plan.CommittedAt) || !branch.ReleasedAt.IsZero() ||
		branch.ReleaseReason != "" || !slices.Equal(branch.Path.Categories, plan.CompletedCategories) {
		return exactDraftBranchPlanError("active branch evidence does not match completion")
	}
	for _, assignment := range branch.Assignments {
		if assignment.State != ExactDraftReservationStateCommitted ||
			!assignment.TransitionedAt.Equal(plan.CommittedAt) {
			return exactDraftBranchPlanError("active branch assignment is not committed")
		}
	}
	return nil
}

func validateReleasedExactDraftBranch(plan ExactDraftBranchPlan, branch ExactDraftBranch) error {
	if !branch.ActivatedAt.IsZero() || !branch.ReleasedAt.Equal(plan.CommittedAt) ||
		branch.ReleaseReason == "" || branch.ReleaseReason != strings.TrimSpace(branch.ReleaseReason) {
		return exactDraftBranchPlanError("released branch evidence is invalid")
	}
	for _, assignment := range branch.Assignments {
		if assignment.State != ExactDraftReservationStateReleased ||
			!assignment.TransitionedAt.Equal(plan.CommittedAt) {
			return exactDraftBranchPlanError("released branch assignment is not released")
		}
	}
	return nil
}

func validateExactDraftPlanState(plan ExactDraftBranchPlan, activeCount int) error {
	switch plan.State {
	case ExactDraftBranchPlanStatePlanned:
		return validatePlannedExactDraftPlan(plan, activeCount)
	case ExactDraftBranchPlanStateCommitted:
		return validateCommittedExactDraftPlan(plan, activeCount)
	default:
		return exactDraftBranchPlanError("unknown plan state %q", plan.State)
	}
}

func validatePlannedExactDraftPlan(plan ExactDraftBranchPlan, activeCount int) error {
	if plan.ActiveBranchID != uuid.Nil || plan.CompletionDraftRevisionID != uuid.Nil ||
		plan.CompletionDraftRevision != 0 || plan.ActivationCommandID != uuid.Nil ||
		len(plan.CompletedCategories) != 0 || !plan.CommittedAt.IsZero() || activeCount != 0 {
		return exactDraftBranchPlanError("planned state contains completion evidence")
	}
	for _, branch := range plan.Branches {
		if branch.State != ExactDraftBranchStateReserved {
			return exactDraftBranchPlanError("planned state contains transitioned branch")
		}
	}
	return nil
}

func validateCommittedExactDraftPlan(plan ExactDraftBranchPlan, activeCount int) error {
	if plan.ActiveBranchID == uuid.Nil || plan.CompletionDraftRevisionID == uuid.Nil ||
		plan.CompletionDraftRevision < plan.SourceDraft.Revision || plan.ActivationCommandID == uuid.Nil {
		return exactDraftBranchPlanError("committed state lacks identity evidence")
	}
	if len(plan.CompletedCategories) == 0 || !domain.IsValidServerTime(plan.CommittedAt) ||
		plan.CommittedAt.Before(plan.CreatedAt) || activeCount != 1 {
		return exactDraftBranchPlanError("committed state lacks transition evidence")
	}
	return nil
}
