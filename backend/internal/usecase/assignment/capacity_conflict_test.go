package assignment_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
)

func TestExactNormalCapacityExhaustionIsConflict(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"history", "reservations"} {
		t.Run(reason, func(t *testing.T) {
			authority, command := exactNormalAssignmentFixture()
			authority.History = nil
			unavailable := make(map[domain.TaskVersionRef]struct{})
			for _, candidate := range authority.Candidates {
				if reason == "history" {
					authority.History = append(authority.History, capacity.TaskUse{
						ParticipantID: authority.ParticipantIDs[0], TaskID: candidate.Task.ID,
					})
				} else {
					unavailable[domain.TaskVersionRef{TaskID: candidate.Task.ID, Version: candidate.Version}] = struct{}{}
				}
			}
			_, err := assignmentusecase.BuildExactNormalAssignmentExcluding(command, authority, unavailable)
			require.ErrorIs(t, err, domain.ErrConflict)
			require.ErrorIs(t, err, assignmentusecase.ErrInvalidExactNormalAssignment)
		})
	}
}

func TestSwissDraftPreservesCapacityConflict(t *testing.T) {
	t.Parallel()
	authority, command := exactDraftBranchPlanFixture(t, domain.SeriesFormatBO1)
	candidate := authority.Branches[0].Assignments[0].Candidates[0]
	authority.UnavailableTaskVersions = []domain.TaskVersionRef{{TaskID: candidate.Task.ID, Version: candidate.Version}}

	_, err := assignmentusecase.BuildExactDraftBranchPlan(command, authority)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.ErrorIs(t, err, assignmentusecase.ErrInvalidExactDraftBranchPlan)
}
