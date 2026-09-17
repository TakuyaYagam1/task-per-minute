package plan_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
)

func TestGoldenExactPlanCommitHardening(t *testing.T) {
	t.Parallel()

	t.Run("reconciles synchronized duplicates with one atomic write", func(t *testing.T) {
		t.Parallel()

		authority, command := planGoldenPlanExact(t)
		repository := newGoldenExactPlanRepository(t, authority)
		results := make(chan goldenPlanCallResult, 2)
		var group sync.WaitGroup
		for range 2 {
			group.Add(1)
			go func() {
				defer group.Done()
				plan, changed, err := goldenusecase.NewUseCase(repository.mock).
					PlanAndCommit(context.Background(), command)
				results <- goldenPlanCallResult{plan: plan, changed: changed, err: err}
			}()
		}
		group.Wait()
		close(results)
		changedCount := 0
		for result := range results {
			require.NoError(t, result.err)
			require.NotNil(t, result.plan)
			if result.changed {
				changedCount++
			}
		}
		require.Equal(t, 1, changedCount)
		require.Equal(t, 1, repository.state.writeCount())
	})

	t.Run("replays an exact stored command after authority advances", func(t *testing.T) {
		t.Parallel()

		authority, command := planGoldenPlanExact(t)
		repository := newGoldenExactPlanRepository(t, authority)
		first, changed, err := goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)

		advanced := authority.Snapshot()
		advanced.Revisions.HistoryRevisionID = planGoldenPlanID(9500)
		advanced.Revisions.HistoryRevision++
		advanced, err = goldenusecase.BuildAuthority(advanced)
		require.NoError(t, err)
		repository.state.mu.Lock()
		repository.state.authorities = []goldenusecase.Authority{advanced}
		repository.state.mu.Unlock()

		replayed, changed, err := goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, first.ProofHash, replayed.ProofHash)
		require.Equal(t, 1, repository.state.writeCount())
	})

	t.Run("prevents global reservation reuse and changed command replay", func(t *testing.T) {
		t.Parallel()

		authority, command := planGoldenPlanExact(t)
		repository := newGoldenExactPlanRepository(t, authority)
		_, changed, err := goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)

		competing := changedGoldenPlanCommand(command, 8000)
		plan, changed, err := goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), competing)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, goldenusecase.ErrExactPlanConflict)

		reused := command
		reused.CreatedAt = command.CreatedAt.Add(time.Second)
		plan, changed, err = goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), reused)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, goldenusecase.ErrExactPlanConflict)
		require.Equal(t, 1, repository.state.writeCount())
	})

	t.Run("rejects every repeated or aliased command identity", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(goldenusecase.Authority, *goldenusecase.Command)
		}{
			{name: "edge within group", mutate: func(_ goldenusecase.Authority, command *goldenusecase.Command) {
				command.GroupCommands[0].EdgeIDs[1] = command.GroupCommands[0].EdgeIDs[0]
			}},
			{name: "edge between groups", mutate: func(_ goldenusecase.Authority, command *goldenusecase.Command) {
				command.GroupCommands[1].EdgeIDs[0] = command.GroupCommands[0].EdgeIDs[0]
			}},
			{name: "reservation within group", mutate: func(_ goldenusecase.Authority, command *goldenusecase.Command) {
				command.GroupCommands[0].ReservationIDs[1] = command.GroupCommands[0].ReservationIDs[0]
			}},
			{name: "reservation between groups", mutate: func(_ goldenusecase.Authority, command *goldenusecase.Command) {
				command.GroupCommands[1].ReservationIDs[0] = command.GroupCommands[0].ReservationIDs[0]
			}},
			{name: "snapshot within group", mutate: func(_ goldenusecase.Authority, command *goldenusecase.Command) {
				command.GroupCommands[0].SnapshotIDs[1] = command.GroupCommands[0].SnapshotIDs[0]
			}},
			{name: "snapshot between groups", mutate: func(_ goldenusecase.Authority, command *goldenusecase.Command) {
				command.GroupCommands[1].SnapshotIDs[0] = command.GroupCommands[0].SnapshotIDs[0]
			}},
			{name: "edge aliases plan", mutate: func(_ goldenusecase.Authority, command *goldenusecase.Command) {
				command.GroupCommands[0].EdgeIDs[0] = command.PlanID
			}},
			{name: "reservation aliases plan revision", mutate: func(_ goldenusecase.Authority, command *goldenusecase.Command) {
				command.GroupCommands[0].ReservationIDs[0] = command.PlanRevisionID
			}},
			{name: "snapshot aliases candidate task", mutate: func(authority goldenusecase.Authority, command *goldenusecase.Command) {
				command.GroupCommands[0].SnapshotIDs[0] = authority.Candidates[0].Task.ID
			}},
			{name: "edge aliases another group reservation", mutate: func(_ goldenusecase.Authority, command *goldenusecase.Command) {
				command.GroupCommands[0].EdgeIDs[0] = command.GroupCommands[1].ReservationIDs[0]
			}},
			{name: "group revision aliases source revision", mutate: func(_ goldenusecase.Authority, command *goldenusecase.Command) {
				command.GroupCommands[0].GroupRevisionID = command.Expected.Revisions.SourceProjectionRevisionID
			}},
			{name: "duplicate group identity", mutate: func(_ goldenusecase.Authority, command *goldenusecase.Command) {
				command.GroupCommands[1].GroupID = command.GroupCommands[0].GroupID
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				authority, command := planGoldenPlanExact(t)
				command.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands)
				test.mutate(authority, &command)
				repository := newGoldenExactPlanRepository(t, authority)
				plan, changed, err := goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), command)
				require.Nil(t, plan)
				require.False(t, changed)
				require.ErrorIs(t, err, goldenusecase.ErrInvalidExactPlan)
				require.Equal(t, 0, repository.state.commitCount())
			})
		}

		authority, command := planGoldenPlanExact(t)
		authority.ExistingTaskReservations = []goldenusecase.TaskReservation{{
			TaskVersion: authority.Pool.Versions[0], ReservationID: planGoldenPlanID(9600),
			PlanID: planGoldenPlanID(9601), PlanRevisionID: planGoldenPlanID(9602),
		}}
		authority, err := goldenusecase.BuildAuthority(authority)
		require.NoError(t, err)
		command.Expected = authority.Expectation()
		command.PlanID = authority.ExistingTaskReservations[0].PlanID
		plan, changed, err := goldenusecase.NewUseCase(newGoldenExactPlanRepository(t, authority).mock).
			PlanAndCommit(t.Context(), command)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, goldenusecase.ErrInvalidExactPlan)
	})

	t.Run("fails closed on nil malformed or mismatched committed records", func(t *testing.T) {
		t.Parallel()

		for _, mode := range []goldenRepositoryReturnMode{
			goldenRepositoryReturnNil,
			goldenRepositoryReturnMalformed,
			goldenRepositoryReturnMismatch,
		} {
			t.Run(string(mode), func(t *testing.T) {
				authority, command := planGoldenPlanExact(t)
				repository := newGoldenExactPlanRepository(t, authority)
				repository.state.returnMode = mode
				plan, changed, err := goldenusecase.NewUseCase(repository.mock).
					PlanAndCommit(t.Context(), command)
				require.Nil(t, plan)
				require.False(t, changed)
				if mode == goldenRepositoryReturnMismatch {
					require.ErrorIs(t, err, goldenusecase.ErrExactPlanConflict)
				} else {
					require.ErrorIs(t, err, domain.ErrInternal)
				}
			})
		}
	})

	t.Run("defensively copies caller authority and returned records", func(t *testing.T) {
		t.Parallel()

		authority, command := planGoldenPlanExact(t)
		repository := newGoldenExactPlanRepository(t, authority)
		repository.state.returnAliases = true
		repository.state.returnAuthorityAliases = true
		plan, _, err := goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		wantProof := plan.ProofHash
		wantTask := plan.Groups[0].Edges[0].Snapshot.TaskID

		authority.Groups[0].ActiveParticipantIDs[0] = planGoldenPlanID(9991)
		authority.Candidates[0].Task.Hints[0] = "caller mutation"
		repository.state.mu.Lock()
		repository.state.authorities[0].Groups[0].ActiveParticipantIDs[0] = planGoldenPlanID(9994)
		repository.state.authorities[0].Candidates[0].Task.Hints[0] = "repository mutation"
		repository.state.mu.Unlock()
		plan.Authority.History = append(plan.Authority.History, assignmentusecase.TaskReceiptRef{})
		plan.Groups[0].Edges[0].Snapshot.TaskID = planGoldenPlanID(9992)
		plan.Groups[0].Edges[0].Snapshot.Hints[0] = "result mutation"

		replayed, changed, err := goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, wantProof, replayed.ProofHash)
		require.Equal(t, wantTask, replayed.Groups[0].Edges[0].Snapshot.TaskID)
		require.NoError(t, replayed.Validate())
	})

	t.Run("rejects a normal pool for Golden planning", func(t *testing.T) {
		t.Parallel()

		authority, _ := planGoldenPlanExact(t)
		authority.Pool.Kind = domain.AssignmentTaskKindNormal
		_, err := goldenusecase.BuildAuthority(authority)
		require.ErrorIs(t, err, goldenusecase.ErrInvalidExactPlan)
	})

	t.Run("rejects repeated participant task history across versions", func(t *testing.T) {
		t.Parallel()

		authority, _ := planGoldenPlanExact(t)
		repeated := authority.History[0]
		repeated.Version++
		authority.History = append(authority.History, repeated)
		_, err := goldenusecase.BuildAuthority(authority)
		require.ErrorIs(t, err, goldenusecase.ErrInvalidExactPlan)
	})

	t.Run("validates the retained authority identity namespace", func(t *testing.T) {
		t.Parallel()

		t.Run("binds historical revision identities by lineage role", func(t *testing.T) {
			authority, _ := planGoldenPlanExact(t)
			successor := goldenExactPlanSuccessorAuthority(t, authority)
			require.Len(t, successor.Groups, 2)
			require.NotNil(t, successor.Source.PreviousRevisionID)
			for _, group := range successor.Groups {
				require.Equal(t, successor.Source.PreviousRevisionID, group.Revision.SourceProjectionPreviousRevisionID())
			}
			_, err := goldenusecase.BuildAuthority(successor)
			require.NoError(t, err)

			sourceAlias := successor.Snapshot()
			sourcePreviousID := sourceAlias.Source.PreviousRevisionID.UUID()
			sourceAlias.Revisions.PoolRevisionID = sourcePreviousID
			sourceAlias.Pool.ID = sourcePreviousID
			for index := range sourceAlias.Candidates {
				sourceAlias.Candidates[index].PoolRevisionID = sourcePreviousID
				sourceAlias.Candidates[index].Health.PoolRevisionID = sourcePreviousID
			}
			_, err = goldenusecase.BuildAuthority(sourceAlias)
			require.ErrorIs(t, err, goldenusecase.ErrInvalidExactPlan)

			groupAlias := successor.Snapshot()
			groupPreviousID := groupAlias.Groups[0].Revision.PreviousRevisionID()
			require.NotNil(t, groupPreviousID)
			groupAlias.Revisions.HistoryRevisionID = groupPreviousID.UUID()
			_, err = goldenusecase.BuildAuthority(groupAlias)
			require.ErrorIs(t, err, goldenusecase.ErrInvalidExactPlan)
		})

		t.Run("candidate task aliases tournament", func(t *testing.T) {
			authority, _ := planGoldenPlanExact(t)
			oldTaskID := authority.Candidates[0].Task.ID
			newTaskID := authority.Scope.TournamentID
			authority.Candidates[0].Task.ID = newTaskID
			authority.Candidates[0].Health.TaskID = newTaskID
			authority.Candidates[0].ArtifactDigest = goldenusecase.TaskArtifactDigest(
				authority.Candidates[0].Task, authority.Candidates[0].Version,
			)
			for index := range authority.Pool.Versions {
				if authority.Pool.Versions[index].TaskID == oldTaskID {
					authority.Pool.Versions[index].TaskID = newTaskID
				}
			}
			_, err := goldenusecase.BuildAuthority(authority)
			require.ErrorIs(t, err, goldenusecase.ErrInvalidExactPlan)
		})

		t.Run("reservation owner aliases pool revision", func(t *testing.T) {
			authority, _ := planGoldenPlanExact(t)
			authority.ExistingTaskReservations = []goldenusecase.TaskReservation{{
				TaskVersion: authority.Pool.Versions[0], ReservationID: planGoldenPlanID(9700),
				PlanID: authority.Revisions.PoolRevisionID, PlanRevisionID: planGoldenPlanID(9701),
			}}
			_, err := goldenusecase.BuildAuthority(authority)
			require.ErrorIs(t, err, goldenusecase.ErrInvalidExactPlan)
		})

		t.Run("allows one exact owner pair across two reserved tasks", func(t *testing.T) {
			authority, _ := planGoldenPlanExact(t)
			authority.ExistingTaskReservations = []goldenusecase.TaskReservation{
				{TaskVersion: authority.Pool.Versions[0], ReservationID: planGoldenPlanID(9710), PlanID: planGoldenPlanID(9712), PlanRevisionID: planGoldenPlanID(9713)},
				{TaskVersion: authority.Pool.Versions[1], ReservationID: planGoldenPlanID(9711), PlanID: planGoldenPlanID(9712), PlanRevisionID: planGoldenPlanID(9713)},
			}
			built, err := goldenusecase.BuildAuthority(authority)
			require.NoError(t, err)
			require.Len(t, built.ExistingTaskReservations, 2)
			require.Equal(t, built.ExistingTaskReservations[0].PlanID, built.ExistingTaskReservations[1].PlanID)
			require.Equal(t, built.ExistingTaskReservations[0].PlanRevisionID, built.ExistingTaskReservations[1].PlanRevisionID)
		})
	})
}
