package arena_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestExactNormalAssignmentCommitsPrimaryAndTwoReserves(t *testing.T) {
	t.Parallel()

	authority, command := exactNormalAssignmentFixture()
	repository := &exactNormalAssignmentRepositoryFake{authorities: []arena.ExactNormalAssignmentAuthority{authority}}
	usecase := arena.NewExactNormalAssignmentUseCase(repository)

	plan, changed, err := usecase.PlanAndCommit(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, plan.Validate())
	require.Len(t, plan.SelectedEdges, domain.ArenaAssignmentReserveCount+1)
	require.Equal(t, authority.Revisions, plan.Revisions)
	require.Equal(t, authority.GraphDigest, plan.GraphDigest)
	require.Equal(t, authority.ArtifactDigest, plan.ArtifactDigest)
	for index, edge := range plan.SelectedEdges {
		require.Equal(t, index+1, edge.Position)
		require.Equal(t, authority.Category, edge.Snapshot.Category)
		require.Equal(t, domain.ArenaTaskKindNormal, edge.Snapshot.Kind)
	}
	require.Equal(t, 1, repository.loadCount())
	require.Equal(t, 1, repository.commitCount())
}

func TestExactNormalAssignmentRetriesEveryAuthorityConflict(t *testing.T) {
	t.Parallel()

	mutations := map[string]func(*arena.ExactNormalAssignmentAuthority){
		"graph": func(authority *arena.ExactNormalAssignmentAuthority) {
			authority.Revisions.GraphRevision++
			authority.GraphDigest = sha256.Sum256([]byte("graph-v2"))
		},
		"pool": func(authority *arena.ExactNormalAssignmentAuthority) {
			authority.Revisions.PoolRevision++
			authority.Pool.Revision++
		},
		"history": func(authority *arena.ExactNormalAssignmentAuthority) {
			authority.Revisions.HistoryRevision++
		},
		"reservation": func(authority *arena.ExactNormalAssignmentAuthority) {
			authority.Revisions.ReservationRevision++
			authority.ParticipantReservations[0].Reservation.Revision++
		},
		"artifact": func(authority *arena.ExactNormalAssignmentAuthority) {
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
			repository := &exactNormalAssignmentRepositoryFake{
				authorities: []arena.ExactNormalAssignmentAuthority{first, second},
				conflicts:   1,
			}

			plan, changed, err := arena.NewExactNormalAssignmentUseCase(repository).
				PlanAndCommit(t.Context(), command)
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, second.Revisions, plan.Revisions)
			require.Equal(t, second.GraphDigest, plan.GraphDigest)
			require.Equal(t, second.ArtifactDigest, plan.ArtifactDigest)
			require.Equal(t, 2, repository.loadCount())
			require.Equal(t, 2, repository.commitCount())
		})
	}
}

func TestExactNormalAssignmentReturnsStableConflictAfterFreshRetry(t *testing.T) {
	t.Parallel()

	authority, command := exactNormalAssignmentFixture()
	repository := &exactNormalAssignmentRepositoryFake{
		authorities: []arena.ExactNormalAssignmentAuthority{authority},
		conflicts:   2,
	}

	plan, changed, err := arena.NewExactNormalAssignmentUseCase(repository).
		PlanAndCommit(t.Context(), command)
	require.Nil(t, plan)
	require.False(t, changed)
	require.ErrorIs(t, err, arena.ErrExactNormalAssignmentConflict)
	require.Equal(t, 2, repository.loadCount())
	require.Equal(t, 2, repository.commitCount())
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
	repository := &competingExactNormalAssignmentRepository{authority: authority}
	commands := []arena.ExactNormalAssignmentCommand{firstCommand, secondCommand}
	results := make(chan error, len(commands))
	var group sync.WaitGroup
	for _, command := range commands {
		group.Add(1)
		go func() {
			defer group.Done()
			_, _, err := arena.NewExactNormalAssignmentUseCase(repository).
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
		case errors.Is(err, arena.ErrExactNormalAssignmentConflict):
			conflict++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
}

type exactNormalAssignmentRepositoryFake struct {
	mu          sync.Mutex
	authorities []arena.ExactNormalAssignmentAuthority
	conflicts   int
	loads       int
	commits     int
	committed   *arena.ExactNormalAssignmentPlan
}

func (r *exactNormalAssignmentRepositoryFake) LoadExactNormalAssignmentAuthority(
	_ context.Context,
	_ arena.ExactNormalAssignmentScope,
) (arena.ExactNormalAssignmentAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := min(r.loads, len(r.authorities)-1)
	r.loads++
	return cloneExactNormalAuthority(r.authorities[index]), nil
}

func (r *exactNormalAssignmentRepositoryFake) CommitExactNormalAssignment(
	_ context.Context,
	plan arena.ExactNormalAssignmentPlan,
) (*arena.ExactNormalAssignmentPlan, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits++
	if r.conflicts > 0 {
		r.conflicts--
		return nil, false, domain.ErrConflict
	}
	if r.committed != nil {
		if r.committed.ProofHash == plan.ProofHash {
			cloned := *r.committed
			return &cloned, false, nil
		}
		return nil, false, domain.ErrConflict
	}
	committed := plan
	r.committed = &committed
	return &committed, true, nil
}

func (r *exactNormalAssignmentRepositoryFake) loadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loads
}

func (r *exactNormalAssignmentRepositoryFake) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

type competingExactNormalAssignmentRepository struct {
	mu        sync.Mutex
	authority arena.ExactNormalAssignmentAuthority
	winner    *arena.ExactNormalAssignmentPlan
}

func (r *competingExactNormalAssignmentRepository) LoadExactNormalAssignmentAuthority(
	_ context.Context,
	_ arena.ExactNormalAssignmentScope,
) (arena.ExactNormalAssignmentAuthority, error) {
	return cloneExactNormalAuthority(r.authority), nil
}

func (r *competingExactNormalAssignmentRepository) CommitExactNormalAssignment(
	_ context.Context,
	plan arena.ExactNormalAssignmentPlan,
) (*arena.ExactNormalAssignmentPlan, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.winner != nil {
		return nil, false, domain.ErrConflict
	}
	committed := plan
	r.winner = &committed
	return &committed, true, nil
}

func exactNormalAssignmentFixture() (
	arena.ExactNormalAssignmentAuthority,
	arena.ExactNormalAssignmentCommand,
) {
	now := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)
	scope := arena.ExactNormalAssignmentScope{
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
	versions := make([]arena.TaskVersionRef, len(tasks))
	candidates := make([]arena.ExactNormalTaskVersion, len(tasks))
	for index, task := range tasks {
		versions[index] = arena.TaskVersionRef{TaskID: task.ID, Version: index + 1}
		candidates[index] = arena.ExactNormalTaskVersion{
			PoolRevisionID: poolID, Version: index + 1, Task: task,
		}
	}
	authority := arena.ExactNormalAssignmentAuthority{
		Scope: scope, Category: domain.CategoryWeb,
		Revisions: arena.ExactNormalAssignmentSourceRevisions{
			SlotRevisionID: task033ID(11), SlotRevision: 2,
			GraphRevisionID: task033ID(7), GraphRevision: 3,
			PoolRevisionID: poolID, PoolRevision: 4,
			HistoryRevisionID: task033ID(8), HistoryRevision: 5,
			ReservationRevisionID: task033ID(9), ReservationRevision: 6,
			ArtifactRevisionID: task033ID(10), ArtifactRevision: 7,
			CategoryRevisionID: scope.CategoryLockID, CategoryRevision: 2,
		},
		Pool: arena.TaskPoolRevision{
			ID: poolID, Revision: 4, Kind: domain.ArenaTaskKindNormal, Versions: versions,
		},
		ParticipantIDs: participants,
		ParticipantReservations: []arena.ExactNormalParticipantReservation{
			{
				ParticipantID: participants[0], PlayerID: task033ID(50),
				Reservation: arenaReservation(40, task033ID(50), scope.TournamentID, now),
			},
			{
				ParticipantID: participants[1], PlayerID: task033ID(51),
				Reservation: arenaReservation(41, task033ID(51), scope.TournamentID, now),
			},
		},
		History:        []arena.CapacityTaskUse{{ParticipantID: participants[0], TaskID: tasks[0].ID}},
		Candidates:     candidates,
		GraphDigest:    sha256.Sum256([]byte("locked-capacity-graph")),
		ArtifactDigest: sha256.Sum256([]byte("locked-slot-artifact")),
	}
	command := arena.ExactNormalAssignmentCommand{
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
	authority arena.ExactNormalAssignmentAuthority,
) arena.ExactNormalAssignmentAuthority {
	cloned := authority
	cloned.Pool.Versions = append([]arena.TaskVersionRef(nil), authority.Pool.Versions...)
	cloned.ParticipantIDs = append([]uuid.UUID(nil), authority.ParticipantIDs...)
	cloned.ParticipantReservations = append(
		[]arena.ExactNormalParticipantReservation(nil), authority.ParticipantReservations...,
	)
	cloned.History = append([]arena.CapacityTaskUse(nil), authority.History...)
	cloned.Candidates = append([]arena.ExactNormalTaskVersion(nil), authority.Candidates...)
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

func arenaReservation(
	id int,
	playerID uuid.UUID,
	tournamentID uuid.UUID,
	at time.Time,
) domain.ParticipantReservation {
	return domain.ParticipantReservation{
		PlayerID: playerID, ReservationID: task033ID(id),
		OwnerKind: domain.ParticipantReservationOwnerArena, OwnerID: tournamentID,
		Revision: 1, AcquiredAt: at, UpdatedAt: at,
	}
}

func task033ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", value))
}
