//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestArenaAssignmentRepositoryCommitsProofAndDeliversExactlyOnce(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	draft := createArenaDraftMigrationFixture(t, ctx)
	baseTime := draft.createdAt.Add(5 * time.Second)
	repository := postgres.NewArenaAssignmentPostgres(postgres.NewTxManager(sharedPool))
	poolRevisionID := uuid.New()
	conservativeID := uuid.New()
	conservative, err := repository.CreateConservativePlan(ctx, postgres.ArenaConservativePlanInput{
		ID: conservativeID, TournamentID: draft.tournamentID, RosterID: draft.rosterID,
		RevisionID: uuid.New(), SourceRosterRevision: 1, SourcePoolRevisionID: poolRevisionID,
		ConstraintGraph: map[string]any{"scope": "all-reachable-branches"},
		ProofEvidence:   map[string]any{"feasible": true}, CreatedAt: baseTime,
	})
	require.NoError(t, err)
	require.Equal(t, "conservative", conservative.Plan.Kind)
	require.Empty(t, conservative.Branches)

	exactID := uuid.New()
	decision, err := domain.NewArenaDecisionEvidence(
		uuid.New(), domain.ArenaDecisionPurposeTask, domain.ArenaDecisionAlgorithmV1,
		[]string{"branch-a", "branch-b"}, exactID, baseTime,
	)
	require.NoError(t, err)
	branches := []postgres.ArenaAssignmentBranchInput{
		arenaAssignmentRepositoryBranch(t, ctx, draft, "branch-a", domain.CategoryWeb),
		arenaAssignmentRepositoryBranch(t, ctx, draft, "branch-b", domain.CategoryCrypto),
	}
	exactRevisionID := uuid.New()
	exact, err := repository.CreateExactPlan(ctx, postgres.ArenaExactPlanInput{
		ID: exactID, TournamentID: draft.tournamentID, RosterID: draft.rosterID,
		ParentPlanID: conservativeID, RevisionID: exactRevisionID, SourceRosterRevision: 1,
		SourcePoolRevisionID: poolRevisionID, SourceDraftRevision: draft.initialRevisionID,
		ConstraintGraph: map[string]any{"source": "conservative-proof"},
		ProofEvidence:   map[string]any{"branches": 2}, DecisionEvidence: decision,
		Branches: branches, CreatedAt: baseTime.Add(time.Second),
	})
	require.NoError(t, err)
	require.Len(t, exact.Branches, 2)
	require.Len(t, exact.Edges, 6)
	require.Len(t, exact.Reservations, 6)
	require.Len(t, exact.Snapshots, 6)

	type commitResult struct {
		branchID uuid.UUID
		changed  bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan commitResult, len(branches))
	var workers sync.WaitGroup
	for _, branch := range branches {
		branch := branch
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, changed, commitErr := repository.CommitBranch(
				ctx, exactID, exactRevisionID, 1, draft.initialRevisionID,
				branch.ID, baseTime.Add(2*time.Second), "unselected draft branch",
			)
			results <- commitResult{branchID: branch.ID, changed: changed, err: commitErr}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	var activeBranchID uuid.UUID
	commitWinners := 0
	for item := range results {
		require.NoError(t, item.err)
		if item.changed {
			commitWinners++
			activeBranchID = item.branchID
		}
	}
	require.Equal(t, 1, commitWinners)
	require.NotEqual(t, uuid.Nil, activeBranchID)

	exact, err = repository.GetPlan(ctx, exactID)
	require.NoError(t, err)
	require.Equal(t, "committed", exact.Plan.State)
	require.Equal(t, activeBranchID, *exact.Plan.ActiveBranchID)
	require.Equal(t, 3, countArenaReservationState(exact.Reservations, "committed"))
	require.Equal(t, 3, countArenaReservationState(exact.Reservations, "released"))

	primaryReservation, primarySnapshot := arenaAssignmentRepositorySlot(t, exact, activeBranchID, 1)
	reserveReservation, reserveSnapshot := arenaAssignmentRepositorySlot(t, exact, activeBranchID, 2)
	finalReservation, finalSnapshot := arenaAssignmentRepositorySlot(t, exact, activeBranchID, 3)
	slotID := createArenaMigrationGameSlot(t, ctx, draft.seriesID, draft.rosterID, 1, "web")
	attemptID := createActiveArenaMigrationAttempt(t, ctx, slotID, draft.seriesID, draft.rosterID, baseTime)
	_, err = repository.CreateAssignment(ctx, postgres.ArenaAssignmentCreateInput{
		ID: uuid.New(), AttemptID: attemptID, SeriesID: draft.seriesID, RosterID: draft.rosterID,
		PlanID: exactID, BranchID: activeBranchID, ReservationID: reserveReservation.ID,
		SnapshotID: reserveSnapshot.Snapshot.SnapshotID, CreatedAt: baseTime.Add(3 * time.Second),
	})
	require.ErrorIs(t, err, domain.ErrConflict, "initial assignment must use the primary proof edge")

	assignmentID := uuid.New()
	assignment, err := repository.CreateAssignment(ctx, postgres.ArenaAssignmentCreateInput{
		ID: assignmentID, AttemptID: attemptID, SeriesID: draft.seriesID, RosterID: draft.rosterID,
		PlanID: exactID, BranchID: activeBranchID, ReservationID: primaryReservation.ID,
		SnapshotID: primarySnapshot.Snapshot.SnapshotID, CreatedAt: baseTime.Add(3 * time.Second),
	})
	require.NoError(t, err)
	require.Equal(t, "active", assignment.State)
	require.Empty(t, assignment.Receipts)

	type deliveryResult struct {
		receipt domain.ArenaDeliveryReceipt
		changed bool
		err     error
	}
	deliveryStart := make(chan struct{})
	deliveryResults := make(chan deliveryResult, 2)
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-deliveryStart
			receipt, changed, deliveryErr := repository.Deliver(
				ctx, assignmentID, uuid.New(), draft.participantIDs[0], baseTime.Add(4*time.Second),
			)
			deliveryResults <- deliveryResult{receipt: receipt, changed: changed, err: deliveryErr}
		}()
	}
	close(deliveryStart)
	workers.Wait()
	close(deliveryResults)
	deliveryWinners := 0
	var deliveredReceiptID uuid.UUID
	for item := range deliveryResults {
		require.NoError(t, item.err)
		if item.changed {
			deliveryWinners++
		}
		if deliveredReceiptID == uuid.Nil {
			deliveredReceiptID = item.receipt.ID
		} else {
			require.Equal(t, deliveredReceiptID, item.receipt.ID, "retry must recover the original receipt")
		}
	}
	require.Equal(t, 1, deliveryWinners)
	_, changed, err := repository.Deliver(
		ctx, assignmentID, uuid.New(), draft.participantIDs[1], baseTime.Add(5*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)

	_, changed, err = repository.Supersede(ctx, assignmentID, assignment.Revision, postgres.ArenaAssignmentSupersedeInput{
		ID: uuid.New(), ReservationID: finalReservation.ID,
		SnapshotID: finalSnapshot.Snapshot.SnapshotID, Reason: "skip ordered reserve",
		OccurredAt: baseTime.Add(6 * time.Second),
	})
	require.ErrorIs(t, err, domain.ErrConflict)
	require.False(t, changed, "supersession cannot skip the next reserved task")

	replacementID := uuid.New()
	replacement, changed, err := repository.Supersede(ctx, assignmentID, assignment.Revision, postgres.ArenaAssignmentSupersedeInput{
		ID: replacementID, ReservationID: reserveReservation.ID,
		SnapshotID: reserveSnapshot.Snapshot.SnapshotID, Reason: "primary task failure",
		OccurredAt: baseTime.Add(6 * time.Second),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, assignmentID, *replacement.SupersedesAssignmentID)
	require.Empty(t, replacement.Receipts, "supersession must not leak prior solved history")

	_, changed, err = repository.Supersede(ctx, assignmentID, assignment.Revision, postgres.ArenaAssignmentSupersedeInput{
		ID: uuid.New(), ReservationID: reserveReservation.ID, SnapshotID: reserveSnapshot.Snapshot.SnapshotID,
		Reason: "stale retry", OccurredAt: baseTime.Add(7 * time.Second),
	})
	require.NoError(t, err)
	require.False(t, changed)
	old, err := repository.GetAssignment(ctx, assignmentID)
	require.NoError(t, err)
	require.Equal(t, "superseded", old.State)
	require.Len(t, old.Receipts, 2)
}

func arenaAssignmentRepositoryBranch(
	t testing.TB,
	ctx context.Context,
	draft arenaDraftMigrationFixture,
	key string,
	category domain.Category,
) postgres.ArenaAssignmentBranchInput {
	t.Helper()
	branch := postgres.ArenaAssignmentBranchInput{
		ID: uuid.New(), DraftID: draft.draftID, DraftRevisionID: draft.initialRevisionID,
		Key: key, Categories: []domain.Category{category},
	}
	for position := 1; position <= 3; position++ {
		taskID := createArenaAssignmentMigrationTask(t, ctx, string(category), position)
		snapshot := domain.ArenaTaskSnapshot{
			SnapshotID: uuid.New(), TaskID: taskID, Version: 1, Kind: domain.ArenaTaskKindNormal,
			Title: key + " task", Description: "immutable assignment task description",
			Category: category, Difficulty: domain.DifficultyMedium, TimeLimit: 180,
			Flag: "FLAG{repository-snapshot}", Hints: []string{"repository hint"},
		}
		require.NoError(t, snapshot.Validate())
		branch.Edges = append(branch.Edges, postgres.ArenaAssignmentEdgeInput{
			ID: uuid.New(), ReservationID: uuid.New(), Position: position, Snapshot: snapshot,
			ContentDigest:     sha256.Sum256([]byte(snapshot.SnapshotID.String())),
			SelectionEvidence: map[string]any{"position": position, "eligible": true},
		})
	}
	return branch
}

func arenaAssignmentRepositorySlot(
	t testing.TB,
	plan *postgres.ArenaAssignmentPlanAggregate,
	branchID uuid.UUID,
	position int,
) (postgres.ArenaTaskReservationRecord, postgres.ArenaTaskSnapshotRecord) {
	t.Helper()
	var edgeID uuid.UUID
	for _, edge := range plan.Edges {
		if edge.BranchID == branchID && edge.Position == position {
			edgeID = edge.ID
			break
		}
	}
	require.NotEqual(t, uuid.Nil, edgeID)
	var reservation postgres.ArenaTaskReservationRecord
	for _, item := range plan.Reservations {
		if item.EdgeID == edgeID {
			reservation = item
			break
		}
	}
	require.NotEqual(t, uuid.Nil, reservation.ID)
	var snapshot postgres.ArenaTaskSnapshotRecord
	for _, item := range plan.Snapshots {
		if item.ReservationID == reservation.ID {
			snapshot = item
			break
		}
	}
	require.NotEqual(t, uuid.Nil, snapshot.Snapshot.SnapshotID)
	return reservation, snapshot
}

func countArenaReservationState(reservations []postgres.ArenaTaskReservationRecord, state string) int {
	count := 0
	for _, reservation := range reservations {
		if reservation.State == state {
			count++
		}
	}
	return count
}
