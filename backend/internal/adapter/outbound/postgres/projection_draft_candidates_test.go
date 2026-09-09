package postgres

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	assignment "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestFinalDraftCandidatesMatchCanonicalEligibleSet(t *testing.T) {
	at := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	scope := assignment.ExactNormalAssignmentScope{TournamentID: uuid.New(), RosterID: uuid.New(), SeriesID: uuid.New(), SlotID: uuid.New(), CategoryLockID: uuid.New()}
	authority := assignment.ExactNormalAssignmentAuthority{Scope: scope, Category: domain.CategoryWeb,
		Pool:           domain.TaskPoolRevision{ID: uuid.New(), Revision: 1, Kind: domain.AssignmentTaskKindNormal},
		ParticipantIDs: []uuid.UUID{uuid.New(), uuid.New()}, GraphDigest: sha256.Sum256([]byte("graph")), ArtifactDigest: sha256.Sum256([]byte("artifact")),
	}
	authority.Revisions = assignment.ExactNormalAssignmentSourceRevisions{SeriesRevision: 1, PoolRevisionID: authority.Pool.ID, PoolRevision: 1, HistoryRevisionID: uuid.New(), HistoryRevision: 1, RosterRevision: 1, ArtifactRevisionID: uuid.New(), ArtifactRevision: 1, CategoryRevisionID: scope.CategoryLockID, CategoryRevision: 1}
	for _, id := range authority.ParticipantIDs {
		player := uuid.New()
		authority.ParticipantReservations = append(authority.ParticipantReservations, assignment.ExactNormalParticipantReservation{ParticipantID: id, PlayerID: player, Reservation: domain.ParticipantReservation{PlayerID: player, TournamentID: scope.TournamentID, ReservationID: uuid.New(), Revision: 1, AcquiredAt: at, UpdatedAt: at}})
	}
	for index := range 9 {
		category := domain.CategoryWeb
		if index == 8 {
			category = domain.CategoryCrypto
		}
		task := domain.Task{ID: uuid.New(), Title: "Task", Description: "Task description", Category: category, Difficulty: domain.DifficultyEasy, TimeLimit: 60, Flag: "fixture", Kind: domain.TaskKindNormal, Enabled: true, CurrentVersion: 1, CreatedAt: at}
		authority.Pool.Versions = append(authority.Pool.Versions, domain.TaskVersionRef{TaskID: task.ID, Version: 1})
		authority.Candidates = append(authority.Candidates, assignment.ExactNormalTaskVersion{PoolRevisionID: authority.Pool.ID, Version: 1, Task: task})
	}
	authority.History = []capacity.TaskUse{{ParticipantID: authority.ParticipantIDs[0], TaskID: authority.Candidates[0].Task.ID}}
	plan := assignment.ExactDraftBranchPlan{Branches: []assignment.ExactDraftBranch{{}}}
	unavailable := map[domain.TaskVersionRef]struct{}{}
	for range 2 {
		command := assignment.ExactNormalAssignmentCommand{Scope: scope, PlanID: uuid.New(), PlanRevisionID: uuid.New(), BranchID: uuid.New(), DecisionEvidenceID: uuid.New(), CreatedAt: at}
		for i := range command.EdgeIDs {
			command.EdgeIDs[i], command.ReservationIDs[i], command.SnapshotIDs[i] = uuid.New(), uuid.New(), uuid.New()
		}
		child, err := assignment.BuildExactNormalAssignmentExcluding(command, authority, unavailable)
		require.NoError(t, err)
		require.NoError(t, child.Validate())
		refs, err := assignment.RestoreExactNormalDecisionCandidates(child.Pool, child.DecisionEvidence)
		require.NoError(t, err)
		require.Equal(t, child.CandidateTaskVersions, refs)
		plan.Branches[0].Assignments = append(plan.Branches[0].Assignments, assignment.ExactDraftBranchAssignment{Plan: child})
		for _, edge := range child.SelectedEdges {
			unavailable[domain.TaskVersionRef{TaskID: edge.Snapshot.TaskID, Version: edge.Snapshot.Version}] = struct{}{}
		}
	}
	require.NoError(t, validateExactDraftPlanCandidates(plan, authority.Candidates), "canonical category/history/prior-edge exclusions must be accepted")
	for _, mutation := range []string{"missing", "extra", "duplicate", "version"} {
		candidates := append([]assignment.ExactNormalTaskVersion(nil), authority.Candidates...)
		switch mutation {
		case "missing":
			candidates = candidates[1:]
		case "extra":
			extra := candidates[0]
			extra.Task.ID = uuid.New()
			candidates = append(candidates, extra)
		case "duplicate":
			candidates[1] = candidates[0]
		case "version":
			candidates[0].Version++
		}
		require.False(t, exactDraftPlanHasCandidates(plan, candidates), mutation)
	}
}
