package assignment

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"

	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func exactDraftPlanMatchesCommand(
	plan ExactDraftBranchPlan,
	command ExactDraftBranchPlanCommand,
) bool {
	if plan.ID != command.PlanID || plan.RevisionID != command.PlanRevisionID ||
		plan.SourceDraft.ID != command.DraftID ||
		plan.SourceDraft.RevisionID != command.ExpectedDraftRevisionID ||
		plan.SourceDraft.Revision != command.ExpectedDraftRevision ||
		!plan.CreatedAt.Equal(command.CreatedAt) || len(plan.Branches) != len(command.Branches) {
		return false
	}
	branches := make(map[string]ExactDraftBranchCommand, len(command.Branches))
	for _, branch := range command.Branches {
		branches[branch.Key] = branch
	}
	for _, branch := range plan.Branches {
		commandBranch, ok := branches[branch.Path.Key]
		if !ok || commandBranch.BranchID != branch.ID ||
			commandBranch.ChildBranchIDs != branch.ChildBranchIDs {
			return false
		}
	}
	return true
}

func exactParticipantsMatchDraft(participants []uuid.UUID, draft draftusecase.Execution) bool {
	if len(participants) != 2 {
		return false
	}
	return (participants[0] == draft.FirstParticipantID && participants[1] == draft.SecondParticipantID) ||
		(participants[0] == draft.SecondParticipantID && participants[1] == draft.FirstParticipantID)
}

func exactDraftBranchPlanProofHash(plan ExactDraftBranchPlan) string {
	hash := sha256.New()
	writeExactDraftProofField(hash, exactDraftBranchPlanProofV1)
	writeExactDraftProofField(hash, plan.ID.String())
	writeExactDraftProofField(hash, plan.RevisionID.String())
	writeExactDraftProofField(hash, plan.SourceDraft.ID.String())
	writeExactDraftProofField(hash, plan.SourceDraft.RevisionID.String())
	writeExactDraftProofField(hash, fmt.Sprintf("draft-revision:%d", plan.SourceDraft.Revision))
	writeExactDraftProofField(hash, plan.CreatedAt.Format(time.RFC3339Nano))
	for _, branch := range plan.Branches {
		writeExactDraftProofField(hash, branch.ID.String())
		writeExactDraftProofField(hash, branch.Path.Key)
		for _, category := range branch.Path.Categories {
			writeExactDraftProofField(hash, category.String())
		}
		for _, assignment := range branch.Assignments {
			writeExactDraftProofField(hash, fmt.Sprintf("position:%d", assignment.Position))
			writeExactDraftProofField(hash, assignment.Plan.ProofHash)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeExactDraftProofField(hash io.Writer, value string) {
	_, _ = fmt.Fprintf(hash, "%d:", len(value))
	_, _ = hash.Write([]byte(value))
}

func equalExactDraftBranchPath(first, second ExactDraftBranchPath) bool {
	return first.Key == second.Key && slices.Equal(first.Actions, second.Actions) &&
		slices.Equal(first.Categories, second.Categories)
}

func cloneExactDraftBranchPath(path ExactDraftBranchPath) ExactDraftBranchPath {
	cloned := path
	cloned.Actions = append([]ExactDraftBranchAction(nil), path.Actions...)
	cloned.Categories = append([]domain.Category(nil), path.Categories...)
	return cloned
}

func cloneExactDraftBranchPaths(paths []ExactDraftBranchPath) []ExactDraftBranchPath {
	cloned := make([]ExactDraftBranchPath, len(paths))
	for index, path := range paths {
		cloned[index] = cloneExactDraftBranchPath(path)
	}
	return cloned
}

func cloneExactDraftBranchPlan(plan ExactDraftBranchPlan) ExactDraftBranchPlan {
	cloned := plan
	cloned.SourceDraft = draftusecase.CloneExecution(plan.SourceDraft)
	cloned.CompletedCategories = append([]domain.Category(nil), plan.CompletedCategories...)
	cloned.Branches = make([]ExactDraftBranch, len(plan.Branches))
	for branchIndex, branch := range plan.Branches {
		clonedBranch := branch
		clonedBranch.Path = cloneExactDraftBranchPath(branch.Path)
		clonedBranch.Assignments = make([]ExactDraftBranchAssignment, len(branch.Assignments))
		for assignmentIndex, assignment := range branch.Assignments {
			clonedAssignment := assignment
			clonedAssignment.Plan = CloneExactNormalAssignmentPlan(assignment.Plan)
			clonedBranch.Assignments[assignmentIndex] = clonedAssignment
		}
		cloned.Branches[branchIndex] = clonedBranch
	}
	return cloned
}

func exactDraftBranchPlanError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidExactDraftBranchPlan, fmt.Sprintf(format, arguments...))
}
