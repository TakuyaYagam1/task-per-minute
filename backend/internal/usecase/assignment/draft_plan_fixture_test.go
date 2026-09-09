package assignment_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	assignmentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment/mocks"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	draftmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft/mocks"
)

func assertExactDraftReservations(t *testing.T, plan *assignmentusecase.ExactDraftBranchPlan, want int) {
	t.Helper()

	seen := make(map[domain.TaskVersionRef]struct{}, want)
	for _, branch := range plan.Branches {
		branchSeen := make(map[domain.TaskVersionRef]struct{}, len(branch.Assignments)*3)
		require.Equal(t, assignmentusecase.ExactDraftBranchStateReserved, branch.State)
		require.Len(t, branch.Assignments, len(branch.Path.Categories))
		for position, assignment := range branch.Assignments {
			require.Equal(t, position+1, assignment.Position)
			require.Equal(t, branch.Path.Categories[position], assignment.Plan.Category)
			require.Equal(t, assignmentusecase.ExactDraftReservationStateReserved, assignment.State)
			for _, edge := range assignment.Plan.SelectedEdges {
				ref := domain.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
				if _, duplicate := branchSeen[ref]; duplicate {
					t.Fatalf("task version %v was reused inside reachable branch %q", ref, branch.Path.Key)
				}
				branchSeen[ref] = struct{}{}
				seen[ref] = struct{}{}
			}
		}
		require.Len(t, branchSeen, len(branch.Assignments)*3)
	}
	require.Len(t, seen, want)
}

type exactDraftBranchPlanRepositoryState struct {
	mu                  sync.Mutex
	authorities         []assignmentusecase.ExactDraftBranchPlanAuthority
	plan                *assignmentusecase.ExactDraftBranchPlan
	completion          *draftusecase.Execution
	planConflicts       int
	activationConflicts int
	planLoads           int
	planCommits         int
	activationLoads     int
	activationCommits   int
}

type exactDraftBranchPlanRepositoryHarness struct {
	mock  *assignmentmocks.MockExactDraftBranchPlanRepository
	state *exactDraftBranchPlanRepositoryState
}

func newExactDraftBranchPlanRepositoryHarness(
	t *testing.T,
	state *exactDraftBranchPlanRepositoryState,
) *exactDraftBranchPlanRepositoryHarness {
	t.Helper()

	repository := assignmentmocks.NewMockExactDraftBranchPlanRepository(t)
	repository.EXPECT().LoadExactDraftBranchPlanAuthority(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			_ uuid.UUID,
		) (assignmentusecase.ExactDraftBranchPlanAuthority, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			index := min(state.planLoads, len(state.authorities)-1)
			state.planLoads++
			return state.authorities[index], nil
		}).Maybe()
	repository.EXPECT().CommitExactDraftBranchPlan(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			plan assignmentusecase.ExactDraftBranchPlan,
		) (*assignmentusecase.ExactDraftBranchPlan, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.planCommits++
			if state.planConflicts > 0 {
				state.planConflicts--
				return nil, false, domain.ErrConflict
			}
			if state.plan != nil {
				recorded := *state.plan
				return &recorded, false, nil
			}
			recorded := plan
			state.plan = &recorded
			return &recorded, true, nil
		}).Maybe()
	repository.EXPECT().LoadExactDraftBranchActivation(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			_ uuid.UUID,
		) (*assignmentusecase.ExactDraftBranchPlan, *draftusecase.Execution, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.activationLoads++
			if state.plan == nil || state.completion == nil {
				return nil, nil, nil
			}
			plan := *state.plan
			draft := draftusecase.CloneExecution(*state.completion)
			return &plan, &draft, nil
		}).Maybe()
	repository.EXPECT().CommitExactDraftBranchActivation(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			plan assignmentusecase.ExactDraftBranchPlan,
		) (*assignmentusecase.ExactDraftBranchPlan, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.activationCommits++
			if state.activationConflicts > 0 {
				state.activationConflicts--
				return nil, false, domain.ErrConflict
			}
			recorded := plan
			state.plan = &recorded
			return &recorded, true, nil
		}).Maybe()
	return &exactDraftBranchPlanRepositoryHarness{mock: repository, state: state}
}

func setExactDraftCompletion(
	state *exactDraftBranchPlanRepositoryState,
	draft draftusecase.Execution,
	activationConflicts int,
) {
	state.mu.Lock()
	defer state.mu.Unlock()
	recorded := draftusecase.CloneExecution(draft)
	state.completion = &recorded
	state.activationConflicts = activationConflicts
}

func exactDraftPlanLoadCount(state *exactDraftBranchPlanRepositoryState) int {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.planLoads
}

func exactDraftPlanCommitCount(state *exactDraftBranchPlanRepositoryState) int {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.planCommits
}

func exactDraftActivationLoadCount(state *exactDraftBranchPlanRepositoryState) int {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.activationLoads
}

func exactDraftActivationCommitCount(state *exactDraftBranchPlanRepositoryState) int {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.activationCommits
}

func exactDraftBranchPlanFixture(
	t *testing.T,
	format domain.SeriesFormat,
) (assignmentusecase.ExactDraftBranchPlanAuthority, assignmentusecase.ExactDraftBranchPlanCommand) {
	t.Helper()

	createdAt := time.Date(2026, time.August, 30, 17, 0, 0, 0, time.UTC)
	stage := domain.TournamentStageSwiss
	if format == domain.SeriesFormatBO3 {
		stage = domain.TournamentStageFinal
	}
	categoryRevision := exactDraftCategoryRevision(t, stage, format, task031ID(1), createdAt)
	draft, err := draftusecase.StartExecution(draftusecase.ExecutionStartCommand{
		CategoryRevision: categoryRevision, DraftID: task031ID(2), InitialRevisionID: task031ID(3),
		DecisionEvidenceID: task031ID(4), ParticipantIDs: [2]uuid.UUID{task031ID(5), task031ID(6)},
		ServiceEpoch: task031ID(7), CommandID: task031ID(8), StartedAt: createdAt,
	})
	require.NoError(t, err)
	paths, err := assignmentusecase.ReachableExactDraftBranches(draft)
	require.NoError(t, err)

	poolID := task031ID(20)
	capacity := (domain.AssignmentReserveCount + 1) * len(draft.Pool)
	versions := make([]domain.TaskVersionRef, 0, capacity)
	candidates := make([]assignmentusecase.ExactNormalTaskVersion, 0, capacity)
	nextID := 10000
	for _, category := range draft.Pool {
		for range domain.AssignmentReserveCount + 1 {
			taskID := task031ID(nextID)
			nextID++
			version := len(versions) + 1
			task := domain.Task{
				ID: taskID, Title: fmt.Sprintf("Task %d", version), Description: "Solve this task.",
				Category: category, Difficulty: domain.DifficultyHard, TimeLimit: 180,
				Flag: fmt.Sprintf("FLAG{%d}", version), Hints: []string{"first", "second", "third"},
			}
			versions = append(versions, domain.TaskVersionRef{TaskID: taskID, Version: version})
			candidates = append(candidates, assignmentusecase.ExactNormalTaskVersion{
				PoolRevisionID: poolID, Version: version, Task: task,
			})
		}
	}
	pool := domain.TaskPoolRevision{
		ID: poolID, Revision: 4, Kind: domain.AssignmentTaskKindNormal, Versions: versions,
	}

	planID := task031ID(21)
	planRevisionID := task031ID(22)
	categoryLockID := categoryRevision.ID
	slotIDs := [3]uuid.UUID{task031ID(30), task031ID(31), task031ID(32)}
	command := assignmentusecase.ExactDraftBranchPlanCommand{
		PlanID: planID, PlanRevisionID: planRevisionID, DraftID: draft.ID,
		ExpectedDraftRevisionID: draft.RevisionID, ExpectedDraftRevision: draft.Revision,
		CreatedAt: createdAt.Add(time.Second),
		Branches:  make([]assignmentusecase.ExactDraftBranchCommand, len(paths)),
	}
	authority := assignmentusecase.ExactDraftBranchPlanAuthority{
		Draft:    draft,
		Branches: make([]assignmentusecase.ExactDraftBranchAuthority, len(paths)),
	}
	identity := 100
	for branchIndex, path := range paths {
		branchID := task031ID(identity)
		identity++
		branchCommand := assignmentusecase.ExactDraftBranchCommand{
			BranchID: branchID, Key: path.Key,
			Assignments: make([]assignmentusecase.ExactNormalAssignmentCommand, len(path.Categories)),
		}
		branchAuthority := assignmentusecase.ExactDraftBranchAuthority{
			Key:         path.Key,
			Assignments: make([]assignmentusecase.ExactNormalAssignmentAuthority, len(path.Categories)),
		}
		for position, category := range path.Categories {
			branchCommand.ChildBranchIDs[position] = task031ID(identity)
			identity++
			scope := assignmentusecase.ExactNormalAssignmentScope{
				TournamentID: task031ID(40), RosterID: categoryRevision.RosterID,
				SeriesID: draft.SeriesID, SlotID: slotIDs[position], CategoryLockID: categoryLockID,
			}
			exactCommand := assignmentusecase.ExactNormalAssignmentCommand{
				Scope: scope, PlanID: planID, PlanRevisionID: planRevisionID,
				BranchID: branchCommand.ChildBranchIDs[position], DecisionEvidenceID: task031ID(identity),
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
			branchAuthority.Assignments[position] = assignmentusecase.ExactNormalAssignmentAuthority{
				Scope: scope, Category: category,
				Revisions: assignmentusecase.ExactNormalAssignmentSourceRevisions{
					SeriesRevision: 3,
					PoolRevisionID: poolID, PoolRevision: pool.Revision,
					HistoryRevisionID: task031ID(61), HistoryRevision: 5,
					RosterRevision:     6,
					ArtifactRevisionID: task031ID(70 + position), ArtifactRevision: 7,
					CategoryRevisionID: categoryLockID, CategoryRevision: categoryRevision.Revision,
				},
				Pool:           pool,
				ParticipantIDs: []uuid.UUID{draft.FirstParticipantID, draft.SecondParticipantID},
				ParticipantReservations: []assignmentusecase.ExactNormalParticipantReservation{
					{
						ParticipantID: draft.FirstParticipantID, PlayerID: task031ID(80),
						Reservation: exactDraftReservation(81, task031ID(80), scope.TournamentID, createdAt),
					},
					{
						ParticipantID: draft.SecondParticipantID, PlayerID: task031ID(82),
						Reservation: exactDraftReservation(83, task031ID(82), scope.TournamentID, createdAt),
					},
				},
				Candidates:     candidates,
				GraphDigest:    sha256.Sum256([]byte("draft branch capacity graph")),
				ArtifactDigest: sha256.Sum256(fmt.Appendf(nil, "slot artifact %d", position+1)),
			}
		}
		command.Branches[branchIndex] = branchCommand
		authority.Branches[branchIndex] = branchAuthority
	}
	return authority, command
}

func completeExactDraftBranch(
	t *testing.T,
	initial draftusecase.Execution,
	path assignmentusecase.ExactDraftBranchPath,
	identity int,
) draftusecase.Execution {
	t.Helper()

	current := advanceExactDraft(t, initial, path.Actions[len(initial.Actions):], identity)
	require.Equal(t, draftusecase.ExecutionStateCompleted, current.State)
	return current
}

func advanceExactDraft(
	t *testing.T,
	initial draftusecase.Execution,
	actions []assignmentusecase.ExactDraftBranchAction,
	identity int,
) draftusecase.Execution {
	t.Helper()

	repository := newExactDraftExecutionRepository(t, initial)
	current := initial
	for _, action := range actions {
		at := current.TurnDeadline.Add(-time.Second)
		clock := draftmocks.NewMockClock(t)
		clock.EXPECT().Now().Return(at).Once()
		useCase := draftusecase.NewActionUseCase(repository, clock)
		command := draftusecase.PlayerActionCommand{
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

type exactDraftExecutionRepositoryState struct {
	mu       sync.Mutex
	current  draftusecase.Execution
	commands map[uuid.UUID]draftusecase.Execution
}

func newExactDraftExecutionRepository(
	t *testing.T,
	initial draftusecase.Execution,
) *draftmocks.MockRepository {
	t.Helper()

	cloned := draftusecase.CloneExecution(initial)
	state := &exactDraftExecutionRepositoryState{
		current: cloned,
		commands: map[uuid.UUID]draftusecase.Execution{
			initial.CommandID: cloned,
		},
	}
	repository := draftmocks.NewMockRepository(t)
	repository.EXPECT().LoadDraft(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, draftID uuid.UUID) (*draftusecase.Execution, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.current.ID != draftID {
				return nil, nil
			}
			current := draftusecase.CloneExecution(state.current)
			return &current, nil
		}).Maybe()
	repository.EXPECT().FindDraftCommand(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			draftID uuid.UUID,
			commandID uuid.UUID,
		) (*draftusecase.Execution, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			recorded, ok := state.commands[commandID]
			if !ok || recorded.ID != draftID {
				return nil, nil
			}
			result := draftusecase.CloneExecution(recorded)
			return &result, nil
		}).Maybe()
	repository.EXPECT().CommitDraftRevisions(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			expected draftusecase.RevisionExpectation,
			revisions []draftusecase.Execution,
		) (*draftusecase.Execution, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.current.RevisionID != expected.RevisionID || state.current.Revision != expected.Revision ||
				state.current.ServiceEpoch != expected.ServiceEpoch {
				return nil, false, domain.ErrConflict
			}
			if len(revisions) == 0 {
				return nil, false, domain.ErrValidation
			}
			current := state.current
			for _, revision := range revisions {
				if recorded, ok := state.commands[revision.CommandID]; ok {
					result := draftusecase.CloneExecution(recorded)
					return &result, false, nil
				}
				if revision.ID != current.ID || revision.PreviousRevisionID != current.RevisionID ||
					revision.Revision != current.Revision+1 {
					return nil, false, domain.ErrConflict
				}
				current = draftusecase.CloneExecution(revision)
				state.commands[revision.CommandID] = current
			}
			state.current = current
			result := draftusecase.CloneExecution(current)
			return &result, true, nil
		}).Maybe()
	return repository
}

func exactDraftCategoryRevision(
	t *testing.T,
	stage domain.TournamentStage,
	format domain.SeriesFormat,
	id uuid.UUID,
	createdAt time.Time,
) draftusecase.CategoryRevision {
	t.Helper()

	categories := []domain.Category{
		domain.CategoryCrypto,
		domain.CategoryReverse,
		domain.CategoryWeb,
	}
	if format == domain.SeriesFormatBO3 {
		categories = []domain.Category{
			domain.CategoryCrypto,
			domain.CategoryForensics,
			domain.CategoryPwn,
			domain.CategoryReverse,
			domain.CategoryWeb,
		}
	}
	revision := draftusecase.CategoryRevision{
		ID: id, TournamentID: task031ID(9001), SeriesID: task031ID(9002),
		RosterID: task031ID(9003), Revision: 1, Stage: stage, Format: format,
		Mode: domain.CategoryModeDraft, SourceContentRevision: 1,
		CategoryPool: domain.CategoryPoolRevision{
			ID: task031ID(9004), Revision: 1, Format: format, Categories: categories,
		},
		CreatedAt: createdAt,
	}
	require.NoError(t, revision.Validate())
	return revision
}

func task031ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("31000000-0000-0000-0000-%012d", number))
}

func exactDraftReservation(
	id int,
	playerID uuid.UUID,
	tournamentID uuid.UUID,
	at time.Time,
) domain.ParticipantReservation {
	return domain.ParticipantReservation{
		PlayerID: playerID,
		ReservationID: uuid.MustParse(
			fmt.Sprintf("00000000-0000-0000-0000-%012d", id),
		),
		TournamentID: tournamentID,
		Revision:     1,
		AcquiredAt:   at,
		UpdatedAt:    at,
	}
}
