package assignment

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"

	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func (u *ExactDraftBranchPlanUseCase) PlanAndCommit(
	ctx context.Context,
	command ExactDraftBranchPlanCommand,
) (*ExactDraftBranchPlan, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateExactDraftBranchPlanCommand(command); err != nil {
		return nil, false, err
	}

	for range exactDraftBranchPlanAttempts {
		authority, err := u.repository.LoadExactDraftBranchPlanAuthority(ctx, command.DraftID)
		if err != nil {
			return nil, false, fmt.Errorf("ExactDraftBranchPlanUseCase - load authority: %w", err)
		}
		plan, err := BuildExactDraftBranchPlan(command, authority)
		if err != nil {
			return nil, false, err
		}
		committed, changed, err := u.repository.CommitExactDraftBranchPlan(ctx, plan)
		if errors.Is(err, domain.ErrConflict) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("ExactDraftBranchPlanUseCase - commit plan: %w", err)
		}
		if committed == nil || committed.Validate() != nil ||
			!exactDraftPlanMatchesCommand(*committed, command) ||
			(changed && committed.ProofHash != plan.ProofHash) {
			return nil, false, domain.ErrInternal
		}
		result := cloneExactDraftBranchPlan(*committed)
		return &result, changed, nil
	}
	return nil, false, ErrExactDraftBranchPlanConflict
}

func ReachableExactDraftBranches(draft draftusecase.Execution) ([]ExactDraftBranchPath, error) {
	if err := draft.Validate(); err != nil || draft.State == draftusecase.ExecutionStateSuperseded {
		return nil, exactDraftBranchPlanError("invalid source draft")
	}
	domainDraft, err := draft.DomainDraft()
	if err != nil {
		return nil, exactDraftBranchPlanError("source draft snapshot: %v", err)
	}
	paths := make([]ExactDraftBranchPath, 0)
	if err := enumerateExactDraftBranches(domainDraft, &paths); err != nil {
		return nil, err
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].Key < paths[j].Key })
	for index := 1; index < len(paths); index++ {
		if paths[index-1].Key == paths[index].Key {
			return nil, exactDraftBranchPlanError("duplicate reachable branch key")
		}
	}
	return cloneExactDraftBranchPaths(paths), nil
}

func BuildExactDraftBranchPlan(
	command ExactDraftBranchPlanCommand,
	authority ExactDraftBranchPlanAuthority,
) (ExactDraftBranchPlan, error) {
	if err := validateExactDraftBranchPlanCommand(command); err != nil {
		return ExactDraftBranchPlan{}, err
	}
	if err := validateExactDraftBranchSource(command, authority.Draft); err != nil {
		return ExactDraftBranchPlan{}, err
	}
	paths, err := ReachableExactDraftBranches(authority.Draft)
	if err != nil {
		return ExactDraftBranchPlan{}, err
	}
	commands, authorities, err := exactDraftBranchInputs(paths, command.Branches, authority.Branches)
	if err != nil {
		return ExactDraftBranchPlan{}, err
	}

	plan := ExactDraftBranchPlan{
		ID: command.PlanID, RevisionID: command.PlanRevisionID,
		SourceDraft: draftusecase.CloneExecution(authority.Draft), State: ExactDraftBranchPlanStatePlanned,
		Branches: make([]ExactDraftBranch, len(paths)), CreatedAt: command.CreatedAt,
	}
	for branchIndex, path := range paths {
		// Reachable draft paths are mutually exclusive. A task version must be
		// unique within one possible completed path, but reserving it again for
		// another path would otherwise make a normal three-task category pool
		// impossible to plan before the draft has narrowed its outcome.
		unavailable := make(map[domain.TaskVersionRef]struct{})
		for _, ref := range authority.UnavailableTaskVersions {
			if ref.TaskID == uuid.Nil || ref.Version < 1 {
				return ExactDraftBranchPlan{}, exactDraftBranchPlanError("invalid reservation exclusion")
			}
			if _, duplicate := unavailable[ref]; duplicate {
				return ExactDraftBranchPlan{}, exactDraftBranchPlanError("duplicate reservation exclusion")
			}
			unavailable[ref] = struct{}{}
		}
		branchCommand := commands[path.Key]
		branchAuthority := authorities[path.Key]
		if len(branchCommand.Assignments) != len(path.Categories) ||
			len(branchAuthority.Assignments) != len(path.Categories) {
			return ExactDraftBranchPlan{}, exactDraftBranchPlanError(
				"branch %q does not cover every selected category", path.Key,
			)
		}
		branch := ExactDraftBranch{
			ID:             branchCommand.BranchID,
			ChildBranchIDs: branchCommand.ChildBranchIDs,
			Path:           cloneExactDraftBranchPath(path),
			State:          ExactDraftBranchStateReserved,
			Assignments:    make([]ExactDraftBranchAssignment, len(path.Categories)),
		}
		for position, category := range path.Categories {
			exactCommand := branchCommand.Assignments[position]
			exactAuthority := branchAuthority.Assignments[position]
			for _, ref := range authority.UnavailableTaskVersions {
				found := false
				for _, version := range exactAuthority.Pool.Versions {
					if version == ref {
						found = true
						break
					}
				}
				if !found {
					return ExactDraftBranchPlan{}, exactDraftBranchPlanError("reservation exclusion is outside the immutable pool")
				}
			}
			if err := validateExactDraftBranchAssignmentInput(
				command,
				authority.Draft.SeriesID,
				branch.ChildBranchIDs[position],
				category,
				exactCommand,
				exactAuthority,
			); err != nil {
				return ExactDraftBranchPlan{}, err
			}
			exact, buildErr := BuildExactNormalAssignmentExcluding(
				exactCommand,
				exactAuthority,
				unavailable,
			)
			if buildErr != nil {
				return ExactDraftBranchPlan{}, exactDraftBranchPlanError(
					"branch %q position %d: %v", path.Key, position+1, buildErr,
				)
			}
			for _, edge := range exact.SelectedEdges {
				ref := domain.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
				unavailable[ref] = struct{}{}
			}
			branch.Assignments[position] = ExactDraftBranchAssignment{
				Position: position + 1, Plan: exact, State: ExactDraftReservationStateReserved,
			}
		}
		plan.Branches[branchIndex] = branch
	}
	plan.ProofHash = exactDraftBranchPlanProofHash(plan)
	if err := plan.Validate(); err != nil {
		return ExactDraftBranchPlan{}, err
	}
	return cloneExactDraftBranchPlan(plan), nil
}

func enumerateExactDraftBranches(
	draft domain.Draft,
	paths *[]ExactDraftBranchPath,
) error {
	if draft.State == domain.DraftStateCompleted {
		*paths = append(*paths, exactDraftBranchPath(draft))
		return nil
	}
	turn, err := draft.CurrentTurn()
	if err != nil {
		return exactDraftBranchPlanError("current turn: %v", err)
	}
	for _, category := range draft.Pool {
		if exactDraftCategoryUsed(draft.Actions, category) {
			continue
		}
		next := draftusecase.CloneDraft(draft)
		nextDeadline := turn.Deadline.Add(draftusecase.TurnDuration)
		if draftusecase.FinalTurn(draft) {
			nextDeadline = time.Time{}
		}
		if err := next.ApplyAction(
			turn.Number,
			turn.ActorID,
			turn.Action,
			category,
			turn.Deadline,
			nextDeadline,
		); err != nil {
			return exactDraftBranchPlanError("enumerate turn %d: %v", turn.Number, err)
		}
		if err := enumerateExactDraftBranches(next, paths); err != nil {
			return err
		}
	}
	return nil
}

func exactDraftCategoryUsed(actions []domain.DraftAction, category domain.Category) bool {
	for _, action := range actions {
		if action.Category == category {
			return true
		}
	}
	return false
}

func exactDraftBranchPath(draft domain.Draft) ExactDraftBranchPath {
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

func exactDraftBranchInputs(
	paths []ExactDraftBranchPath,
	commandInput []ExactDraftBranchCommand,
	authorityInput []ExactDraftBranchAuthority,
) (map[string]ExactDraftBranchCommand, map[string]ExactDraftBranchAuthority, error) {
	if len(commandInput) != len(paths) || len(authorityInput) != len(paths) {
		return nil, nil, exactDraftBranchPlanError("every reachable branch must be supplied")
	}
	commands := make(map[string]ExactDraftBranchCommand, len(commandInput))
	for _, branch := range commandInput {
		if branch.BranchID == uuid.Nil || branch.Key == "" || branch.Key != strings.TrimSpace(branch.Key) {
			return nil, nil, exactDraftBranchPlanError("invalid branch command")
		}
		if _, duplicate := commands[branch.Key]; duplicate {
			return nil, nil, exactDraftBranchPlanError("duplicate branch command %q", branch.Key)
		}
		commands[branch.Key] = branch
	}
	authorities := make(map[string]ExactDraftBranchAuthority, len(authorityInput))
	for _, branch := range authorityInput {
		if branch.Key == "" || branch.Key != strings.TrimSpace(branch.Key) {
			return nil, nil, exactDraftBranchPlanError("invalid branch authority")
		}
		if _, duplicate := authorities[branch.Key]; duplicate {
			return nil, nil, exactDraftBranchPlanError("duplicate branch authority %q", branch.Key)
		}
		authorities[branch.Key] = branch
	}
	for _, path := range paths {
		if _, exists := commands[path.Key]; !exists {
			return nil, nil, exactDraftBranchPlanError("missing branch command %q", path.Key)
		}
		if _, exists := authorities[path.Key]; !exists {
			return nil, nil, exactDraftBranchPlanError("missing branch authority %q", path.Key)
		}
	}
	return commands, authorities, nil
}
