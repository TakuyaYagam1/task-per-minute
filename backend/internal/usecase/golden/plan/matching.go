package plan

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func goldenExactMatching(authority Authority) ([][]int, error) {
	demandCount := len(authority.Groups) * (domain.AssignmentReserveCount + 1)
	assignedCandidate := make([]int, demandCount)
	assignedDemand := make([]int, len(authority.Candidates))
	for index := range assignedCandidate {
		assignedCandidate[index] = -1
	}
	for index := range assignedDemand {
		assignedDemand[index] = -1
	}
	var assign func(int, []bool) bool
	assign = func(demand int, seen []bool) bool {
		groupIndex := demand / (domain.AssignmentReserveCount + 1)
		for candidateIndex := range authority.Candidates {
			if seen[candidateIndex] || !goldenCandidateEligibleForGroup(authority, groupIndex, candidateIndex) {
				continue
			}
			seen[candidateIndex] = true
			previousDemand := assignedDemand[candidateIndex]
			if previousDemand == -1 || assign(previousDemand, seen) {
				assignedDemand[candidateIndex] = demand
				assignedCandidate[demand] = candidateIndex
				return true
			}
		}
		return false
	}
	for demand := 0; demand < demandCount; demand++ {
		if !assign(demand, make([]bool, len(authority.Candidates))) {
			return nil, fmt.Errorf("%w: no complete primary and reserve matching", ErrExactPlanInsufficient)
		}
	}
	result := make([][]int, len(authority.Groups))
	for groupIndex := range result {
		result[groupIndex] = append([]int(nil), assignedCandidate[groupIndex*(domain.AssignmentReserveCount+1):(groupIndex+1)*(domain.AssignmentReserveCount+1)]...)
	}
	return result, nil
}

func goldenCandidateEligibleForGroup(authority Authority, groupIndex, candidateIndex int) bool {
	candidate := authority.Candidates[candidateIndex]
	health := candidate.Health
	if !health.Exists || !health.Enabled || !health.Healthy || !health.MutationLocked || health.PubliclyExposed {
		return false
	}
	ref := domain.TaskVersionRef{TaskID: candidate.Task.ID, Version: candidate.Version}
	for _, reservation := range authority.ExistingTaskReservations {
		if reservation.TaskVersion == ref {
			return false
		}
	}
	participants := make(map[uuid.UUID]struct{}, len(authority.Groups[groupIndex].ActiveParticipantIDs))
	for _, participantID := range authority.Groups[groupIndex].ActiveParticipantIDs {
		participants[participantID] = struct{}{}
	}
	for _, receipt := range authority.History {
		if receipt.TaskID == candidate.Task.ID {
			if _, belongs := participants[receipt.ParticipantID]; belongs {
				return false
			}
		}
	}
	return true
}

func goldenCandidateIndex(candidates []TaskVersion, ref domain.TaskVersionRef) int {
	for index, candidate := range candidates {
		if candidate.Task.ID == ref.TaskID && candidate.Version == ref.Version {
			return index
		}
	}
	return -1
}
