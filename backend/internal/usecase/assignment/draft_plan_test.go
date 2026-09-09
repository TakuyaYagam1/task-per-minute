package assignment_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
)

func TestExactDraftBranchPlan(t *testing.T) {
	t.Parallel()

	t.Run("reserves every BO1 outcome and retries fresh authority", func(t *testing.T) {
		t.Parallel()

		authority, command := exactDraftBranchPlanFixture(t, domain.SeriesFormatBO1)
		repository := newExactDraftBranchPlanRepositoryHarness(t, &exactDraftBranchPlanRepositoryState{
			authorities:   []assignmentusecase.ExactDraftBranchPlanAuthority{authority, authority},
			planConflicts: 1,
		})

		plan, changed, err := assignmentusecase.NewExactDraftBranchPlanUseCase(repository.mock).
			PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, plan.Validate())
		require.Equal(t, assignmentusecase.ExactDraftBranchPlanStatePlanned, plan.State)
		require.Len(t, plan.Branches, 6)
		require.Equal(t, 2, exactDraftPlanLoadCount(repository.state))
		require.Equal(t, 2, exactDraftPlanCommitCount(repository.state))
		assertExactDraftReservations(t, plan, len(authority.Draft.Pool)*3)
	})

	t.Run("reserves every BO3 outcome", func(t *testing.T) {
		t.Parallel()

		authority, command := exactDraftBranchPlanFixture(t, domain.SeriesFormatBO3)
		repository := newExactDraftBranchPlanRepositoryHarness(t, &exactDraftBranchPlanRepositoryState{
			authorities: []assignmentusecase.ExactDraftBranchPlanAuthority{authority},
		})

		plan, changed, err := assignmentusecase.NewExactDraftBranchPlanUseCase(repository.mock).
			PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, plan.Validate())
		require.Len(t, plan.Branches, 120)
		// Five categories with three healthy versions each are sufficient for
		// every mutually exclusive BO3 outcome. The same version may appear
		// in different draft groups, never twice in one completed path.
		assertExactDraftReservations(t, plan, len(authority.Draft.Pool)*3)
	})

	t.Run("fails closed when one reachable branch is missing", func(t *testing.T) {
		t.Parallel()

		authority, command := exactDraftBranchPlanFixture(t, domain.SeriesFormatBO1)
		authority.Branches = authority.Branches[:len(authority.Branches)-1]
		command.Branches = command.Branches[:len(command.Branches)-1]
		repository := newExactDraftBranchPlanRepositoryHarness(t, &exactDraftBranchPlanRepositoryState{
			authorities: []assignmentusecase.ExactDraftBranchPlanAuthority{authority},
		})

		plan, changed, err := assignmentusecase.NewExactDraftBranchPlanUseCase(repository.mock).
			PlanAndCommit(t.Context(), command)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, assignmentusecase.ErrInvalidExactDraftBranchPlan)
		require.Equal(t, 0, exactDraftPlanCommitCount(repository.state))
	})

	t.Run("rejects a child branch identity swapped between category positions", func(t *testing.T) {
		t.Parallel()

		authority, command := exactDraftBranchPlanFixture(t, domain.SeriesFormatBO3)
		first := &command.Branches[0]
		first.ChildBranchIDs[0], first.ChildBranchIDs[1] = first.ChildBranchIDs[1], first.ChildBranchIDs[0]
		// An attacker cannot retarget the proof by moving only the parent-child
		// mapping. Each exact-normal command is bound to its own child identity.
		repository := newExactDraftBranchPlanRepositoryHarness(t, &exactDraftBranchPlanRepositoryState{
			authorities: []assignmentusecase.ExactDraftBranchPlanAuthority{authority},
		})

		plan, changed, err := assignmentusecase.NewExactDraftBranchPlanUseCase(repository.mock).
			PlanAndCommit(t.Context(), command)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, assignmentusecase.ErrInvalidExactDraftBranchPlan)
		require.Equal(t, 0, exactDraftPlanCommitCount(repository.state))
	})

	t.Run("fails closed when the final branch cannot reserve three unique versions", func(t *testing.T) {
		t.Parallel()

		authority, command := exactDraftBranchPlanFixture(t, domain.SeriesFormatBO1)
		for branchIndex := range authority.Branches {
			for assignmentIndex := range authority.Branches[branchIndex].Assignments {
				assignment := &authority.Branches[branchIndex].Assignments[assignmentIndex]
				assignment.Pool.Versions = assignment.Pool.Versions[:len(assignment.Pool.Versions)-1]
				assignment.Candidates = assignment.Candidates[:len(assignment.Candidates)-1]
			}
		}
		repository := newExactDraftBranchPlanRepositoryHarness(t, &exactDraftBranchPlanRepositoryState{
			authorities: []assignmentusecase.ExactDraftBranchPlanAuthority{authority},
		})

		plan, changed, err := assignmentusecase.NewExactDraftBranchPlanUseCase(repository.mock).
			PlanAndCommit(t.Context(), command)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, assignmentusecase.ErrInvalidExactDraftBranchPlan)
		require.Equal(t, 0, exactDraftPlanCommitCount(repository.state))
	})

	t.Run("recomputes only suffixes reachable from a partial draft", func(t *testing.T) {
		t.Parallel()

		authority, _ := exactDraftBranchPlanFixture(t, domain.SeriesFormatBO3)
		initialPaths, err := assignmentusecase.ReachableExactDraftBranches(authority.Draft)
		require.NoError(t, err)
		require.Len(t, initialPaths, 120)
		firstAction := initialPaths[0].Actions[0]
		partial := advanceExactDraft(
			t,
			authority.Draft,
			[]assignmentusecase.ExactDraftBranchAction{firstAction},
			6800,
		)

		paths, err := assignmentusecase.ReachableExactDraftBranches(partial)
		require.NoError(t, err)
		require.Len(t, paths, 24)
		for _, path := range paths {
			require.Equal(t, firstAction, path.Actions[0])
		}
	})

	t.Run("activates the completed path and releases every unused reservation", func(t *testing.T) {
		t.Parallel()

		authority, command := exactDraftBranchPlanFixture(t, domain.SeriesFormatBO1)
		repository := newExactDraftBranchPlanRepositoryHarness(t, &exactDraftBranchPlanRepositoryState{
			authorities: []assignmentusecase.ExactDraftBranchPlanAuthority{authority},
		})
		useCase := assignmentusecase.NewExactDraftBranchPlanUseCase(repository.mock)
		plan, _, err := useCase.PlanAndCommit(t.Context(), command)
		require.NoError(t, err)

		completed := completeExactDraftBranch(t, authority.Draft, plan.Branches[2].Path, 7000)
		setExactDraftCompletion(repository.state, completed, 1)
		committedAt := completed.Actions[len(completed.Actions)-1].OccurredAt.Add(time.Second)
		activation := assignmentusecase.ExactDraftBranchActivationCommand{
			PlanID: plan.ID, DraftID: completed.ID, ExpectedPlanRevisionID: plan.RevisionID,
			ExpectedDraftRevisionID: completed.RevisionID, ExpectedDraftRevision: completed.Revision,
			CommandID: task031ID(7990), CommittedAt: committedAt,
			ReleaseReason: "draft branch was not selected",
		}

		activated, changed, err := useCase.ActivateCompletedBranch(t.Context(), activation)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, activated.Validate())
		require.Equal(t, assignmentusecase.ExactDraftBranchPlanStateCommitted, activated.State)
		require.Equal(t, plan.Branches[2].ID, activated.ActiveBranchID)
		require.Equal(t, 2, exactDraftActivationLoadCount(repository.state))
		require.Equal(t, 2, exactDraftActivationCommitCount(repository.state))

		for _, branch := range activated.Branches {
			if branch.ID == activated.ActiveBranchID {
				require.Equal(t, assignmentusecase.ExactDraftBranchStateActive, branch.State)
				for _, assignment := range branch.Assignments {
					require.Equal(t, assignmentusecase.ExactDraftReservationStateCommitted, assignment.State)
					require.Equal(t, committedAt, assignment.TransitionedAt)
				}
				continue
			}
			require.Equal(t, assignmentusecase.ExactDraftBranchStateReleased, branch.State)
			require.Equal(t, activation.ReleaseReason, branch.ReleaseReason)
			for _, assignment := range branch.Assignments {
				require.Equal(t, assignmentusecase.ExactDraftReservationStateReleased, assignment.State)
				require.Equal(t, committedAt, assignment.TransitionedAt)
			}
		}
	})
}
