//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type assignmentReservationFixture struct {
	reservationID uuid.UUID
	snapshotID    uuid.UUID
	taskID        uuid.UUID
	taskVersion   int
}

func TestAssignmentMigration(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	draft := createDraftMigrationFixture(ctx, t)
	createdAt := draft.createdAt.Add(5 * time.Second)
	conservativePlanID := createConservativeAssignmentPlan(ctx, t, draft, createdAt)
	exactPlanID := createExactAssignmentPlan(
		ctx, t,
		draft,
		conservativePlanID,
		createdAt.Add(time.Second),
	)

	activeBranchID := createAssignmentBranch(
		ctx, t,
		exactPlanID,
		draft,
		"crypto-final",
		`["crypto"]`,
		createdAt.Add(2*time.Second),
	)
	releasedBranchID := createAssignmentBranch(
		ctx, t,
		exactPlanID,
		draft,
		"pwn-final",
		`["pwn"]`,
		createdAt.Add(2*time.Second),
	)

	activeReservations := createAssignmentBranchReservations(
		ctx, t,
		exactPlanID,
		activeBranchID,
		"crypto",
		createdAt.Add(3*time.Second),
	)
	releasedReservations := createAssignmentBranchReservations(
		ctx, t,
		exactPlanID,
		releasedBranchID,
		"pwn",
		createdAt.Add(3*time.Second),
	)

	_, err := sharedPool.Exec(ctx, `
		UPDATE assignment_plans
		SET state = 'committed', active_branch_id = $2, committed_at = $3
		WHERE id = $1`, exactPlanID, activeBranchID, createdAt.Add(4*time.Second))
	require.Error(t, err)

	for _, reservation := range activeReservations {
		_, err = sharedPool.Exec(ctx, `
			UPDATE task_version_reservations
			SET state = 'committed', committed_at = $2
			WHERE id = $1`, reservation.reservationID, createdAt.Add(4*time.Second))
		require.NoError(t, err)
	}
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'active', activated_at = $2
		WHERE id = $1`, activeBranchID, createdAt.Add(5*time.Second))
	require.NoError(t, err)

	_, err = sharedPool.Exec(
		ctx, `
		UPDATE task_version_reservations
		SET state = 'released',
			disclosed_at = $2,
			released_at = $2,
			release_reason = 'unused draft branch'
		WHERE id = $1`,
		releasedReservations[0].reservationID,
		createdAt.Add(4*time.Second),
	)
	require.Error(t, err)

	for _, reservation := range releasedReservations {
		_, err = sharedPool.Exec(ctx, `
			UPDATE task_version_reservations
			SET state = 'released',
				released_at = $2,
				release_reason = 'unused draft branch'
			WHERE id = $1`, reservation.reservationID, createdAt.Add(4*time.Second))
		require.NoError(t, err)
	}
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'released',
			released_at = $2,
			release_reason = 'unused draft branch'
		WHERE id = $1`, releasedBranchID, createdAt.Add(5*time.Second))
	require.NoError(t, err)

	committedAt := createdAt.Add(6 * time.Second)
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_plans
		SET state = 'committed', active_branch_id = $2, committed_at = $3
		WHERE id = $1`, exactPlanID, activeBranchID, committedAt)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_plans
		SET proof_evidence = '{"changed":true}'::JSONB
		WHERE id = $1`, exactPlanID)
	require.Error(t, err)

	assertAssignmentTransitionEvidenceImmutable(
		ctx, t,
		exactPlanID,
		activeBranchID,
		releasedBranchID,
		activeReservations[0].reservationID,
		committedAt,
	)

	_, err = sharedPool.Exec(ctx, `
		UPDATE task_snapshots
		SET title = 'changed official evidence'
		WHERE id = $1`, activeReservations[0].snapshotID)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE tasks
		SET title = 'mutable catalog title'
		WHERE id = $1`, activeReservations[0].taskID)
	require.NoError(t, err)

	var snapshotTitle string
	err = sharedPool.QueryRow(ctx, `
		SELECT title
		FROM task_snapshots
		WHERE id = $1`, activeReservations[0].snapshotID).Scan(&snapshotTitle)
	require.NoError(t, err)
	require.NotEqual(t, "mutable catalog title", snapshotTitle)

	slotID := createMigrationGameSlot(
		ctx, t,
		draft.seriesID,
		draft.rosterID,
		1,
		"crypto",
	)
	attemptID := createActiveMigrationAttempt(
		ctx, t,
		slotID,
		draft.seriesID,
		draft.rosterID,
		committedAt,
	)
	assertCrossRosterAssignmentRejected(
		ctx, t,
		exactPlanID,
		activeBranchID,
		activeReservations[0],
		committedAt,
	)

	assignmentID := createActiveMigrationAssignment(
		ctx, t,
		attemptID,
		draft,
		exactPlanID,
		activeBranchID,
		activeReservations[0],
		nil,
		committedAt.Add(time.Second),
	)

	deliveredAt := committedAt.Add(2 * time.Second)
	_, err = sharedPool.Exec(ctx, `
		UPDATE task_version_reservations
		SET disclosed_at = $2
		WHERE id = $1`, activeReservations[0].reservationID, deliveredAt)
	require.NoError(t, err)

	_, err = sharedPool.Exec(
		ctx, `
		INSERT INTO task_delivery_receipts (
			assignment_id, instance_id, attempt_id, roster_id, participant_id,
			snapshot_id, task_id, task_version, delivered_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		assignmentID,
		domain.ParticipantTaskInstanceID(assignmentID, draft.participantIDs[0]),
		attemptID,
		draft.rosterID,
		draft.participantIDs[0],
		activeReservations[1].snapshotID,
		activeReservations[1].taskID,
		activeReservations[1].taskVersion,
		deliveredAt,
	)
	require.Error(t, err)

	for _, participantID := range draft.participantIDs {
		_, err = sharedPool.Exec(
			ctx, `
			INSERT INTO task_delivery_receipts (
				assignment_id, instance_id, attempt_id, roster_id, participant_id,
				snapshot_id, task_id, task_version, delivered_at, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
			assignmentID,
			domain.ParticipantTaskInstanceID(assignmentID, participantID),
			attemptID,
			draft.rosterID,
			participantID,
			activeReservations[0].snapshotID,
			activeReservations[0].taskID,
			activeReservations[0].taskVersion,
			deliveredAt,
		)
		require.NoError(t, err)
	}

	_, err = sharedPool.Exec(
		ctx, `
		INSERT INTO task_delivery_receipts (
			assignment_id, instance_id, attempt_id, roster_id, participant_id,
			snapshot_id, task_id, task_version, delivered_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		assignmentID,
		domain.ParticipantTaskInstanceID(assignmentID, draft.participantIDs[0]),
		attemptID,
		draft.rosterID,
		draft.participantIDs[0],
		activeReservations[0].snapshotID,
		activeReservations[0].taskID,
		activeReservations[0].taskVersion,
		deliveredAt,
	)
	require.Error(t, err)

	supersededAt := deliveredAt.Add(time.Second)
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignments
		SET state = 'superseded',
			revision = revision + 1,
			updated_at = $2,
			superseded_at = $2,
			supersession_reason = 'primary task failure'
		WHERE id = $1`, assignmentID, supersededAt)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE task_version_reservations
		SET disclosed_at = $2
		WHERE id = $1`, activeReservations[1].reservationID, supersededAt)
	require.NoError(t, err)

	replacementAssignmentID := createActiveMigrationAssignment(
		ctx, t,
		attemptID,
		draft,
		exactPlanID,
		activeBranchID,
		activeReservations[1],
		assignmentID,
		supersededAt.Add(time.Second),
	)
	require.NotEqual(t, assignmentID, replacementAssignmentID)

	_, err = sharedPool.Exec(ctx, `
		UPDATE task_version_reservations
		SET state = 'released',
			released_at = $2,
			release_reason = 'attempted reuse'
		WHERE id = $1`, activeReservations[1].reservationID, supersededAt.Add(2*time.Second))
	require.Error(t, err)

	var (
		activeAssignments int
		retainedOld       int
		releasedBranches  int
	)
	err = sharedPool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE state = 'active'),
			COUNT(*) FILTER (WHERE id = $2 AND state = 'superseded')
		FROM assignments
		WHERE attempt_id = $1`, attemptID, assignmentID).Scan(&activeAssignments, &retainedOld)
	require.NoError(t, err)
	require.Equal(t, 1, activeAssignments)
	require.Equal(t, 1, retainedOld)

	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM assignment_branches
		WHERE plan_id = $1 AND state = 'released'`, exactPlanID).Scan(&releasedBranches)
	require.NoError(t, err)
	require.Equal(t, 1, releasedBranches)
}
