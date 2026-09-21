package plan_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
)

func TestGoldenExactPlan(t *testing.T) {
	t.Parallel()

	t.Run("matches every multi-party group with one primary and two reserves", func(t *testing.T) {
		t.Parallel()

		authority, command := planGoldenPlanExact(t)
		wantTaskDuration := int(domain.TournamentTaskDuration / time.Second)
		require.NotEqual(t, wantTaskDuration, authority.Candidates[0].Task.TimeLimit)
		repository := newGoldenExactPlanRepository(t, authority)
		plan, changed, err := goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, plan.Validate())
		require.Len(t, plan.Groups, 2)
		require.Len(t, plan.Groups[0].ParticipantIDs, 2)
		require.Len(t, plan.Groups[1].ParticipantIDs, 3)

		selected := make(map[domain.TaskVersionRef]uuid.UUID)
		for _, group := range plan.Groups {
			require.Len(t, group.Edges, domain.AssignmentReserveCount+1)
			for index, edge := range group.Edges {
				require.Equal(t, index+1, edge.Position)
				require.Equal(t, domain.AssignmentTaskKindGolden, edge.Snapshot.Kind)
				require.Equal(t, wantTaskDuration, edge.Snapshot.TimeLimit)
				ref := domain.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
				require.NotContains(t, selected, ref)
				selected[ref] = group.GroupID
			}
		}
		require.Len(t, selected, 6)
		for _, edge := range plan.Groups[1].Edges {
			require.Less(t, goldenTaskNumber(edge.Snapshot.TaskID), 503, "matching must reassign scarce tasks to the second group")
		}
		require.Equal(t, 1, repository.state.writeCount())
	})

	t.Run("supports a primary-only chain without materializing empty reserves", func(t *testing.T) {
		t.Parallel()

		authority, command := planGoldenPlanExact(t)
		for index := range command.GroupCommands {
			for slot := 1; slot < domain.AssignmentReserveCount+1; slot++ {
				command.GroupCommands[index].EdgeIDs[slot] = uuid.Nil
				command.GroupCommands[index].ReservationIDs[slot] = uuid.Nil
				command.GroupCommands[index].SnapshotIDs[slot] = uuid.Nil
			}
		}
		plan, changed, err := goldenusecase.NewUseCase(newGoldenExactPlanRepository(t, authority).mock).
			PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, plan.Validate())
		for _, group := range plan.Groups {
			require.Len(t, group.Edges, 1)
			require.NotEqual(t, uuid.Nil, group.Edges[0].ID)
			require.NotEqual(t, uuid.Nil, group.Edges[0].ReservationID)
			require.NotEqual(t, uuid.Nil, group.Edges[0].Snapshot.SnapshotID)
		}
	})

	t.Run("selects only eligible Golden content when capacity remains", func(t *testing.T) {
		t.Parallel()

		authority, command := planGoldenPlanExact(t)
		ineligible := authority.Candidates[0]
		ineligibleRef := domain.TaskVersionRef{TaskID: ineligible.Task.ID, Version: ineligible.Version}
		authority.Candidates[0].Health.Enabled = false

		extraTask := planGoldenPlanTask(600)
		extraTask.TimeLimit = 240
		extraCandidate := goldenusecase.TaskVersion{
			PoolRevisionID: authority.Pool.ID,
			Version:        2,
			Task:           extraTask,
			Health: domain.TaskVersionHealth{
				TaskID: extraTask.ID, Version: 2, PoolRevisionID: authority.Pool.ID,
				PoolKind: domain.AssignmentTaskKindGolden, Exists: true, Enabled: true,
				Healthy: true, MutationLocked: true,
			},
			ArtifactDigest: goldenusecase.TaskArtifactDigest(extraTask, 2),
		}
		authority.Pool.Versions = append(authority.Pool.Versions,
			domain.TaskVersionRef{TaskID: extraTask.ID, Version: extraCandidate.Version})
		authority.Candidates = append(authority.Candidates, extraCandidate)
		authority, err := goldenusecase.BuildAuthority(authority)
		require.NoError(t, err)
		command.Expected = authority.Expectation()
		repository := newGoldenExactPlanRepository(t, authority)
		plan, changed, err := goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, plan.Validate())

		eligible := make(map[domain.TaskVersionRef]struct{}, len(authority.Candidates))
		for _, candidate := range authority.Candidates {
			if candidate.Health.Exists && candidate.Health.Enabled && candidate.Health.Healthy &&
				candidate.Health.MutationLocked && !candidate.Health.PubliclyExposed {
				eligible[domain.TaskVersionRef{TaskID: candidate.Task.ID, Version: candidate.Version}] = struct{}{}
			}
		}
		selected := make(map[domain.TaskVersionRef]struct{})
		for _, group := range plan.Groups {
			for _, edge := range group.Edges {
				ref := domain.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}
				_, isEligible := eligible[ref]
				require.True(t, isEligible, "selected content must be eligible: %v", ref)
				require.NotEqual(t, ineligibleRef, ref)
				selected[ref] = struct{}{}
				require.Equal(t, int(domain.TournamentTaskDuration/time.Second), edge.Snapshot.TimeLimit)
			}
		}
		require.Len(t, selected, 6)
		require.Equal(t, 1, repository.state.writeCount())
	})

	t.Run("rejects a tampered Golden snapshot duration with a matching digest", func(t *testing.T) {
		t.Parallel()

		authority, command := planGoldenPlanExact(t)
		repository := newGoldenExactPlanRepository(t, authority)
		plan, changed, err := goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		wantTaskDuration := int(domain.TournamentTaskDuration / time.Second)
		require.Equal(t, wantTaskDuration, plan.Groups[0].Edges[0].Snapshot.TimeLimit)

		tampered := plan.Snapshot()
		tampered.Groups[0].Edges[0].Snapshot.TimeLimit = wantTaskDuration + 1
		require.NoError(t, tampered.Groups[0].Edges[0].Snapshot.Validate())
		digest, err := taskexec.SnapshotDigest(tampered.Groups[0].Edges[0].Snapshot)
		require.NoError(t, err)
		tampered.Groups[0].Edges[0].ContentDigest = digest
		require.ErrorIs(t, tampered.Validate(), goldenusecase.ErrInvalidExactPlan)
	})

	t.Run("plans the authoritative non-empty subset of active topology groups", func(t *testing.T) {
		t.Parallel()

		authority, command := planGoldenPlanExact(t)
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
		authority, err := goldenusecase.BuildAuthority(authority)
		require.NoError(t, err)
		command.Expected = authority.Expectation()
		command.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands[:1])
		plan, changed, err := goldenusecase.NewUseCase(newGoldenExactPlanRepository(t, authority).mock).
			PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Len(t, plan.Groups, 1)
		require.Equal(t, authority.Groups[0].Revision.GroupID(), plan.Groups[0].GroupID)
		require.Len(t, plan.Groups[0].Edges, domain.AssignmentReserveCount+1)
	})

	t.Run("excludes a TaskID used at another version", func(t *testing.T) {
		t.Parallel()

		authority, command := planGoldenPlanExact(t)
		usedTaskID := authority.Pool.Versions[0].TaskID
		authority.History = append(authority.History, assignmentusecase.TaskReceiptRef{
			ParticipantID: authority.Groups[0].ActiveParticipantIDs[0],
			TaskID:        usedTaskID, Version: authority.Pool.Versions[0].Version - 1,
		})
		authority, err := goldenusecase.BuildAuthority(authority)
		require.NoError(t, err)
		command.Expected = authority.Expectation()
		repository := newGoldenExactPlanRepository(t, authority)
		plan, _, err := goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), command)
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

		authority, command := planGoldenPlanExact(t)
		staleCases := []struct {
			name   string
			mutate func(*goldenusecase.Expectation)
		}{
			{name: "source revision", mutate: func(value *goldenusecase.Expectation) {
				value.Revisions.SourceProjectionRevisionID = planGoldenPlanRevisionID(9001)
			}},
			{name: "group revision id", mutate: func(value *goldenusecase.Expectation) { value.Revisions.GroupSetRevisionID = planGoldenPlanID(9002) }},
			{name: "group revision", mutate: func(value *goldenusecase.Expectation) { value.Revisions.GroupSetRevision++ }},
			{name: "pool revision id", mutate: func(value *goldenusecase.Expectation) { value.Revisions.PoolRevisionID = planGoldenPlanID(9003) }},
			{name: "pool revision", mutate: func(value *goldenusecase.Expectation) { value.Revisions.PoolRevision++ }},
			{name: "history revision id", mutate: func(value *goldenusecase.Expectation) { value.Revisions.HistoryRevisionID = planGoldenPlanID(9004) }},
			{name: "history revision", mutate: func(value *goldenusecase.Expectation) { value.Revisions.HistoryRevision++ }},
			{name: "health revision id", mutate: func(value *goldenusecase.Expectation) {
				value.Revisions.TaskHealthRevisionID = planGoldenPlanID(9005)
			}},
			{name: "health revision", mutate: func(value *goldenusecase.Expectation) { value.Revisions.TaskHealthRevision++ }},
			{name: "artifact revision id", mutate: func(value *goldenusecase.Expectation) { value.Revisions.ArtifactRevisionID = planGoldenPlanID(9006) }},
			{name: "artifact revision", mutate: func(value *goldenusecase.Expectation) { value.Revisions.ArtifactRevision++ }},
			{name: "reservation revision id", mutate: func(value *goldenusecase.Expectation) {
				value.Revisions.ReservationRevisionID = planGoldenPlanID(9007)
			}},
			{name: "reservation revision", mutate: func(value *goldenusecase.Expectation) { value.Revisions.ReservationRevision++ }},
			{name: "membership revision id", mutate: func(value *goldenusecase.Expectation) {
				value.Revisions.MembershipRevisionID = planGoldenPlanID(9008)
			}},
			{name: "membership revision", mutate: func(value *goldenusecase.Expectation) { value.Revisions.MembershipRevision++ }},
			{name: "source digest", mutate: func(value *goldenusecase.Expectation) {
				value.SourcePayloadDigest = sha256.Sum256([]byte("stale source"))
			}},
			{name: "group digest", mutate: func(value *goldenusecase.Expectation) {
				value.GroupDigest = sha256.Sum256([]byte("stale group"))
			}},
			{name: "pool digest", mutate: func(value *goldenusecase.Expectation) { value.PoolDigest = sha256.Sum256([]byte("stale pool")) }},
			{name: "history digest", mutate: func(value *goldenusecase.Expectation) {
				value.HistoryDigest = sha256.Sum256([]byte("stale history"))
			}},
			{name: "health digest", mutate: func(value *goldenusecase.Expectation) {
				value.TaskHealthDigest = sha256.Sum256([]byte("stale health"))
			}},
			{name: "artifact digest", mutate: func(value *goldenusecase.Expectation) {
				value.ArtifactDigest = sha256.Sum256([]byte("stale artifact"))
			}},
			{name: "reservation digest", mutate: func(value *goldenusecase.Expectation) {
				value.ReservationDigest = sha256.Sum256([]byte("stale reservation"))
			}},
			{name: "membership digest", mutate: func(value *goldenusecase.Expectation) {
				value.MembershipDigest = sha256.Sum256([]byte("stale membership"))
			}},
		}
		for _, test := range staleCases {
			t.Run(test.name, func(t *testing.T) {
				stale := command
				stale.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands)
				test.mutate(&stale.Expected)
				repository := newGoldenExactPlanRepository(t, authority)
				plan, changed, err := goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), stale)
				require.Nil(t, plan)
				require.False(t, changed)
				require.ErrorIs(t, err, goldenusecase.ErrExactPlanStale)
				require.Equal(t, 0, repository.state.commitCount())
			})
		}

		short := authority.Snapshot()
		short.Candidates = short.Candidates[:5]
		short.Pool.Versions = short.Pool.Versions[:5]
		short, err := goldenusecase.BuildAuthority(short)
		require.NoError(t, err)
		shortCommand := command
		shortCommand.Expected = short.Expectation()
		shortCommand.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands)
		shortRepository := newGoldenExactPlanRepository(t, short)
		plan, changed, err := goldenusecase.NewUseCase(shortRepository.mock).
			PlanAndCommit(t.Context(), shortCommand)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, goldenusecase.ErrExactPlanInsufficient)
		require.Equal(t, 0, shortRepository.state.commitCount())

		reserved := authority.Snapshot()
		reserved.ExistingTaskReservations = []goldenusecase.TaskReservation{{
			TaskVersion: reserved.Pool.Versions[0], ReservationID: planGoldenPlanID(9300),
			PlanID: planGoldenPlanID(9301), PlanRevisionID: planGoldenPlanID(9302),
		}}
		reserved, err = goldenusecase.BuildAuthority(reserved)
		require.NoError(t, err)
		reservedCommand := command
		reservedCommand.Expected = reserved.Expectation()
		reservedCommand.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands)
		plan, changed, err = goldenusecase.NewUseCase(newGoldenExactPlanRepository(t, reserved).mock).
			PlanAndCommit(t.Context(), reservedCommand)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, goldenusecase.ErrExactPlanInsufficient)
	})

	t.Run("retries the exact deterministic plan then reports bounded conflict", func(t *testing.T) {
		t.Parallel()

		authority, command := planGoldenPlanExact(t)
		repository := newGoldenExactPlanRepository(t, authority)
		repository.state.conflicts = 1
		plan, changed, err := goldenusecase.NewUseCase(repository.mock).PlanAndCommit(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, 2, repository.state.loadCount())
		require.Equal(t, 2, repository.state.commitCount())
		require.Len(t, repository.state.attemptedProofs, 2)
		require.Equal(t, repository.state.attemptedProofs[0], repository.state.attemptedProofs[1])
		require.Equal(t, plan.ProofHash, repository.state.attemptedProofs[1])

		blocked := newGoldenExactPlanRepository(t, authority)
		blocked.state.conflicts = 2
		plan, changed, err = goldenusecase.NewUseCase(blocked.mock).PlanAndCommit(t.Context(), command)
		require.Nil(t, plan)
		require.False(t, changed)
		require.ErrorIs(t, err, goldenusecase.ErrExactPlanConflict)
		require.Equal(t, 2, blocked.state.commitCount())

		mutableCommand := command
		mutableCommand.GroupCommands = cloneGoldenGroupCommands(command.GroupCommands)
		frozen := newGoldenExactPlanRepository(t, authority)
		frozen.state.conflicts = 1
		frozen.state.onConflict = func() {
			mutableCommand.GroupCommands[0].EdgeIDs[0] = planGoldenPlanID(9499)
		}
		plan, changed, err = goldenusecase.NewUseCase(frozen.mock).PlanAndCommit(t.Context(), mutableCommand)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, command.GroupCommands[0].EdgeIDs[0], plan.Groups[0].Edges[0].ID)
		require.Equal(t, frozen.state.attemptedProofs[0], frozen.state.attemptedProofs[1])
	})

	t.Run("rejects unhealthy or mismatched candidate authority", func(t *testing.T) {
		t.Parallel()

		ineligible := []struct {
			name   string
			mutate func(*goldenusecase.TaskVersion)
		}{
			{name: "missing", mutate: func(value *goldenusecase.TaskVersion) { value.Health.Exists = false }},
			{name: "disabled", mutate: func(value *goldenusecase.TaskVersion) { value.Health.Enabled = false }},
			{name: "unhealthy", mutate: func(value *goldenusecase.TaskVersion) { value.Health.Healthy = false }},
			{name: "mutable", mutate: func(value *goldenusecase.TaskVersion) { value.Health.MutationLocked = false }},
			{name: "publicly exposed", mutate: func(value *goldenusecase.TaskVersion) { value.Health.PubliclyExposed = true }},
		}
		for _, test := range ineligible {
			t.Run(test.name, func(t *testing.T) {
				authority, command := planGoldenPlanExact(t)
				test.mutate(&authority.Candidates[0])
				authority, err := goldenusecase.BuildAuthority(authority)
				require.NoError(t, err)
				command.Expected = authority.Expectation()
				plan, changed, err := goldenusecase.NewUseCase(newGoldenExactPlanRepository(t, authority).mock).
					PlanAndCommit(t.Context(), command)
				require.Nil(t, plan)
				require.False(t, changed)
				require.ErrorIs(t, err, goldenusecase.ErrExactPlanInsufficient)
			})
		}

		invalid := []struct {
			name   string
			mutate func(*goldenusecase.TaskVersion)
		}{
			{name: "wrong pool", mutate: func(value *goldenusecase.TaskVersion) { value.Health.PoolKind = domain.AssignmentTaskKindNormal }},
			{name: "artifact changed", mutate: func(value *goldenusecase.TaskVersion) { value.ArtifactDigest = sha256.Sum256([]byte("changed")) }},
		}
		for _, test := range invalid {
			t.Run(test.name, func(t *testing.T) {
				authority, _ := planGoldenPlanExact(t)
				test.mutate(&authority.Candidates[0])
				_, err := goldenusecase.BuildAuthority(authority)
				require.ErrorIs(t, err, goldenusecase.ErrInvalidExactPlan)
			})
		}
	})
}
