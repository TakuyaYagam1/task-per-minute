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

func TestAssignmentRepositoryCommitsProofAndDeliversExactlyOnce(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	draft := createDraftMigrationFixture(ctx, t)
	baseTime := draft.createdAt.Add(5 * time.Second)
	repository := postgres.NewAssignmentPostgres(postgres.NewTxManager(sharedPool))
	poolRevisionID := uuid.New()
	conservativeID := uuid.New()
	conservative, err := repository.CreateConservativePlan(ctx, postgres.ConservativePlanInput{
		ID: conservativeID, TournamentID: draft.tournamentID, RosterID: draft.rosterID,
		RevisionID: uuid.New(), SourceRosterRevision: 1, SourcePoolRevisionID: poolRevisionID,
		ConstraintGraph: map[string]any{"scope": "all-reachable-branches"},
		ProofEvidence:   map[string]any{"feasible": true}, CreatedAt: baseTime,
	})
	require.NoError(t, err)
	require.Equal(t, "conservative", conservative.Plan.Kind)
	require.Empty(t, conservative.Branches)

	exactID := uuid.New()
	decision, err := domain.NewDecisionEvidence(
		uuid.New(), domain.DecisionPurposeTask, domain.DecisionAlgorithmV1,
		[]string{"branch-a", "branch-b"}, exactID, baseTime,
	)
	require.NoError(t, err)
	branches := []postgres.AssignmentBranchInput{
		assignmentRepositoryBranch(ctx, t, draft, "branch-a", domain.CategoryWeb),
		assignmentRepositoryBranch(ctx, t, draft, "branch-b", domain.CategoryCrypto),
	}
	exactRevisionID := uuid.New()
	exact, err := repository.CreateExactPlan(ctx, postgres.ExactPlanInput{
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
	require.Equal(t, 3, countReservationState(exact.Reservations, "committed"))
	require.Equal(t, 3, countReservationState(exact.Reservations, "released"))

	primaryReservation, primarySnapshot := assignmentRepositorySlot(t, exact, activeBranchID, 1)
	reserveReservation, reserveSnapshot := assignmentRepositorySlot(t, exact, activeBranchID, 2)
	finalReservation, finalSnapshot := assignmentRepositorySlot(t, exact, activeBranchID, 3)
	slotID := createMigrationGameSlot(ctx, t, draft.seriesID, draft.rosterID, 1, "web")
	attemptID := createActiveMigrationAttempt(ctx, t, slotID, draft.seriesID, draft.rosterID, baseTime)
	_, err = repository.CreateAssignment(ctx, postgres.AssignmentCreateInput{
		ID: uuid.New(), AttemptID: attemptID, SeriesID: draft.seriesID, RosterID: draft.rosterID,
		PlanID: exactID, BranchID: activeBranchID, ReservationID: reserveReservation.ID,
		SnapshotID: reserveSnapshot.Snapshot.SnapshotID, CreatedAt: baseTime.Add(3 * time.Second),
	})
	require.ErrorIs(t, err, domain.ErrConflict, "initial assignment must use the primary proof edge")

	assignmentID := uuid.New()
	assignment, err := repository.CreateAssignment(ctx, postgres.AssignmentCreateInput{
		ID: assignmentID, AttemptID: attemptID, SeriesID: draft.seriesID, RosterID: draft.rosterID,
		PlanID: exactID, BranchID: activeBranchID, ReservationID: primaryReservation.ID,
		SnapshotID: primarySnapshot.Snapshot.SnapshotID, CreatedAt: baseTime.Add(3 * time.Second),
	})
	require.NoError(t, err)
	require.Equal(t, "active", assignment.State)
	require.Empty(t, assignment.Receipts)

	type deliveryResult struct {
		receipt domain.TaskDeliveryReceipt
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
	expectedFirstInstanceID := domain.ParticipantTaskInstanceID(assignmentID, draft.participantIDs[0])
	for item := range deliveryResults {
		require.NoError(t, item.err)
		require.Equal(t, expectedFirstInstanceID, item.receipt.InstanceID)
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
	secondReceipt, changed, err := repository.Deliver(
		ctx, assignmentID, uuid.New(), draft.participantIDs[1], baseTime.Add(5*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(
		t,
		domain.ParticipantTaskInstanceID(assignmentID, draft.participantIDs[1]),
		secondReceipt.InstanceID,
	)

	_, changed, err = repository.Supersede(ctx, assignmentID, assignment.Revision, postgres.AssignmentSupersedeInput{
		ID: uuid.New(), ReservationID: finalReservation.ID,
		SnapshotID: finalSnapshot.Snapshot.SnapshotID, Reason: "skip ordered reserve",
		OccurredAt: baseTime.Add(6 * time.Second),
	})
	require.ErrorIs(t, err, domain.ErrConflict)
	require.False(t, changed, "supersession cannot skip the next reserved task")

	replacementID := uuid.New()
	replacement, changed, err := repository.Supersede(ctx, assignmentID, assignment.Revision, postgres.AssignmentSupersedeInput{
		ID: replacementID, ReservationID: reserveReservation.ID,
		SnapshotID: reserveSnapshot.Snapshot.SnapshotID, Reason: "primary task failure",
		OccurredAt: baseTime.Add(6 * time.Second),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, assignmentID, *replacement.SupersedesAssignmentID)
	require.Empty(t, replacement.Receipts, "supersession must not leak prior solved history")

	_, changed, err = repository.Supersede(ctx, assignmentID, assignment.Revision, postgres.AssignmentSupersedeInput{
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

func assignmentRepositoryBranch(
	ctx context.Context, tb testing.TB,
	draft draftMigrationFixture,
	key string,
	category domain.Category,
) postgres.AssignmentBranchInput {
	tb.Helper()
	branch := postgres.AssignmentBranchInput{
		ID: uuid.New(), DraftID: draft.draftID, DraftRevisionID: draft.initialRevisionID,
		Key: key, Categories: []domain.Category{category},
	}
	for position := 1; position <= 3; position++ {
		taskID := createAssignmentMigrationTask(ctx, tb, string(category), position-1, nil)
		snapshot := domain.AssignmentTaskSnapshot{
			SnapshotID: uuid.New(), TaskID: taskID, Version: 1, Kind: domain.AssignmentTaskKindNormal,
			Title: key + " task", Description: "immutable assignment task description",
			Category: category, Difficulty: domain.DifficultyMedium, TimeLimit: 180,
			Flag: "FLAG{repository-snapshot}", Hints: []string{"repository hint"},
		}
		require.NoError(tb, snapshot.Validate())
		branch.Edges = append(branch.Edges, postgres.AssignmentEdgeInput{
			ID: uuid.New(), ReservationID: uuid.New(), Position: position, Snapshot: snapshot,
			ContentDigest:     sha256.Sum256([]byte(snapshot.SnapshotID.String())),
			SelectionEvidence: map[string]any{"position": position, "eligible": true},
		})
	}
	return branch
}

func assignmentRepositorySlot(
	tb testing.TB,
	plan *postgres.AssignmentPlanAggregate,
	branchID uuid.UUID,
	position int,
) (postgres.TaskReservationRecord, postgres.TaskSnapshotRecord) {
	tb.Helper()
	var edgeID uuid.UUID
	for _, edge := range plan.Edges {
		if edge.BranchID == branchID && edge.Position == position {
			edgeID = edge.ID
			break
		}
	}
	require.NotEqual(tb, uuid.Nil, edgeID)
	var reservation postgres.TaskReservationRecord
	for _, item := range plan.Reservations {
		if item.EdgeID == edgeID {
			reservation = item
			break
		}
	}
	require.NotEqual(tb, uuid.Nil, reservation.ID)
	var snapshot postgres.TaskSnapshotRecord
	for _, item := range plan.Snapshots {
		if item.ReservationID == reservation.ID {
			snapshot = item
			break
		}
	}
	require.NotEqual(tb, uuid.Nil, snapshot.Snapshot.SnapshotID)
	return reservation, snapshot
}

func countReservationState(reservations []postgres.TaskReservationRecord, state string) int {
	count := 0
	for _, reservation := range reservations {
		if reservation.State == state {
			count++
		}
	}
	return count
}
