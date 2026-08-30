package arena_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestExactDraftBranchPlan(t *testing.T) {
	t.Parallel()

	t.Run("reserves every BO1 outcome and retries fresh authority", func(t *testing.T) {
		t.Parallel()

		authority, command := exactDraftBranchPlanFixture(t, domain.ArenaSeriesFormatBO1)
		repository := &exactDraftBranchPlanRepositoryFake{
			authorities:   []arena.ExactDraftBranchPlanAuthority{authority, authority},
			planConflicts: 1,
		}

		plan, changed, err := arena.NewExactDraftBranchPlanUseCase(repository).
			PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, plan.Validate())
		require.Equal(t, arena.ExactDraftBranchPlanStatePlanned, plan.State)
		require.Len(t, plan.Branches, 6)
		require.Equal(t, 2, repository.planLoadCount())
		require.Equal(t, 2, repository.planCommitCount())
		assertExactDraftReservations(t, plan, 6*3)
	})

	t.Run("reserves every BO3 outcome", func(t *testing.T) {
		t.Parallel()

		authority, command := exactDraftBranchPlanFixture(t, domain.ArenaSeriesFormatBO3)
		repository := &exactDraftBranchPlanRepositoryFake{
			authorities: []arena.ExactDraftBranchPlanAuthority{authority},
		}

		plan, changed, err := arena.NewExactDraftBranchPlanUseCase(repository).
			PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, plan.Validate())
		require.Len(t, plan.Branches, 120)
		assertExactDraftReservations(t, plan, 120*3*3)
	})

	t.Run("fails closed when one reachable branch is missing", func(t *testing.T) {
		t.Parallel()

		authority, command := exactDraftBranchPlanFixture(t, domain.ArenaSeriesFormatBO1)
		authority.Branches = authority.Branches[:len(authority.Branches)-1]
		command.Branches = command.Branches[:len(command.Branches)-1]
		repository := &exactDraftBranchPlanRepositoryFake{
			authorities: []arena.ExactDraftBranchPlanAuthority{authority},
		}

		plan, changed, err := arena.NewExactDraftBranchPlanUseCase(repository).
			PlanAndCommit(t.Context(), command)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrInvalidExactDraftBranchPlan)
		require.Equal(t, 0, repository.planCommitCount())
	})

	t.Run("fails closed when the final branch cannot reserve three unique versions", func(t *testing.T) {
		t.Parallel()

		authority, command := exactDraftBranchPlanFixture(t, domain.ArenaSeriesFormatBO1)
		for branchIndex := range authority.Branches {
			for assignmentIndex := range authority.Branches[branchIndex].Assignments {
				assignment := &authority.Branches[branchIndex].Assignments[assignmentIndex]
				assignment.Pool.Versions = assignment.Pool.Versions[:len(assignment.Pool.Versions)-1]
				assignment.Candidates = assignment.Candidates[:len(assignment.Candidates)-1]
			}
		}
		repository := &exactDraftBranchPlanRepositoryFake{
			authorities: []arena.ExactDraftBranchPlanAuthority{authority},
		}

		plan, changed, err := arena.NewExactDraftBranchPlanUseCase(repository).
			PlanAndCommit(t.Context(), command)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrInvalidExactDraftBranchPlan)
		require.Equal(t, 0, repository.planCommitCount())
	})

	t.Run("recomputes only suffixes reachable from a partial draft", func(t *testing.T) {
		t.Parallel()

		authority, _ := exactDraftBranchPlanFixture(t, domain.ArenaSeriesFormatBO3)
		initialPaths, err := arena.ReachableExactDraftBranches(authority.Draft)
		require.NoError(t, err)
		require.Len(t, initialPaths, 120)
		firstAction := initialPaths[0].Actions[0]
		partial := advanceExactDraft(
			t,
			authority.Draft,
			[]arena.ExactDraftBranchAction{firstAction},
			6800,
		)

		paths, err := arena.ReachableExactDraftBranches(partial)
		require.NoError(t, err)
		require.Len(t, paths, 24)
		for _, path := range paths {
			require.Equal(t, firstAction, path.Actions[0])
		}
	})

	t.Run("activates the completed path and releases every unused reservation", func(t *testing.T) {
		t.Parallel()

		authority, command := exactDraftBranchPlanFixture(t, domain.ArenaSeriesFormatBO1)
		repository := &exactDraftBranchPlanRepositoryFake{
			authorities: []arena.ExactDraftBranchPlanAuthority{authority},
		}
		useCase := arena.NewExactDraftBranchPlanUseCase(repository)
		plan, _, err := useCase.PlanAndCommit(t.Context(), command)
		require.NoError(t, err)

		completed := completeExactDraftBranch(t, authority.Draft, plan.Branches[2].Path, 7000)
		repository.setCompletion(completed)
		repository.activationConflicts = 1
		committedAt := completed.Actions[len(completed.Actions)-1].OccurredAt.Add(time.Second)
		activation := arena.ExactDraftBranchActivationCommand{
			PlanID: plan.ID, DraftID: completed.ID, ExpectedPlanRevisionID: plan.RevisionID,
			ExpectedDraftRevisionID: completed.RevisionID, ExpectedDraftRevision: completed.Revision,
			CommandID: task031ID(7990), CommittedAt: committedAt,
			ReleaseReason: "draft branch was not selected",
		}

		activated, changed, err := useCase.ActivateCompletedBranch(t.Context(), activation)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, activated.Validate())
		require.Equal(t, arena.ExactDraftBranchPlanStateCommitted, activated.State)
		require.Equal(t, plan.Branches[2].ID, activated.ActiveBranchID)
		require.Equal(t, 2, repository.activationLoadCount())
		require.Equal(t, 2, repository.activationCommitCount())

		for _, branch := range activated.Branches {
			if branch.ID == activated.ActiveBranchID {
				require.Equal(t, arena.ExactDraftBranchStateActive, branch.State)
				for _, assignment := range branch.Assignments {
					require.Equal(t, arena.ExactDraftReservationStateCommitted, assignment.State)
					require.Equal(t, committedAt, assignment.TransitionedAt)
				}
				continue
			}
			require.Equal(t, arena.ExactDraftBranchStateReleased, branch.State)
			require.Equal(t, activation.ReleaseReason, branch.ReleaseReason)
			for _, assignment := range branch.Assignments {
				require.Equal(t, arena.ExactDraftReservationStateReleased, assignment.State)
				require.Equal(t, committedAt, assignment.TransitionedAt)
			}
		}
	})
}

func assertExactDraftReservations(t *testing.T, plan *arena.ExactDraftBranchPlan, want int) {
	t.Helper()

	seen := make(map[arena.TaskVersionRef]struct{}, want)
	for _, branch := range plan.Branches {
		require.Equal(t, arena.ExactDraftBranchStateReserved, branch.State)
		require.Len(t, branch.Assignments, len(branch.Path.Categories))
		for position, assignment := range branch.Assignments {
			require.Equal(t, position+1, assignment.Position)
			require.Equal(t, branch.Path.Categories[position], assignment.Plan.Category)
			require.Equal(t, arena.ExactDraftReservationStateReserved, assignment.State)
			for _, edge := range assignment.Plan.SelectedEdges {
				ref := arena.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
				if _, duplicate := seen[ref]; duplicate {
					t.Fatalf("task version %v was reserved for more than one draft branch", ref)
				}
				seen[ref] = struct{}{}
			}
		}
	}
	require.Len(t, seen, want)
}

type exactDraftBranchPlanRepositoryFake struct {
	mu                  sync.Mutex
	authorities         []arena.ExactDraftBranchPlanAuthority
	plan                *arena.ExactDraftBranchPlan
	completion          *arena.DraftExecution
	planConflicts       int
	activationConflicts int
	planLoads           int
	planCommits         int
	activationLoads     int
	activationCommits   int
}

func (r *exactDraftBranchPlanRepositoryFake) LoadExactDraftBranchPlanAuthority(
	_ context.Context,
	_ uuid.UUID,
) (arena.ExactDraftBranchPlanAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := min(r.planLoads, len(r.authorities)-1)
	r.planLoads++
	return r.authorities[index], nil
}

func (r *exactDraftBranchPlanRepositoryFake) CommitExactDraftBranchPlan(
	_ context.Context,
	plan arena.ExactDraftBranchPlan,
) (*arena.ExactDraftBranchPlan, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.planCommits++
	if r.planConflicts > 0 {
		r.planConflicts--
		return nil, false, domain.ErrConflict
	}
	if r.plan != nil {
		recorded := *r.plan
		return &recorded, false, nil
	}
	recorded := plan
	r.plan = &recorded
	return &recorded, true, nil
}

func (r *exactDraftBranchPlanRepositoryFake) LoadExactDraftBranchActivation(
	_ context.Context,
	_ uuid.UUID,
) (*arena.ExactDraftBranchPlan, *arena.DraftExecution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activationLoads++
	if r.plan == nil || r.completion == nil {
		return nil, nil, nil
	}
	plan := *r.plan
	draft := *r.completion
	return &plan, &draft, nil
}

func (r *exactDraftBranchPlanRepositoryFake) CommitExactDraftBranchActivation(
	_ context.Context,
	plan arena.ExactDraftBranchPlan,
) (*arena.ExactDraftBranchPlan, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activationCommits++
	if r.activationConflicts > 0 {
		r.activationConflicts--
		return nil, false, domain.ErrConflict
	}
	recorded := plan
	r.plan = &recorded
	return &recorded, true, nil
}

func (r *exactDraftBranchPlanRepositoryFake) setCompletion(draft arena.DraftExecution) {
	r.mu.Lock()
	defer r.mu.Unlock()
	recorded := draft
	r.completion = &recorded
}

func (r *exactDraftBranchPlanRepositoryFake) planLoadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.planLoads
}

func (r *exactDraftBranchPlanRepositoryFake) planCommitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.planCommits
}

func (r *exactDraftBranchPlanRepositoryFake) activationLoadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activationLoads
}

func (r *exactDraftBranchPlanRepositoryFake) activationCommitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activationCommits
}

func exactDraftBranchPlanFixture(
	t *testing.T,
	format domain.ArenaSeriesFormat,
) (arena.ExactDraftBranchPlanAuthority, arena.ExactDraftBranchPlanCommand) {
	t.Helper()

	createdAt := time.Date(2026, time.August, 30, 17, 0, 0, 0, time.UTC)
	stage := arena.ArenaStageSwiss
	if format == domain.ArenaSeriesFormatBO3 {
		stage = arena.ArenaStageFinal
	}
	categoryRevision := task029CategoryRevision(t, stage, true, task031ID(1), createdAt)
	draft, err := arena.StartDraftExecution(arena.DraftExecutionStartCommand{
		CategoryRevision: categoryRevision, DraftID: task031ID(2), InitialRevisionID: task031ID(3),
		DecisionEvidenceID: task031ID(4), ParticipantIDs: [2]uuid.UUID{task031ID(5), task031ID(6)},
		ServiceEpoch: task031ID(7), CommandID: task031ID(8), StartedAt: createdAt,
	})
	require.NoError(t, err)
	paths, err := arena.ReachableExactDraftBranches(draft)
	require.NoError(t, err)

	perCategory := make(map[domain.Category]int)
	for _, path := range paths {
		for _, category := range path.Categories {
			perCategory[category] += domain.ArenaAssignmentReserveCount + 1
		}
	}

	poolID := task031ID(20)
	versions := make([]arena.TaskVersionRef, 0)
	candidates := make([]arena.ExactNormalTaskVersion, 0)
	nextID := 10000
	for _, category := range draft.Pool {
		for range perCategory[category] {
			taskID := task031ID(nextID)
			nextID++
			version := len(versions) + 1
			task := domain.Task{
				ID: taskID, Title: fmt.Sprintf("Arena task %d", version), Description: "Solve this task.",
				Category: category, Difficulty: domain.DifficultyHard, TimeLimit: 180,
				Flag: fmt.Sprintf("FLAG{%d}", version), Hints: []string{"first", "second", "third"},
			}
			versions = append(versions, arena.TaskVersionRef{TaskID: taskID, Version: version})
			candidates = append(candidates, arena.ExactNormalTaskVersion{
				PoolRevisionID: poolID, Version: version, Task: task,
			})
		}
	}
	pool := arena.TaskPoolRevision{
		ID: poolID, Revision: 4, Kind: domain.ArenaTaskKindNormal, Versions: versions,
	}

	planID := task031ID(21)
	planRevisionID := task031ID(22)
	categoryLockID := categoryRevision.ID
	slotIDs := [3]uuid.UUID{task031ID(30), task031ID(31), task031ID(32)}
	command := arena.ExactDraftBranchPlanCommand{
		PlanID: planID, PlanRevisionID: planRevisionID, DraftID: draft.ID,
		ExpectedDraftRevisionID: draft.RevisionID, ExpectedDraftRevision: draft.Revision,
		CreatedAt: createdAt.Add(time.Second),
		Branches:  make([]arena.ExactDraftBranchCommand, len(paths)),
	}
	authority := arena.ExactDraftBranchPlanAuthority{
		Draft:    draft,
		Branches: make([]arena.ExactDraftBranchAuthority, len(paths)),
	}
	identity := 100
	for branchIndex, path := range paths {
		branchID := task031ID(identity)
		identity++
		branchCommand := arena.ExactDraftBranchCommand{
			BranchID: branchID, Key: path.Key,
			Assignments: make([]arena.ExactNormalAssignmentCommand, len(path.Categories)),
		}
		branchAuthority := arena.ExactDraftBranchAuthority{
			Key:         path.Key,
			Assignments: make([]arena.ExactNormalAssignmentAuthority, len(path.Categories)),
		}
		for position, category := range path.Categories {
			scope := arena.ExactNormalAssignmentScope{
				TournamentID: task031ID(40), RosterID: categoryRevision.RosterID,
				SeriesID: draft.SeriesID, SlotID: slotIDs[position], CategoryLockID: categoryLockID,
			}
			exactCommand := arena.ExactNormalAssignmentCommand{
				Scope: scope, PlanID: planID, PlanRevisionID: planRevisionID,
				BranchID: branchID, DecisionEvidenceID: task031ID(identity),
				CreatedAt: command.CreatedAt,
			}
			identity++
			for edge := range exactCommand.EdgeIDs {
				exactCommand.EdgeIDs[edge] = task031ID(identity)
				identity++
				exactCommand.ReservationIDs[edge] = task031ID(identity)
				identity++
				exactCommand.SnapshotIDs[edge] = task031ID(identity)
				identity++
			}
			branchCommand.Assignments[position] = exactCommand
			branchAuthority.Assignments[position] = arena.ExactNormalAssignmentAuthority{
				Scope: scope, Category: category,
				Revisions: arena.ExactNormalAssignmentSourceRevisions{
					SlotRevisionID: task031ID(50 + position), SlotRevision: 2,
					GraphRevisionID: task031ID(60), GraphRevision: 3,
					PoolRevisionID: poolID, PoolRevision: pool.Revision,
					HistoryRevisionID: task031ID(61), HistoryRevision: 5,
					ReservationRevisionID: task031ID(62), ReservationRevision: 6,
					ArtifactRevisionID: task031ID(70 + position), ArtifactRevision: 7,
					CategoryRevisionID: categoryLockID, CategoryRevision: categoryRevision.Revision,
				},
				Pool:           pool,
				ParticipantIDs: []uuid.UUID{draft.FirstParticipantID, draft.SecondParticipantID},
				ParticipantReservations: []arena.ExactNormalParticipantReservation{
					{
						ParticipantID: draft.FirstParticipantID, PlayerID: task031ID(80),
						Reservation: arenaReservation(81, task031ID(80), scope.TournamentID, createdAt),
					},
					{
						ParticipantID: draft.SecondParticipantID, PlayerID: task031ID(82),
						Reservation: arenaReservation(83, task031ID(82), scope.TournamentID, createdAt),
					},
				},
				Candidates:     candidates,
				GraphDigest:    sha256.Sum256([]byte("draft branch capacity graph")),
				ArtifactDigest: sha256.Sum256([]byte(fmt.Sprintf("slot artifact %d", position+1))),
			}
		}
		command.Branches[branchIndex] = branchCommand
		authority.Branches[branchIndex] = branchAuthority
	}
	return authority, command
}

func completeExactDraftBranch(
	t *testing.T,
	initial arena.DraftExecution,
	path arena.ExactDraftBranchPath,
	identity int,
) arena.DraftExecution {
	t.Helper()

	current := advanceExactDraft(t, initial, path.Actions[len(initial.Actions):], identity)
	require.Equal(t, arena.DraftExecutionStateCompleted, current.State)
	return current
}

func advanceExactDraft(
	t *testing.T,
	initial arena.DraftExecution,
	actions []arena.ExactDraftBranchAction,
	identity int,
) arena.DraftExecution {
	t.Helper()

	repository := newDraftRepositoryFake(initial)
	current := initial
	for _, action := range actions {
		at := current.TurnDeadline.Add(-time.Second)
		useCase := arena.NewDraftActionUseCase(repository, task030Clock{at: at})
		command := arena.DraftPlayerActionCommand{
			DraftID: current.ID, ExpectedRevisionID: current.RevisionID,
			ExpectedRevision: current.Revision, ExpectedServiceEpoch: current.ServiceEpoch,
			ExpectedTurn: current.Turn, CommandID: task031ID(identity),
			ResultRevisionID: task031ID(identity + 1), ActionID: task031ID(identity + 2),
			ActorID: *current.CurrentActorID, Action: action.Action, Category: action.Category,
		}
		identity += 3
		result, err := useCase.Apply(t.Context(), command)
		require.NoError(t, err)
		current = result.Draft
	}
	return current
}

func task031ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("31000000-0000-0000-0000-%012d", number))
}
