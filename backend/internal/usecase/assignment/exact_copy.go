package assignment

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
)

func exactNormalTaskVersionRefs(candidates []ExactNormalTaskVersion) []domain.TaskVersionRef {
	result := make([]domain.TaskVersionRef, len(candidates))
	for index, candidate := range candidates {
		result[index] = domain.TaskVersionRef{TaskID: candidate.Task.ID, Version: candidate.Version}
	}
	return result
}

func canonicalExactNormalHistory(history []capacity.TaskUse) []capacity.TaskUse {
	result := append([]capacity.TaskUse(nil), history...)
	sort.Slice(result, func(i, j int) bool {
		if comparison := bytes.Compare(result[i].ParticipantID[:], result[j].ParticipantID[:]); comparison != 0 {
			return comparison < 0
		}
		if comparison := bytes.Compare(result[i].TaskID[:], result[j].TaskID[:]); comparison != 0 {
			return comparison < 0
		}
		return result[i].Version < result[j].Version
	})
	return result
}

func cloneExactNormalParticipantReservations(
	input []ExactNormalParticipantReservation,
) []ExactNormalParticipantReservation {
	return append([]ExactNormalParticipantReservation(nil), input...)
}

func cloneExactNormalAssignmentPlan(plan ExactNormalAssignmentPlan) ExactNormalAssignmentPlan {
	cloned := plan
	cloned.Pool = domain.CloneTaskPool(plan.Pool)
	cloned.ParticipantIDs = append([]uuid.UUID(nil), plan.ParticipantIDs...)
	cloned.ParticipantReservations = cloneExactNormalParticipantReservations(plan.ParticipantReservations)
	cloned.History = append([]capacity.TaskUse(nil), plan.History...)
	cloned.CandidateTaskVersions = append([]domain.TaskVersionRef(nil), plan.CandidateTaskVersions...)
	cloned.DecisionEvidence.NormalizedInputs = append([]string(nil), plan.DecisionEvidence.NormalizedInputs...)
	cloned.DecisionEvidence.Result = append([]string(nil), plan.DecisionEvidence.Result...)
	cloned.SelectedEdges = append([]ExactNormalAssignmentEdge(nil), plan.SelectedEdges...)
	for index := range cloned.SelectedEdges {
		task := taskexec.CloneTask(&domain.Task{
			ID:            cloned.SelectedEdges[index].Snapshot.TaskID,
			Title:         cloned.SelectedEdges[index].Snapshot.Title,
			Description:   cloned.SelectedEdges[index].Snapshot.Description,
			Category:      cloned.SelectedEdges[index].Snapshot.Category,
			Difficulty:    cloned.SelectedEdges[index].Snapshot.Difficulty,
			TimeLimit:     cloned.SelectedEdges[index].Snapshot.TimeLimit,
			Flag:          cloned.SelectedEdges[index].Snapshot.Flag,
			Hints:         cloned.SelectedEdges[index].Snapshot.Hints,
			TaskURL:       cloned.SelectedEdges[index].Snapshot.TaskURL,
			SourceFileURL: cloned.SelectedEdges[index].Snapshot.SourceFileURL,
		})
		cloned.SelectedEdges[index].Snapshot.Hints = task.Hints
		cloned.SelectedEdges[index].Snapshot.TaskURL = task.TaskURL
		cloned.SelectedEdges[index].Snapshot.SourceFileURL = task.SourceFileURL
	}
	return cloned
}

// CloneExactNormalAssignmentPlan returns a detached copy safe for use across
// application orchestration boundaries.
func CloneExactNormalAssignmentPlan(plan ExactNormalAssignmentPlan) ExactNormalAssignmentPlan {
	return cloneExactNormalAssignmentPlan(plan)
}

func exactNormalTaskVersionEvidence(taskID uuid.UUID, version int) string {
	return fmt.Sprintf("%s@%d", taskID, version)
}

func exactNormalAssignmentError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidExactNormalAssignment, fmt.Sprintf(format, arguments...))
}

func exactNormalCapacityError(message string) error {
	return fmt.Errorf("%w: %w", exactNormalAssignmentError("%s", message), domain.ErrConflict)
}
