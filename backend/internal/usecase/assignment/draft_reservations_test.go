package assignment_test

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignment "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExactDraftExcludesLockedReservations(t *testing.T) {
	authority, command := exactDraftBranchPlanFixture(t, domain.SeriesFormatBO3)
	extra := authority.Branches[0].Assignments[0].Candidates[0]
	busy := domain.TaskVersionRef{TaskID: extra.Task.ID, Version: extra.Version}
	extra.Task.ID = uuid.New()
	ref := domain.TaskVersionRef{TaskID: extra.Task.ID, Version: extra.Version}
	for i := range authority.Branches {
		for j := range authority.Branches[i].Assignments {
			item := &authority.Branches[i].Assignments[j]
			item.Candidates = append(append([]assignment.ExactNormalTaskVersion(nil), item.Candidates...), extra)
			item.Pool.Versions = append(append([]domain.TaskVersionRef(nil), item.Pool.Versions...), ref)
		}
	}
	authority.UnavailableTaskVersions = []domain.TaskVersionRef{busy}
	plan, err := assignment.BuildExactDraftBranchPlan(command, authority)
	require.NoError(t, err)
	checkedMissing := false
	for _, branch := range plan.Branches {
		for _, child := range branch.Assignments {
			if child.Plan.Category == extra.Task.Category {
				require.Error(t, assignment.ValidateExactNormalAssignmentCandidates(child.Plan,
					authority.Branches[0].Assignments[0].Candidates, nil), "missing locked exclusion must invalidate the retained eligible set")
				checkedMissing = true
			}
			require.NotContains(t, child.Plan.CandidateTaskVersions, busy)
			require.NoError(t, assignment.ValidateExactNormalAssignmentCandidates(child.Plan, authority.Branches[0].Assignments[0].Candidates, map[domain.TaskVersionRef]struct{}{busy: {}}))
		}
	}
	require.True(t, checkedMissing)
	for _, exclusions := range [][]domain.TaskVersionRef{{busy, busy}, {{TaskID: uuid.New(), Version: 1}}, {{TaskID: busy.TaskID, Version: busy.Version + 1}}} {
		authority.UnavailableTaskVersions = exclusions
		_, err := assignment.BuildExactDraftBranchPlan(command, authority)
		require.Error(t, err)
	}
}
