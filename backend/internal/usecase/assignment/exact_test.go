package assignment_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	assignmentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment/mocks"
)

func TestExactNormalAssignmentCommitsPrimaryAndTwoReserves(t *testing.T) {
	t.Parallel()

	authority, command := exactNormalAssignmentFixture()
	repository, state := newExactNormalAssignmentRepository(t, []assignmentusecase.ExactNormalAssignmentAuthority{authority}, 0)
	usecase := assignmentusecase.NewExactNormalAssignmentUseCase(repository)

	plan, changed, err := usecase.PlanAndCommit(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, plan.Validate())
	require.Len(t, plan.SelectedEdges, domain.AssignmentReserveCount+1)
	require.Equal(t, authority.Revisions, plan.Revisions)
	require.Equal(t, authority.GraphDigest, plan.GraphDigest)
	require.Equal(t, authority.ArtifactDigest, plan.ArtifactDigest)
	for index, edge := range plan.SelectedEdges {
		require.Equal(t, index+1, edge.Position)
		require.Equal(t, authority.Category, edge.Snapshot.Category)
		require.Equal(t, domain.AssignmentTaskKindNormal, edge.Snapshot.Kind)
	}
	require.Equal(t, 1, state.loadCount())
	require.Equal(t, 1, state.commitCount())
}

func TestExactNormalLegacyHistoryProofSurvivesVersionlessJSON(t *testing.T) {
	t.Parallel()

	authority, command := exactNormalAssignmentFixture()
	_, err := assignmentusecase.BuildExactNormalAssignment(command, authority)
	require.NoError(t, err)

	serialized, err := json.Marshal(authority.History)
	require.NoError(t, err)
	var decoded []capacity.TaskUse
	require.NoError(t, json.Unmarshal(serialized, &decoded))
	require.Equal(t, authority.History, decoded)
	reserialized, err := json.Marshal(decoded)
	require.NoError(t, err)
	require.Equal(t, serialized, reserialized)

	decodedAuthority := cloneExactNormalAuthority(authority)
	decodedAuthority.History = decoded
	decodedPlan, err := assignmentusecase.BuildExactNormalAssignment(command, decodedAuthority)
	require.NoError(t, err)
	require.NoError(t, decodedPlan.Validate())

	versionedAuthority := cloneExactNormalAuthority(authority)
	versionedAuthority.History[0].Version = 1
	versionedPlan, err := assignmentusecase.BuildExactNormalAssignment(command, versionedAuthority)
	require.NoError(t, err)
	require.NoError(t, versionedPlan.Validate())
}

func TestExactNormalAssignmentRetriesEveryAuthorityConflict(t *testing.T) {
	t.Parallel()

	mutations := map[string]func(*assignmentusecase.ExactNormalAssignmentAuthority){
		"graph": func(authority *assignmentusecase.ExactNormalAssignmentAuthority) {
			authority.Revisions.SeriesRevision++
			authority.GraphDigest = sha256.Sum256([]byte("graph-v2"))
		},
		"pool": func(authority *assignmentusecase.ExactNormalAssignmentAuthority) {
			authority.Revisions.PoolRevision++
			authority.Pool.Revision++
		},
		"history": func(authority *assignmentusecase.ExactNormalAssignmentAuthority) {
			authority.Revisions.HistoryRevision++
		},
		"reservation": func(authority *assignmentusecase.ExactNormalAssignmentAuthority) {
			authority.Revisions.RosterRevision++
			authority.ParticipantReservations[0].Reservation.Revision++
		},
		"artifact": func(authority *assignmentusecase.ExactNormalAssignmentAuthority) {
			authority.Revisions.ArtifactRevision++
			authority.ArtifactDigest = sha256.Sum256([]byte("artifact-v2"))
		},
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			first, command := exactNormalAssignmentFixture()
			second := cloneExactNormalAuthority(first)
			mutate(&second)
			repository, state := newExactNormalAssignmentRepository(
				t,
				[]assignmentusecase.ExactNormalAssignmentAuthority{first, second},
				1,
			)

			plan, changed, err := assignmentusecase.NewExactNormalAssignmentUseCase(repository).
				PlanAndCommit(t.Context(), command)
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, second.Revisions, plan.Revisions)
			require.Equal(t, second.GraphDigest, plan.GraphDigest)
			require.Equal(t, second.ArtifactDigest, plan.ArtifactDigest)
			require.Equal(t, 2, state.loadCount())
			require.Equal(t, 2, state.commitCount())
		})
	}
}

func TestExactNormalAssignmentReturnsStableConflictAfterFreshRetry(t *testing.T) {
	t.Parallel()

	authority, command := exactNormalAssignmentFixture()
	repository, state := newExactNormalAssignmentRepository(
		t,
		[]assignmentusecase.ExactNormalAssignmentAuthority{authority},
		2,
	)

	plan, changed, err := assignmentusecase.NewExactNormalAssignmentUseCase(repository).
		PlanAndCommit(t.Context(), command)
	require.Nil(t, plan)
	require.False(t, changed)
	require.ErrorIs(t, err, assignmentusecase.ErrExactNormalAssignmentConflict)
	require.Equal(t, 2, state.loadCount())
	require.Equal(t, 2, state.commitCount())
}

func TestExactNormalAssignmentConcurrentCommitHasOneWinner(t *testing.T) {
	t.Parallel()

	authority, firstCommand := exactNormalAssignmentFixture()
	secondCommand := firstCommand
	secondCommand.PlanID = task033ID(901)
	secondCommand.PlanRevisionID = task033ID(902)
	secondCommand.BranchID = task033ID(903)
	secondCommand.DecisionEvidenceID = task033ID(904)
	for index := range secondCommand.EdgeIDs {
		secondCommand.EdgeIDs[index] = task033ID(910 + index)
		secondCommand.ReservationIDs[index] = task033ID(920 + index)
		secondCommand.SnapshotIDs[index] = task033ID(930 + index)
	}
	repository, _ := newExactNormalAssignmentRepository(
		t,
		[]assignmentusecase.ExactNormalAssignmentAuthority{authority},
		0,
	)
	commands := []assignmentusecase.ExactNormalAssignmentCommand{firstCommand, secondCommand}
	results := make(chan error, len(commands))
	var group sync.WaitGroup
	for _, command := range commands {
		group.Add(1)
		go func() {
			defer group.Done()
			_, _, err := assignmentusecase.NewExactNormalAssignmentUseCase(repository).
				PlanAndCommit(context.Background(), command)
			results <- err
		}()
	}
	group.Wait()
	close(results)

	var success, conflict int
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, assignmentusecase.ErrExactNormalAssignmentConflict):
			conflict++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
}

type exactNormalAssignmentState struct {
	mu          sync.Mutex
	authorities []assignmentusecase.ExactNormalAssignmentAuthority
	conflicts   int
	loads       int
	commits     int
	committed   *assignmentusecase.ExactNormalAssignmentPlan
}

func newExactNormalAssignmentRepository(
	t *testing.T,
	authorities []assignmentusecase.ExactNormalAssignmentAuthority,
	conflicts int,
) (*assignmentmocks.MockExactNormalAssignmentRepository, *exactNormalAssignmentState) {
	t.Helper()

	state := &exactNormalAssignmentState{authorities: authorities, conflicts: conflicts}
	repository := assignmentmocks.NewMockExactNormalAssignmentRepository(t)
	repository.EXPECT().
		LoadExactNormalAssignmentAuthority(mock.Anything, mock.Anything).
		RunAndReturn(func(
			context.Context,
			assignmentusecase.ExactNormalAssignmentScope,
		) (assignmentusecase.ExactNormalAssignmentAuthority, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			index := min(state.loads, len(state.authorities)-1)
			state.loads++
			return cloneExactNormalAuthority(state.authorities[index]), nil
		})
	repository.EXPECT().
		CommitExactNormalAssignment(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			plan assignmentusecase.ExactNormalAssignmentPlan,
		) (*assignmentusecase.ExactNormalAssignmentPlan, bool, error) {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.commits++
			if state.conflicts > 0 {
				state.conflicts--
				return nil, false, domain.ErrConflict
			}
			if state.committed != nil {
				if state.committed.ProofHash == plan.ProofHash {
					cloned := *state.committed
					return &cloned, false, nil
				}
				return nil, false, domain.ErrConflict
			}
			committed := plan
			state.committed = &committed
			return &committed, true, nil
		})
	return repository, state
}

func (r *exactNormalAssignmentState) loadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loads
}

func (r *exactNormalAssignmentState) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func exactNormalAssignmentFixture() (
	assignmentusecase.ExactNormalAssignmentAuthority,
	assignmentusecase.ExactNormalAssignmentCommand,
) {
	now := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	scope := assignmentusecase.ExactNormalAssignmentScope{
		TournamentID: task033ID(1), RosterID: task033ID(2), SeriesID: task033ID(3),
		SlotID: task033ID(4), CategoryLockID: task033ID(5),
	}
	participants := []uuid.UUID{task033ID(20), task033ID(21)}
	tasks := []domain.Task{
		exactNormalTask(30, domain.CategoryWeb),
		exactNormalTask(31, domain.CategoryWeb),
		exactNormalTask(32, domain.CategoryWeb),
		exactNormalTask(33, domain.CategoryWeb),
		exactNormalTask(34, domain.CategoryCrypto),
	}
	poolID := task033ID(6)
	versions := make([]domain.TaskVersionRef, len(tasks))
	candidates := make([]assignmentusecase.ExactNormalTaskVersion, len(tasks))
	for index, task := range tasks {
		versions[index] = domain.TaskVersionRef{TaskID: task.ID, Version: index + 1}
		candidates[index] = assignmentusecase.ExactNormalTaskVersion{
			PoolRevisionID: poolID, Version: index + 1, Task: task,
		}
	}
	authority := assignmentusecase.ExactNormalAssignmentAuthority{
		Scope: scope, Category: domain.CategoryWeb,
		Revisions: assignmentusecase.ExactNormalAssignmentSourceRevisions{
			SeriesRevision: 3,
			PoolRevisionID: poolID, PoolRevision: 4,
			HistoryRevisionID: task033ID(8), HistoryRevision: 5,
			RosterRevision:     6,
			ArtifactRevisionID: task033ID(10), ArtifactRevision: 7,
			CategoryRevisionID: scope.CategoryLockID, CategoryRevision: 2,
		},
		Pool: domain.TaskPoolRevision{
			ID: poolID, Revision: 4, Kind: domain.AssignmentTaskKindNormal, Versions: versions,
		},
		ParticipantIDs: participants,
		ParticipantReservations: []assignmentusecase.ExactNormalParticipantReservation{
			{
				ParticipantID: participants[0], PlayerID: task033ID(50),
				Reservation: reservationFixture(40, task033ID(50), scope.TournamentID, now),
			},
			{
				ParticipantID: participants[1], PlayerID: task033ID(51),
				Reservation: reservationFixture(41, task033ID(51), scope.TournamentID, now),
			},
		},
		History:        []capacity.TaskUse{{ParticipantID: participants[0], TaskID: tasks[0].ID}},
		Candidates:     candidates,
		GraphDigest:    sha256.Sum256([]byte("locked-capacity-graph")),
		ArtifactDigest: sha256.Sum256([]byte("locked-slot-artifact")),
	}
	command := assignmentusecase.ExactNormalAssignmentCommand{
		Scope: scope, PlanID: task033ID(100), PlanRevisionID: task033ID(101),
		BranchID: task033ID(102), DecisionEvidenceID: task033ID(103), CreatedAt: now,
	}
	for index := range command.EdgeIDs {
		command.EdgeIDs[index] = task033ID(110 + index)
		command.ReservationIDs[index] = task033ID(120 + index)
		command.SnapshotIDs[index] = task033ID(130 + index)
	}
	return authority, command
}

func cloneExactNormalAuthority(
	authority assignmentusecase.ExactNormalAssignmentAuthority,
) assignmentusecase.ExactNormalAssignmentAuthority {
	cloned := authority
	cloned.Pool.Versions = append([]domain.TaskVersionRef(nil), authority.Pool.Versions...)
	cloned.ParticipantIDs = append([]uuid.UUID(nil), authority.ParticipantIDs...)
	cloned.ParticipantReservations = append(
		[]assignmentusecase.ExactNormalParticipantReservation(nil), authority.ParticipantReservations...,
	)
	cloned.History = append([]capacity.TaskUse(nil), authority.History...)
	cloned.Candidates = append([]assignmentusecase.ExactNormalTaskVersion(nil), authority.Candidates...)
	for index := range cloned.Candidates {
		cloned.Candidates[index].Task.Hints = append([]string(nil), authority.Candidates[index].Task.Hints...)
	}
	return cloned
}

func exactNormalTask(id int, category domain.Category) domain.Task {
	return domain.Task{
		ID: task033ID(id), Title: fmt.Sprintf("Task %d", id), Description: "Solve this task.",
		Category: category, Difficulty: domain.DifficultyHard, TimeLimit: 90,
		Flag: fmt.Sprintf("FLAG{%d}", id), Hints: []string{"first", "second", "third"},
	}
}

func reservationFixture(
	id int,
	playerID uuid.UUID,
	tournamentID uuid.UUID,
	at time.Time,
) domain.ParticipantReservation {
	return domain.ParticipantReservation{
		PlayerID: playerID, ReservationID: task033ID(id),
		TournamentID: tournamentID,
		Revision:     1, AcquiredAt: at, UpdatedAt: at,
	}
}

func task033ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", value))
}
