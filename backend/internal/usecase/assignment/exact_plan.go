package assignment

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
)

func (u *ExactNormalAssignmentUseCase) PlanAndCommit(
	ctx context.Context,
	command ExactNormalAssignmentCommand,
) (*ExactNormalAssignmentPlan, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateExactNormalAssignmentCommand(command); err != nil {
		return nil, false, err
	}

	for range exactNormalAssignmentAttempts {
		authority, err := u.repository.LoadExactNormalAssignmentAuthority(ctx, command.Scope)
		if err != nil {
			return nil, false, fmt.Errorf("ExactNormalAssignmentUseCase - load authority: %w", err)
		}
		plan, err := BuildExactNormalAssignment(command, authority)
		if err != nil {
			return nil, false, err
		}
		committed, changed, err := u.repository.CommitExactNormalAssignment(ctx, plan)
		if errors.Is(err, domain.ErrConflict) {
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("ExactNormalAssignmentUseCase - commit exact plan: %w", err)
		}
		if committed == nil || committed.Validate() != nil || committed.ProofHash != plan.ProofHash {
			return nil, false, domain.ErrInternal
		}
		result := cloneExactNormalAssignmentPlan(*committed)
		return &result, changed, nil
	}
	return nil, false, ErrExactNormalAssignmentConflict
}

func BuildExactNormalAssignment(
	command ExactNormalAssignmentCommand,
	authority ExactNormalAssignmentAuthority,
) (ExactNormalAssignmentPlan, error) {
	return buildExactNormalAssignment(command, authority, nil)
}

// BuildExactNormalAssignmentExcluding builds an exact assignment while
// treating the supplied task versions as already reserved by the caller's
// wider application transaction.
func BuildExactNormalAssignmentExcluding(
	command ExactNormalAssignmentCommand,
	authority ExactNormalAssignmentAuthority,
	unavailable map[domain.TaskVersionRef]struct{},
) (ExactNormalAssignmentPlan, error) {
	return buildExactNormalAssignment(command, authority, unavailable)
}

func buildExactNormalAssignment(
	command ExactNormalAssignmentCommand,
	authority ExactNormalAssignmentAuthority,
	unavailable map[domain.TaskVersionRef]struct{},
) (ExactNormalAssignmentPlan, error) {
	if err := validateExactNormalAssignmentCommand(command); err != nil {
		return ExactNormalAssignmentPlan{}, err
	}
	canonical, eligible, err := normalizeExactNormalAssignmentAuthority(command.Scope, authority)
	if err != nil {
		return ExactNormalAssignmentPlan{}, err
	}
	eligible = exactNormalAvailableCandidates(eligible, unavailable)
	if len(eligible) < domain.AssignmentReserveCount+1 {
		return ExactNormalAssignmentPlan{}, exactNormalCapacityError("fewer than three unreserved task versions")
	}

	inputs := make([]string, len(eligible))
	byEvidence := make(map[string]ExactNormalTaskVersion, len(eligible))
	for index, candidate := range eligible {
		value := exactNormalTaskVersionEvidence(candidate.Task.ID, candidate.Version)
		inputs[index] = value
		byEvidence[value] = candidate
	}
	evidence, err := domain.NewDecisionEvidence(
		command.DecisionEvidenceID,
		domain.DecisionPurposeTask,
		domain.DecisionAlgorithmV1,
		inputs,
		command.PlanID,
		command.CreatedAt,
	)
	if err != nil {
		return ExactNormalAssignmentPlan{}, exactNormalAssignmentError("task selection: %v", err)
	}

	plan := ExactNormalAssignmentPlan{
		Scope: command.Scope, PlanID: command.PlanID, PlanRevisionID: command.PlanRevisionID,
		BranchID: command.BranchID, Category: canonical.Category, Revisions: canonical.Revisions,
		Pool: domain.CloneTaskPool(canonical.Pool), ParticipantIDs: append([]uuid.UUID(nil), canonical.ParticipantIDs...),
		ParticipantReservations: cloneExactNormalParticipantReservations(canonical.ParticipantReservations),
		History:                 append([]capacity.TaskUse(nil), canonical.History...),
		CandidateTaskVersions:   exactNormalTaskVersionRefs(eligible),
		GraphDigest:             canonical.GraphDigest, ArtifactDigest: canonical.ArtifactDigest,
		DecisionEvidence: evidence, CreatedAt: command.CreatedAt,
		SelectedEdges: make([]ExactNormalAssignmentEdge, domain.AssignmentReserveCount+1),
	}
	for index := range plan.SelectedEdges {
		candidate := byEvidence[evidence.Result[index]]
		snapshot, buildErr := taskexec.BuildSnapshot(taskexec.SnapshotInput{
			SnapshotID: command.SnapshotIDs[index], Version: candidate.Version,
			Kind: domain.AssignmentTaskKindNormal, Task: candidate.Task,
		})
		if buildErr != nil {
			return ExactNormalAssignmentPlan{}, exactNormalAssignmentError("snapshot %d: %v", index+1, buildErr)
		}
		digest, digestErr := taskexec.SnapshotDigest(snapshot)
		if digestErr != nil {
			return ExactNormalAssignmentPlan{}, exactNormalAssignmentError("snapshot digest %d: %v", index+1, digestErr)
		}
		plan.SelectedEdges[index] = ExactNormalAssignmentEdge{
			ID: command.EdgeIDs[index], ReservationID: command.ReservationIDs[index],
			Position: index + 1, Snapshot: snapshot, ContentDigest: digest,
		}
	}
	plan.ProofHash, err = exactNormalAssignmentProofHash(plan)
	if err != nil {
		return ExactNormalAssignmentPlan{}, err
	}
	if err := plan.Validate(); err != nil {
		return ExactNormalAssignmentPlan{}, err
	}
	return cloneExactNormalAssignmentPlan(plan), nil
}
