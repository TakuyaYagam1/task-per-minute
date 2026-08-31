package arena_test

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestGoldenExactPlan(t *testing.T) {
	t.Parallel()

	t.Run("matches every multi-party group with one primary and two reserves", func(t *testing.T) {
		t.Parallel()

		authority, command := goldenExactPlanFixture(t)
		repository := newGoldenExactPlanRepository(authority)
		plan, changed, err := arena.NewGoldenExactPlanUseCase(repository).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, plan.Validate())
		require.Len(t, plan.Groups, 2)
		require.Len(t, plan.Groups[0].ParticipantIDs, 2)
		require.Len(t, plan.Groups[1].ParticipantIDs, 3)

		selected := make(map[arena.TaskVersionRef]uuid.UUID)
		for _, group := range plan.Groups {
			require.Len(t, group.Edges, domain.ArenaAssignmentReserveCount+1)
			for index, edge := range group.Edges {
				require.Equal(t, index+1, edge.Position)
				require.Equal(t, domain.ArenaTaskKindGolden, edge.Snapshot.Kind)
				ref := arena.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
				require.NotContains(t, selected, ref)
				selected[ref] = group.GroupID
			}
		}
		require.Len(t, selected, 6)
		for _, edge := range plan.Groups[1].Edges {
			require.Less(t, goldenTaskNumber(edge.Snapshot.TaskID), 503, "matching must reassign scarce tasks to the second group")
		}
		require.Equal(t, 1, repository.writeCount())
	})

	t.Run("plans the authoritative non-empty subset of active topology groups", func(t *testing.T) {
		t.Parallel()

		authority, command := goldenExactPlanFixture(t)
		active := make(map[uuid.UUID]struct{}, len(authority.Groups[0].ActiveParticipantIDs))
		for _, participantID := range authority.Groups[0].ActiveParticipantIDs {
			active[participantID] = struct{}{}
		}
		authority.Groups = authority.Groups[:1]
		reservations := authority.ParticipantReservations[:0]
		for _, reservation := range authority.ParticipantReservations {
			if _, exists := active[reservation.ParticipantID]; exists {
				reservations = append(reservations, reservation)
			}
		}
		authority.ParticipantReservations = reservations
		authority.History = nil
		authority, err := arena.BuildGoldenExactPlanAuthority(authority)
		require.NoError(t, err)
		command.Expected = authority.Expectation()
		command.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands[:1])
		plan, changed, err := arena.NewGoldenExactPlanUseCase(newGoldenExactPlanRepository(authority)).
			PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Len(t, plan.Groups, 1)
		require.Equal(t, authority.Groups[0].Revision.GroupID(), plan.Groups[0].GroupID)
		require.Len(t, plan.Groups[0].Edges, domain.ArenaAssignmentReserveCount+1)
	})

	t.Run("excludes a TaskID used at another version", func(t *testing.T) {
		t.Parallel()

		authority, command := goldenExactPlanFixture(t)
		usedTaskID := authority.Pool.Versions[0].TaskID
		authority.History = append(authority.History, arena.ArenaTaskReceiptRef{
			ParticipantID: authority.Groups[0].ActiveParticipantIDs[0],
			TaskID:        usedTaskID, Version: authority.Pool.Versions[0].Version - 1,
		})
		authority, err := arena.BuildGoldenExactPlanAuthority(authority)
		require.NoError(t, err)
		command.Expected = authority.Expectation()
		repository := newGoldenExactPlanRepository(authority)
		plan, _, err := arena.NewGoldenExactPlanUseCase(repository).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		for _, edge := range plan.Groups[0].Edges {
			require.NotEqual(t, usedTaskID, edge.Snapshot.TaskID)
		}
		groupBReceived := false
		for _, edge := range plan.Groups[1].Edges {
			groupBReceived = groupBReceived || edge.Snapshot.TaskID == usedTaskID
		}
		require.True(t, groupBReceived, "another group may use a TaskID absent from its own members' history")
	})

	t.Run("returns stable stale and insufficient matching errors", func(t *testing.T) {
		t.Parallel()

		authority, command := goldenExactPlanFixture(t)
		staleCases := []struct {
			name   string
			mutate func(*arena.GoldenExactPlanExpectation)
		}{
			{name: "source revision", mutate: func(value *arena.GoldenExactPlanExpectation) {
				value.Revisions.SourceProjectionRevisionID = task046RevisionID(9001)
			}},
			{name: "group revision id", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.GroupSetRevisionID = task046ID(9002) }},
			{name: "group revision", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.GroupSetRevision++ }},
			{name: "pool revision id", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.PoolRevisionID = task046ID(9003) }},
			{name: "pool revision", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.PoolRevision++ }},
			{name: "history revision id", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.HistoryRevisionID = task046ID(9004) }},
			{name: "history revision", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.HistoryRevision++ }},
			{name: "health revision id", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.TaskHealthRevisionID = task046ID(9005) }},
			{name: "health revision", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.TaskHealthRevision++ }},
			{name: "artifact revision id", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.ArtifactRevisionID = task046ID(9006) }},
			{name: "artifact revision", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.ArtifactRevision++ }},
			{name: "reservation revision id", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.ReservationRevisionID = task046ID(9007) }},
			{name: "reservation revision", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.ReservationRevision++ }},
			{name: "membership revision id", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.MembershipRevisionID = task046ID(9008) }},
			{name: "membership revision", mutate: func(value *arena.GoldenExactPlanExpectation) { value.Revisions.MembershipRevision++ }},
			{name: "source digest", mutate: func(value *arena.GoldenExactPlanExpectation) {
				value.SourcePayloadDigest = sha256.Sum256([]byte("stale source"))
			}},
			{name: "group digest", mutate: func(value *arena.GoldenExactPlanExpectation) {
				value.GroupDigest = sha256.Sum256([]byte("stale group"))
			}},
			{name: "pool digest", mutate: func(value *arena.GoldenExactPlanExpectation) { value.PoolDigest = sha256.Sum256([]byte("stale pool")) }},
			{name: "history digest", mutate: func(value *arena.GoldenExactPlanExpectation) {
				value.HistoryDigest = sha256.Sum256([]byte("stale history"))
			}},
			{name: "health digest", mutate: func(value *arena.GoldenExactPlanExpectation) {
				value.TaskHealthDigest = sha256.Sum256([]byte("stale health"))
			}},
			{name: "artifact digest", mutate: func(value *arena.GoldenExactPlanExpectation) {
				value.ArtifactDigest = sha256.Sum256([]byte("stale artifact"))
			}},
			{name: "reservation digest", mutate: func(value *arena.GoldenExactPlanExpectation) {
				value.ReservationDigest = sha256.Sum256([]byte("stale reservation"))
			}},
			{name: "membership digest", mutate: func(value *arena.GoldenExactPlanExpectation) {
				value.MembershipDigest = sha256.Sum256([]byte("stale membership"))
			}},
		}
		for _, test := range staleCases {
			t.Run(test.name, func(t *testing.T) {
				stale := command
				stale.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands)
				test.mutate(&stale.Expected)
				repository := newGoldenExactPlanRepository(authority)
				plan, changed, err := arena.NewGoldenExactPlanUseCase(repository).PlanAndCommit(t.Context(), stale)
				require.Nil(t, plan)
				require.False(t, changed)
				require.ErrorIs(t, err, arena.ErrGoldenExactPlanStale)
				require.Equal(t, 0, repository.commitCount())
			})
		}

		short := authority.Snapshot()
		short.Candidates = short.Candidates[:5]
		short.Pool.Versions = short.Pool.Versions[:5]
		short, err := arena.BuildGoldenExactPlanAuthority(short)
		require.NoError(t, err)
		shortCommand := command
		shortCommand.Expected = short.Expectation()
		shortCommand.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands)
		plan, changed, err := arena.NewGoldenExactPlanUseCase(newGoldenExactPlanRepository(short)).
			PlanAndCommit(t.Context(), shortCommand)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrGoldenExactPlanInsufficient)

		reserved := authority.Snapshot()
		reserved.ExistingTaskReservations = []arena.GoldenTaskReservation{{
			TaskVersion: reserved.Pool.Versions[0], ReservationID: task046ID(9300),
			PlanID: task046ID(9301), PlanRevisionID: task046ID(9302),
		}}
		reserved, err = arena.BuildGoldenExactPlanAuthority(reserved)
		require.NoError(t, err)
		reservedCommand := command
		reservedCommand.Expected = reserved.Expectation()
		reservedCommand.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands)
		plan, changed, err = arena.NewGoldenExactPlanUseCase(newGoldenExactPlanRepository(reserved)).
			PlanAndCommit(t.Context(), reservedCommand)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrGoldenExactPlanInsufficient)
	})

	t.Run("retries the exact deterministic plan then reports bounded conflict", func(t *testing.T) {
		t.Parallel()

		authority, command := goldenExactPlanFixture(t)
		repository := newGoldenExactPlanRepository(authority)
		repository.conflicts = 1
		plan, changed, err := arena.NewGoldenExactPlanUseCase(repository).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, 2, repository.loadCount())
		require.Equal(t, 2, repository.commitCount())
		require.Len(t, repository.attemptedProofs, 2)
		require.Equal(t, repository.attemptedProofs[0], repository.attemptedProofs[1])
		require.Equal(t, plan.ProofHash, repository.attemptedProofs[1])

		blocked := newGoldenExactPlanRepository(authority)
		blocked.conflicts = 2
		plan, changed, err = arena.NewGoldenExactPlanUseCase(blocked).PlanAndCommit(t.Context(), command)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrGoldenExactPlanConflict)
		require.Equal(t, 2, blocked.commitCount())

		mutableCommand := command
		mutableCommand.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands)
		frozen := newGoldenExactPlanRepository(authority)
		frozen.conflicts = 1
		frozen.onConflict = func() {
			mutableCommand.GroupCommands[0].EdgeIDs[0] = task046ID(9499)
		}
		plan, changed, err = arena.NewGoldenExactPlanUseCase(frozen).PlanAndCommit(t.Context(), mutableCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, command.GroupCommands[0].EdgeIDs[0], plan.Groups[0].Edges[0].ID)
		require.Equal(t, frozen.attemptedProofs[0], frozen.attemptedProofs[1])
	})

	t.Run("rejects unhealthy or mismatched candidate authority", func(t *testing.T) {
		t.Parallel()

		ineligible := []struct {
			name   string
			mutate func(*arena.GoldenExactTaskVersion)
		}{
			{name: "missing", mutate: func(value *arena.GoldenExactTaskVersion) { value.Health.Exists = false }},
			{name: "disabled", mutate: func(value *arena.GoldenExactTaskVersion) { value.Health.Enabled = false }},
			{name: "unhealthy", mutate: func(value *arena.GoldenExactTaskVersion) { value.Health.Healthy = false }},
			{name: "mutable", mutate: func(value *arena.GoldenExactTaskVersion) { value.Health.MutationLocked = false }},
			{name: "publicly exposed", mutate: func(value *arena.GoldenExactTaskVersion) { value.Health.PubliclyExposed = true }},
		}
		for _, test := range ineligible {
			t.Run(test.name, func(t *testing.T) {
				authority, command := goldenExactPlanFixture(t)
				test.mutate(&authority.Candidates[0])
				authority, err := arena.BuildGoldenExactPlanAuthority(authority)
				require.NoError(t, err)
				command.Expected = authority.Expectation()
				plan, changed, err := arena.NewGoldenExactPlanUseCase(newGoldenExactPlanRepository(authority)).
					PlanAndCommit(t.Context(), command)
				require.Nil(t, plan)
				require.False(t, changed)
				require.ErrorIs(t, err, arena.ErrGoldenExactPlanInsufficient)
			})
		}

		invalid := []struct {
			name   string
			mutate func(*arena.GoldenExactTaskVersion)
		}{
			{name: "wrong pool", mutate: func(value *arena.GoldenExactTaskVersion) { value.Health.PoolKind = domain.ArenaTaskKindNormal }},
			{name: "artifact changed", mutate: func(value *arena.GoldenExactTaskVersion) { value.ArtifactDigest = sha256.Sum256([]byte("changed")) }},
		}
		for _, test := range invalid {
			t.Run(test.name, func(t *testing.T) {
				authority, _ := goldenExactPlanFixture(t)
				test.mutate(&authority.Candidates[0])
				_, err := arena.BuildGoldenExactPlanAuthority(authority)
				require.ErrorIs(t, err, arena.ErrInvalidGoldenExactPlan)
			})
		}
	})

	t.Run("reconciles synchronized duplicates with one atomic write", func(t *testing.T) {
		t.Parallel()

		authority, command := goldenExactPlanFixture(t)
		repository := newGoldenExactPlanRepository(authority)
		results := make(chan goldenPlanCallResult, 2)
		var group sync.WaitGroup
		for range 2 {
			group.Add(1)
			go func() {
				defer group.Done()
				plan, changed, err := arena.NewGoldenExactPlanUseCase(repository).
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
		require.Equal(t, 1, repository.writeCount())
	})

	t.Run("replays an exact stored command after authority advances", func(t *testing.T) {
		t.Parallel()

		authority, command := goldenExactPlanFixture(t)
		repository := newGoldenExactPlanRepository(authority)
		first, changed, err := arena.NewGoldenExactPlanUseCase(repository).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)

		advanced := authority.Snapshot()
		advanced.Revisions.HistoryRevisionID = task046ID(9500)
		advanced.Revisions.HistoryRevision++
		advanced, err = arena.BuildGoldenExactPlanAuthority(advanced)
		require.NoError(t, err)
		repository.mu.Lock()
		repository.authorities = []arena.GoldenExactPlanAuthority{advanced}
		repository.mu.Unlock()

		replayed, changed, err := arena.NewGoldenExactPlanUseCase(repository).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, first.ProofHash, replayed.ProofHash)
		require.Equal(t, 1, repository.writeCount())
	})

	t.Run("prevents global reservation reuse and changed command replay", func(t *testing.T) {
		t.Parallel()

		authority, command := goldenExactPlanFixture(t)
		repository := newGoldenExactPlanRepository(authority)
		_, changed, err := arena.NewGoldenExactPlanUseCase(repository).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)

		competing := changedGoldenPlanCommand(command, 8000)
		plan, changed, err := arena.NewGoldenExactPlanUseCase(repository).PlanAndCommit(t.Context(), competing)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrGoldenExactPlanConflict)

		reused := command
		reused.CreatedAt = command.CreatedAt.Add(time.Second)
		plan, changed, err = arena.NewGoldenExactPlanUseCase(repository).PlanAndCommit(t.Context(), reused)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrGoldenExactPlanConflict)
		require.Equal(t, 1, repository.writeCount())
	})

	t.Run("rejects every repeated or aliased command identity", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(arena.GoldenExactPlanAuthority, *arena.GoldenExactPlanCommand)
		}{
			{name: "edge within group", mutate: func(_ arena.GoldenExactPlanAuthority, command *arena.GoldenExactPlanCommand) {
				command.GroupCommands[0].EdgeIDs[1] = command.GroupCommands[0].EdgeIDs[0]
			}},
			{name: "edge between groups", mutate: func(_ arena.GoldenExactPlanAuthority, command *arena.GoldenExactPlanCommand) {
				command.GroupCommands[1].EdgeIDs[0] = command.GroupCommands[0].EdgeIDs[0]
			}},
			{name: "reservation within group", mutate: func(_ arena.GoldenExactPlanAuthority, command *arena.GoldenExactPlanCommand) {
				command.GroupCommands[0].ReservationIDs[1] = command.GroupCommands[0].ReservationIDs[0]
			}},
			{name: "reservation between groups", mutate: func(_ arena.GoldenExactPlanAuthority, command *arena.GoldenExactPlanCommand) {
				command.GroupCommands[1].ReservationIDs[0] = command.GroupCommands[0].ReservationIDs[0]
			}},
			{name: "snapshot within group", mutate: func(_ arena.GoldenExactPlanAuthority, command *arena.GoldenExactPlanCommand) {
				command.GroupCommands[0].SnapshotIDs[1] = command.GroupCommands[0].SnapshotIDs[0]
			}},
			{name: "snapshot between groups", mutate: func(_ arena.GoldenExactPlanAuthority, command *arena.GoldenExactPlanCommand) {
				command.GroupCommands[1].SnapshotIDs[0] = command.GroupCommands[0].SnapshotIDs[0]
			}},
			{name: "edge aliases plan", mutate: func(_ arena.GoldenExactPlanAuthority, command *arena.GoldenExactPlanCommand) {
				command.GroupCommands[0].EdgeIDs[0] = command.PlanID
			}},
			{name: "reservation aliases plan revision", mutate: func(_ arena.GoldenExactPlanAuthority, command *arena.GoldenExactPlanCommand) {
				command.GroupCommands[0].ReservationIDs[0] = command.PlanRevisionID
			}},
			{name: "snapshot aliases candidate task", mutate: func(authority arena.GoldenExactPlanAuthority, command *arena.GoldenExactPlanCommand) {
				command.GroupCommands[0].SnapshotIDs[0] = authority.Candidates[0].Task.ID
			}},
			{name: "edge aliases another group reservation", mutate: func(_ arena.GoldenExactPlanAuthority, command *arena.GoldenExactPlanCommand) {
				command.GroupCommands[0].EdgeIDs[0] = command.GroupCommands[1].ReservationIDs[0]
			}},
			{name: "group revision aliases source revision", mutate: func(_ arena.GoldenExactPlanAuthority, command *arena.GoldenExactPlanCommand) {
				command.GroupCommands[0].GroupRevisionID = command.Expected.Revisions.SourceProjectionRevisionID
			}},
			{name: "duplicate group identity", mutate: func(_ arena.GoldenExactPlanAuthority, command *arena.GoldenExactPlanCommand) {
				command.GroupCommands[1].GroupID = command.GroupCommands[0].GroupID
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				authority, command := goldenExactPlanFixture(t)
				command.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands)
				test.mutate(authority, &command)
				repository := newGoldenExactPlanRepository(authority)
				plan, changed, err := arena.NewGoldenExactPlanUseCase(repository).PlanAndCommit(t.Context(), command)
				require.Nil(t, plan)
				require.False(t, changed)
				require.ErrorIs(t, err, arena.ErrInvalidGoldenExactPlan)
				require.Equal(t, 0, repository.commitCount())
			})
		}

		authority, command := goldenExactPlanFixture(t)
		authority.ExistingTaskReservations = []arena.GoldenTaskReservation{{
			TaskVersion: authority.Pool.Versions[0], ReservationID: task046ID(9600),
			PlanID: task046ID(9601), PlanRevisionID: task046ID(9602),
		}}
		authority, err := arena.BuildGoldenExactPlanAuthority(authority)
		require.NoError(t, err)
		command.Expected = authority.Expectation()
		command.PlanID = authority.ExistingTaskReservations[0].PlanID
		plan, changed, err := arena.NewGoldenExactPlanUseCase(newGoldenExactPlanRepository(authority)).
			PlanAndCommit(t.Context(), command)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrInvalidGoldenExactPlan)
	})

	t.Run("fails closed on nil malformed or mismatched committed records", func(t *testing.T) {
		t.Parallel()

		for _, mode := range []goldenRepositoryReturnMode{
			goldenRepositoryReturnNil,
			goldenRepositoryReturnMalformed,
			goldenRepositoryReturnMismatch,
		} {
			t.Run(string(mode), func(t *testing.T) {
				authority, command := goldenExactPlanFixture(t)
				repository := newGoldenExactPlanRepository(authority)
				repository.returnMode = mode
				plan, changed, err := arena.NewGoldenExactPlanUseCase(repository).
					PlanAndCommit(t.Context(), command)
				require.Nil(t, plan)
				require.False(t, changed)
				if mode == goldenRepositoryReturnMismatch {
					require.ErrorIs(t, err, arena.ErrGoldenExactPlanConflict)
				} else {
					require.ErrorIs(t, err, domain.ErrInternal)
				}
			})
		}
	})

	t.Run("defensively copies caller authority and returned records", func(t *testing.T) {
		t.Parallel()

		authority, command := goldenExactPlanFixture(t)
		repository := newGoldenExactPlanRepository(authority)
		repository.returnAliases = true
		repository.returnAuthorityAliases = true
		plan, _, err := arena.NewGoldenExactPlanUseCase(repository).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		wantProof := plan.ProofHash
		wantTask := plan.Groups[0].Edges[0].Snapshot.TaskID

		authority.Groups[0].ActiveParticipantIDs[0] = task046ID(9991)
		authority.Candidates[0].Task.Hints[0] = "caller mutation"
		repository.mu.Lock()
		repository.authorities[0].Groups[0].ActiveParticipantIDs[0] = task046ID(9994)
		repository.authorities[0].Candidates[0].Task.Hints[0] = "repository mutation"
		repository.mu.Unlock()
		plan.Authority.History = append(plan.Authority.History, arena.ArenaTaskReceiptRef{})
		plan.Groups[0].Edges[0].Snapshot.TaskID = task046ID(9992)
		plan.Groups[0].Edges[0].Snapshot.Hints[0] = "result mutation"

		replayed, changed, err := arena.NewGoldenExactPlanUseCase(repository).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, wantProof, replayed.ProofHash)
		require.Equal(t, wantTask, replayed.Groups[0].Edges[0].Snapshot.TaskID)
		require.NoError(t, replayed.Validate())
	})

	t.Run("rejects a normal pool for Golden planning", func(t *testing.T) {
		t.Parallel()

		authority, _ := goldenExactPlanFixture(t)
		authority.Pool.Kind = domain.ArenaTaskKindNormal
		_, err := arena.BuildGoldenExactPlanAuthority(authority)
		require.ErrorIs(t, err, arena.ErrInvalidGoldenExactPlan)
	})

	t.Run("rejects repeated participant task history across versions", func(t *testing.T) {
		t.Parallel()

		authority, _ := goldenExactPlanFixture(t)
		repeated := authority.History[0]
		repeated.Version++
		authority.History = append(authority.History, repeated)
		_, err := arena.BuildGoldenExactPlanAuthority(authority)
		require.ErrorIs(t, err, arena.ErrInvalidGoldenExactPlan)
	})

	t.Run("validates the retained authority identity namespace", func(t *testing.T) {
		t.Parallel()

		t.Run("binds historical revision identities by lineage role", func(t *testing.T) {
			authority, _ := goldenExactPlanFixture(t)
			successor := goldenExactPlanSuccessorAuthority(t, authority)
			require.Len(t, successor.Groups, 2)
			require.NotNil(t, successor.Source.PreviousRevisionID)
			for _, group := range successor.Groups {
				require.Equal(t, successor.Source.PreviousRevisionID, group.Revision.SourceProjectionPreviousRevisionID())
			}
			_, err := arena.BuildGoldenExactPlanAuthority(successor)
			require.NoError(t, err)

			sourceAlias := successor.Snapshot()
			sourcePreviousID := sourceAlias.Source.PreviousRevisionID.UUID()
			sourceAlias.Revisions.PoolRevisionID = sourcePreviousID
			sourceAlias.Pool.ID = sourcePreviousID
			for index := range sourceAlias.Candidates {
				sourceAlias.Candidates[index].PoolRevisionID = sourcePreviousID
				sourceAlias.Candidates[index].Health.PoolRevisionID = sourcePreviousID
			}
			_, err = arena.BuildGoldenExactPlanAuthority(sourceAlias)
			require.ErrorIs(t, err, arena.ErrInvalidGoldenExactPlan)

			groupAlias := successor.Snapshot()
			groupPreviousID := groupAlias.Groups[0].Revision.PreviousRevisionID()
			require.NotNil(t, groupPreviousID)
			groupAlias.Revisions.HistoryRevisionID = groupPreviousID.UUID()
			_, err = arena.BuildGoldenExactPlanAuthority(groupAlias)
			require.ErrorIs(t, err, arena.ErrInvalidGoldenExactPlan)
		})

		t.Run("candidate task aliases tournament", func(t *testing.T) {
			authority, _ := goldenExactPlanFixture(t)
			oldTaskID := authority.Candidates[0].Task.ID
			newTaskID := authority.Scope.TournamentID
			authority.Candidates[0].Task.ID = newTaskID
			authority.Candidates[0].Health.TaskID = newTaskID
			authority.Candidates[0].ArtifactDigest = arena.GoldenTaskArtifactDigest(
				authority.Candidates[0].Task, authority.Candidates[0].Version,
			)
			for index := range authority.Pool.Versions {
				if authority.Pool.Versions[index].TaskID == oldTaskID {
					authority.Pool.Versions[index].TaskID = newTaskID
				}
			}
			_, err := arena.BuildGoldenExactPlanAuthority(authority)
			require.ErrorIs(t, err, arena.ErrInvalidGoldenExactPlan)
		})

		t.Run("reservation owner aliases pool revision", func(t *testing.T) {
			authority, _ := goldenExactPlanFixture(t)
			authority.ExistingTaskReservations = []arena.GoldenTaskReservation{{
				TaskVersion: authority.Pool.Versions[0], ReservationID: task046ID(9700),
				PlanID: authority.Revisions.PoolRevisionID, PlanRevisionID: task046ID(9701),
			}}
			_, err := arena.BuildGoldenExactPlanAuthority(authority)
			require.ErrorIs(t, err, arena.ErrInvalidGoldenExactPlan)
		})

		t.Run("allows one exact owner pair across two reserved tasks", func(t *testing.T) {
			authority, _ := goldenExactPlanFixture(t)
			authority.ExistingTaskReservations = []arena.GoldenTaskReservation{
				{TaskVersion: authority.Pool.Versions[0], ReservationID: task046ID(9710), PlanID: task046ID(9712), PlanRevisionID: task046ID(9713)},
				{TaskVersion: authority.Pool.Versions[1], ReservationID: task046ID(9711), PlanID: task046ID(9712), PlanRevisionID: task046ID(9713)},
			}
			built, err := arena.BuildGoldenExactPlanAuthority(authority)
			require.NoError(t, err)
			require.Len(t, built.ExistingTaskReservations, 2)
			require.Equal(t, built.ExistingTaskReservations[0].PlanID, built.ExistingTaskReservations[1].PlanID)
			require.Equal(t, built.ExistingTaskReservations[0].PlanRevisionID, built.ExistingTaskReservations[1].PlanRevisionID)
		})
	})
}

type goldenPlanCallResult struct {
	plan    *arena.GoldenExactPlan
	changed bool
	err     error
}

type goldenRepositoryReturnMode string

const (
	goldenRepositoryReturnNormal    goldenRepositoryReturnMode = "normal"
	goldenRepositoryReturnNil       goldenRepositoryReturnMode = "nil"
	goldenRepositoryReturnMalformed goldenRepositoryReturnMode = "malformed"
	goldenRepositoryReturnMismatch  goldenRepositoryReturnMode = "mismatch"
)

type goldenExactPlanRepositoryFake struct {
	mu                     sync.Mutex
	authorities            []arena.GoldenExactPlanAuthority
	loads                  int
	commits                int
	writes                 int
	conflicts              int
	stored                 *arena.GoldenExactPlan
	reserved               map[arena.TaskVersionRef]uuid.UUID
	attemptedProofs        []string
	returnMode             goldenRepositoryReturnMode
	returnAliases          bool
	returnAuthorityAliases bool
	onConflict             func()
}

func newGoldenExactPlanRepository(authority arena.GoldenExactPlanAuthority) *goldenExactPlanRepositoryFake {
	return &goldenExactPlanRepositoryFake{
		authorities: []arena.GoldenExactPlanAuthority{authority.Snapshot()},
		reserved:    make(map[arena.TaskVersionRef]uuid.UUID),
		returnMode:  goldenRepositoryReturnNormal,
	}
}

func (r *goldenExactPlanRepositoryFake) LoadGoldenExactPlanAuthority(
	_ context.Context,
	_ arena.GoldenExactPlanScope,
) (arena.GoldenExactPlanAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := min(r.loads, len(r.authorities)-1)
	r.loads++
	if r.returnAuthorityAliases {
		return r.authorities[index], nil
	}
	return r.authorities[index].Snapshot(), nil
}

func (r *goldenExactPlanRepositoryFake) GetGoldenExactPlan(
	_ context.Context,
	scope arena.GoldenExactPlanScope,
	planID uuid.UUID,
) (*arena.GoldenExactPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stored == nil || r.stored.Scope != scope || r.stored.PlanID != planID {
		return nil, nil
	}
	if r.returnAliases {
		return r.stored, nil
	}
	stored := r.stored.Snapshot()
	return &stored, nil
}

func (r *goldenExactPlanRepositoryFake) CommitGoldenExactPlan(
	_ context.Context,
	plan arena.GoldenExactPlan,
) (*arena.GoldenExactPlan, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits++
	r.attemptedProofs = append(r.attemptedProofs, plan.ProofHash)
	if plan.Validate() != nil || plan.Expected != r.authorities[len(r.authorities)-1].Expectation() {
		return nil, false, domain.ErrConflict
	}
	if r.conflicts > 0 {
		r.conflicts--
		if r.onConflict != nil {
			r.onConflict()
			r.onConflict = nil
		}
		return nil, false, domain.ErrConflict
	}
	if r.stored != nil {
		if r.stored.PlanID == plan.PlanID {
			stored := r.stored.Snapshot()
			return &stored, false, nil
		}
		return nil, false, domain.ErrConflict
	}
	for _, group := range plan.Groups {
		for _, edge := range group.Edges {
			ref := arena.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
			if _, exists := r.reserved[ref]; exists {
				return nil, false, domain.ErrConflict
			}
		}
	}
	stored := plan.Snapshot()
	r.stored = &stored
	r.writes++
	for _, group := range stored.Groups {
		for _, edge := range group.Edges {
			ref := arena.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
			r.reserved[ref] = edge.ReservationID
		}
	}
	switch r.returnMode {
	case goldenRepositoryReturnNormal:
		if r.returnAliases {
			return r.stored, true, nil
		}
		result := stored.Snapshot()
		return &result, true, nil
	case goldenRepositoryReturnNil:
		return nil, true, nil
	case goldenRepositoryReturnMalformed:
		malformed := stored.Snapshot()
		malformed.ProofHash = "bad"
		return &malformed, true, nil
	case goldenRepositoryReturnMismatch:
		mismatch := stored.Snapshot()
		mismatch.PlanID = task046ID(9993)
		return &mismatch, false, nil
	}
	return nil, false, domain.ErrInternal
}

func (r *goldenExactPlanRepositoryFake) loadCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loads
}

func (r *goldenExactPlanRepositoryFake) commitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.commits
}

func (r *goldenExactPlanRepositoryFake) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func goldenExactPlanFixture(t *testing.T) (arena.GoldenExactPlanAuthority, arena.GoldenExactPlanCommand) {
	t.Helper()
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tournamentID := task046ID(400)
	source := mustGoldenStandingsProjection(
		t, tournamentID, task046ID(401), task046RevisionID(402), 1,
		goldenStandings([]int{10, 10, 9, 9, 9, 7}),
	)
	partition, err := arena.PartitionGoldenTies(source)
	require.NoError(t, err)
	seeds := partition.GoldenGroups()
	groups := make([]arena.GoldenPlanGroupAuthority, len(seeds))
	for index, seed := range seeds {
		command := arena.GoldenGroupRevisionCommand{
			TournamentID: tournamentID, GroupID: task046ID(410 + index),
			RevisionID: task046RevisionID(420 + index), RevisionNo: 1,
			ExpectedSourceRevisionID:    source.RevisionID,
			ExpectedSourcePayloadDigest: source.PayloadDigest,
			PositionFrom:                seed.PositionFrom, PositionTo: seed.PositionTo,
		}
		revision, buildErr := arena.BuildGoldenGroupRevision(command, source, seed, nil, nil)
		require.NoError(t, buildErr)
		members := revision.Members()
		active := make([]uuid.UUID, len(members))
		for memberIndex, member := range members {
			active[memberIndex] = member.ParticipantID
		}
		groups[index] = arena.GoldenPlanGroupAuthority{Revision: revision, ActiveParticipantIDs: active}
	}

	poolID := task046ID(430)
	candidates := make([]arena.GoldenExactTaskVersion, 6)
	versions := make([]arena.TaskVersionRef, len(candidates))
	for index := range candidates {
		task := goldenTask(500 + index)
		version := 2
		versions[index] = arena.TaskVersionRef{TaskID: task.ID, Version: version}
		candidates[index] = arena.GoldenExactTaskVersion{
			PoolRevisionID: poolID, Version: version, Task: task,
			Health: arena.TaskVersionHealth{
				TaskID: task.ID, Version: version, PoolRevisionID: poolID,
				PoolKind: domain.ArenaTaskKindGolden, Exists: true, Enabled: true,
				Healthy: true, MutationLocked: true,
			},
			ArtifactDigest: arena.GoldenTaskArtifactDigest(task, version),
		}
	}

	participants := make([]arena.GoldenParticipantReservation, 0, 5)
	for _, group := range groups {
		for _, participantID := range group.ActiveParticipantIDs {
			participants = append(participants, arena.GoldenParticipantReservation{
				ParticipantID: participantID, PlayerID: task046ID(1000 + len(participants)),
				Reservation: domain.ParticipantReservation{
					PlayerID: task046ID(1000 + len(participants)), ReservationID: task046ID(1100 + len(participants)),
					OwnerKind: domain.ParticipantReservationOwnerArena, OwnerID: tournamentID,
					Revision: 1, AcquiredAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
				},
			})
		}
	}

	history := make([]arena.ArenaTaskReceiptRef, 0, 3)
	for index := 3; index < 6; index++ {
		history = append(history, arena.ArenaTaskReceiptRef{
			ParticipantID: groups[1].ActiveParticipantIDs[0],
			TaskID:        versions[index].TaskID, Version: 1,
		})
	}
	authority, err := arena.BuildGoldenExactPlanAuthority(arena.GoldenExactPlanAuthority{
		Scope: arena.GoldenExactPlanScope{TournamentID: tournamentID, PlanSetID: task046ID(440)},
		Revisions: arena.GoldenExactPlanRevisions{
			SourceProjectionRevisionID: source.RevisionID,
			GroupSetRevisionID:         task046ID(441), GroupSetRevision: 1,
			PoolRevisionID: poolID, PoolRevision: 1,
			HistoryRevisionID: task046ID(442), HistoryRevision: 1,
			TaskHealthRevisionID: task046ID(443), TaskHealthRevision: 1,
			ArtifactRevisionID: task046ID(444), ArtifactRevision: 1,
			ReservationRevisionID: task046ID(445), ReservationRevision: 1,
			MembershipRevisionID: task046ID(446), MembershipRevision: 1,
		},
		Source: source, Groups: groups,
		Pool:    arena.TaskPoolRevision{ID: poolID, Revision: 1, Kind: domain.ArenaTaskKindGolden, Versions: versions},
		History: history, Candidates: candidates,
		ParticipantReservations: participants,
	})
	require.NoError(t, err)

	groupCommands := make([]arena.GoldenExactGroupCommand, len(groups))
	for groupIndex, group := range groups {
		groupCommands[groupIndex].GroupID = group.Revision.GroupID()
		groupCommands[groupIndex].GroupRevisionID = group.Revision.RevisionID()
		for edgeIndex := range groupCommands[groupIndex].EdgeIDs {
			base := 1200 + groupIndex*100 + edgeIndex*10
			groupCommands[groupIndex].EdgeIDs[edgeIndex] = task046ID(base + 1)
			groupCommands[groupIndex].ReservationIDs[edgeIndex] = task046ID(base + 2)
			groupCommands[groupIndex].SnapshotIDs[edgeIndex] = task046ID(base + 3)
		}
	}
	command := arena.GoldenExactPlanCommand{
		Scope: authority.Scope, PlanID: task046ID(700), PlanRevisionID: task046ID(701),
		Expected: authority.Expectation(), GroupCommands: groupCommands, CreatedAt: now,
	}
	return authority, command
}

func goldenExactPlanSuccessorAuthority(
	t *testing.T,
	current arena.GoldenExactPlanAuthority,
) arena.GoldenExactPlanAuthority {
	t.Helper()
	standings := cloneSwissNormalStandings(current.Source.Standings)
	standings[len(standings)-1].Buchholz++
	previousSourceID := current.Source.RevisionID
	source, err := arena.NewGoldenStandingsProjection(
		current.Source.TournamentID,
		current.Source.ProjectionID,
		task046RevisionID(9800),
		current.Source.RevisionNo+1,
		&previousSourceID,
		true,
		standings,
	)
	require.NoError(t, err)
	partition, err := arena.PartitionGoldenTies(source)
	require.NoError(t, err)
	seeds := partition.GoldenGroups()
	require.Len(t, seeds, len(current.Groups))

	groups := make([]arena.GoldenPlanGroupAuthority, len(current.Groups))
	for index, group := range current.Groups {
		previousGroupID := group.Revision.RevisionID()
		command := arena.GoldenGroupRevisionCommand{
			TournamentID:                current.Scope.TournamentID,
			GroupID:                     group.Revision.GroupID(),
			RevisionID:                  task046RevisionID(9810 + index),
			RevisionNo:                  group.Revision.RevisionNo() + 1,
			PreviousRevisionID:          &previousGroupID,
			ExpectedSourceRevisionID:    source.RevisionID,
			ExpectedSourcePayloadDigest: source.PayloadDigest,
			PositionFrom:                seeds[index].PositionFrom,
			PositionTo:                  seeds[index].PositionTo,
		}
		revision, buildErr := arena.BuildGoldenGroupRevision(command, source, seeds[index], &group.Revision, nil)
		require.NoError(t, buildErr)
		groups[index] = arena.GoldenPlanGroupAuthority{
			Revision:             revision,
			ActiveParticipantIDs: append([]uuid.UUID(nil), group.ActiveParticipantIDs...),
		}
	}

	successor := current.Snapshot()
	successor.Source = source
	successor.Groups = groups
	successor.Revisions.SourceProjectionRevisionID = source.RevisionID
	successor.Revisions.GroupSetRevisionID = task046ID(9820)
	successor.Revisions.GroupSetRevision++
	built, err := arena.BuildGoldenExactPlanAuthority(successor)
	require.NoError(t, err)
	return built
}

func goldenTask(value int) domain.Task {
	httpsURL := "https://tasks.example.test/golden"
	return domain.Task{
		ID: task046ID(value), Title: "Golden challenge", Description: "Solve the Golden challenge.",
		Category: domain.CategoryWeb, Difficulty: domain.DifficultyMedium,
		TimeLimit: 300, Flag: "FLAG{golden}", Hints: []string{"Inspect the request."},
		TaskURL: &httpsURL,
	}
}

func goldenTaskNumber(id uuid.UUID) int {
	value := 0
	for _, character := range id.String()[24:] {
		value = value*10 + int(character-'0')
	}
	return value
}

func cloneGoldenGroupCommands(input []arena.GoldenExactGroupCommand) []arena.GoldenExactGroupCommand {
	return append([]arena.GoldenExactGroupCommand(nil), input...)
}

func changedGoldenPlanCommand(command arena.GoldenExactPlanCommand, base int) arena.GoldenExactPlanCommand {
	changed := command
	changed.PlanID = task046ID(base)
	changed.PlanRevisionID = task046ID(base + 1)
	changed.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands)
	for groupIndex := range changed.GroupCommands {
		for edgeIndex := range changed.GroupCommands[groupIndex].EdgeIDs {
			value := base + 10 + groupIndex*20 + edgeIndex*3
			changed.GroupCommands[groupIndex].EdgeIDs[edgeIndex] = task046ID(value)
			changed.GroupCommands[groupIndex].ReservationIDs[edgeIndex] = task046ID(value + 1)
			changed.GroupCommands[groupIndex].SnapshotIDs[edgeIndex] = task046ID(value + 2)
		}
	}
	return changed
}

var _ arena.GoldenExactPlanRepository = (*goldenExactPlanRepositoryFake)(nil)
