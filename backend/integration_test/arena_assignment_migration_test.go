//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type arenaAssignmentReservationFixture struct {
	reservationID uuid.UUID
	snapshotID    uuid.UUID
	taskID        uuid.UUID
	taskVersion   int
}

func TestArenaAssignmentMigration(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	draft := createArenaDraftMigrationFixture(t, ctx)
	createdAt := draft.createdAt.Add(5 * time.Second)
	conservativePlanID := createConservativeArenaAssignmentPlan(t, ctx, draft, createdAt)
	exactPlanID := createExactArenaAssignmentPlan(
		t,
		ctx,
		draft,
		conservativePlanID,
		createdAt.Add(time.Second),
	)

	activeBranchID := createArenaAssignmentBranch(
		t,
		ctx,
		exactPlanID,
		draft,
		"crypto-final",
		`["crypto"]`,
		createdAt.Add(2*time.Second),
	)
	releasedBranchID := createArenaAssignmentBranch(
		t,
		ctx,
		exactPlanID,
		draft,
		"pwn-final",
		`["pwn"]`,
		createdAt.Add(2*time.Second),
	)

	activeReservations := createArenaAssignmentBranchReservations(
		t,
		ctx,
		exactPlanID,
		activeBranchID,
		"crypto",
		createdAt.Add(3*time.Second),
	)
	releasedReservations := createArenaAssignmentBranchReservations(
		t,
		ctx,
		exactPlanID,
		releasedBranchID,
		"pwn",
		createdAt.Add(3*time.Second),
	)

	_, err := sharedPool.Exec(ctx, `
		UPDATE arena_assignment_plans
		SET state = 'committed', active_branch_id = $2, committed_at = $3
		WHERE id = $1`, exactPlanID, activeBranchID, createdAt.Add(4*time.Second))
	require.Error(t, err)

	for _, reservation := range activeReservations {
		_, err = sharedPool.Exec(ctx, `
			UPDATE arena_task_version_reservations
			SET state = 'committed', committed_at = $2
			WHERE id = $1`, reservation.reservationID, createdAt.Add(4*time.Second))
		require.NoError(t, err)
	}
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_assignment_branches
		SET state = 'active', activated_at = $2
		WHERE id = $1`, activeBranchID, createdAt.Add(5*time.Second))
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_task_version_reservations
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
			UPDATE arena_task_version_reservations
			SET state = 'released',
				released_at = $2,
				release_reason = 'unused draft branch'
			WHERE id = $1`, reservation.reservationID, createdAt.Add(4*time.Second))
		require.NoError(t, err)
	}
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_assignment_branches
		SET state = 'released',
			released_at = $2,
			release_reason = 'unused draft branch'
		WHERE id = $1`, releasedBranchID, createdAt.Add(5*time.Second))
	require.NoError(t, err)

	committedAt := createdAt.Add(6 * time.Second)
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_assignment_plans
		SET state = 'committed', active_branch_id = $2, committed_at = $3
		WHERE id = $1`, exactPlanID, activeBranchID, committedAt)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_assignment_plans
		SET proof_evidence = '{"changed":true}'::JSONB
		WHERE id = $1`, exactPlanID)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_task_snapshots
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
		FROM arena_task_snapshots
		WHERE id = $1`, activeReservations[0].snapshotID).Scan(&snapshotTitle)
	require.NoError(t, err)
	require.NotEqual(t, "mutable catalog title", snapshotTitle)

	slotID := createArenaMigrationGameSlot(
		t,
		ctx,
		draft.seriesID,
		draft.rosterID,
		1,
		"crypto",
	)
	attemptID := createActiveArenaMigrationAttempt(
		t,
		ctx,
		slotID,
		draft.seriesID,
		draft.rosterID,
		committedAt,
	)
	assertCrossRosterArenaAssignmentRejected(
		t,
		ctx,
		exactPlanID,
		activeBranchID,
		activeReservations[0],
		committedAt,
	)

	assignmentID := createActiveArenaMigrationAssignment(
		t,
		ctx,
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
		UPDATE arena_task_version_reservations
		SET disclosed_at = $2
		WHERE id = $1`, activeReservations[0].reservationID, deliveredAt)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_task_delivery_receipts (
			assignment_id, attempt_id, roster_id, participant_id,
			snapshot_id, task_id, task_version, delivered_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`,
		assignmentID,
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
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO arena_task_delivery_receipts (
				assignment_id, attempt_id, roster_id, participant_id,
				snapshot_id, task_id, task_version, delivered_at, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`,
			assignmentID,
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

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_task_delivery_receipts (
			assignment_id, attempt_id, roster_id, participant_id,
			snapshot_id, task_id, task_version, delivered_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`,
		assignmentID,
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
		UPDATE arena_assignments
		SET state = 'superseded',
			revision = revision + 1,
			updated_at = $2,
			superseded_at = $2,
			supersession_reason = 'primary task failure'
		WHERE id = $1`, assignmentID, supersededAt)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_task_version_reservations
		SET disclosed_at = $2
		WHERE id = $1`, activeReservations[1].reservationID, supersededAt)
	require.NoError(t, err)

	replacementAssignmentID := createActiveArenaMigrationAssignment(
		t,
		ctx,
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
		UPDATE arena_task_version_reservations
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
		FROM arena_assignments
		WHERE attempt_id = $1`, attemptID, assignmentID).Scan(&activeAssignments, &retainedOld)
	require.NoError(t, err)
	require.Equal(t, 1, activeAssignments)
	require.Equal(t, 1, retainedOld)

	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_assignment_branches
		WHERE plan_id = $1 AND state = 'released'`, exactPlanID).Scan(&releasedBranches)
	require.NoError(t, err)
	require.Equal(t, 1, releasedBranches)
}

func assertCrossRosterArenaAssignmentRejected(
	t testing.TB,
	ctx context.Context,
	planID uuid.UUID,
	branchID uuid.UUID,
	reservation arenaAssignmentReservationFixture,
	createdAt time.Time,
) {
	t.Helper()

	otherDraft := createArenaDraftMigrationFixture(t, ctx)
	otherSlotID := createArenaMigrationGameSlot(
		t,
		ctx,
		otherDraft.seriesID,
		otherDraft.rosterID,
		1,
		"crypto",
	)
	otherAttemptID := createActiveArenaMigrationAttempt(
		t,
		ctx,
		otherSlotID,
		otherDraft.seriesID,
		otherDraft.rosterID,
		createdAt,
	)

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_assignments (
			id, attempt_id, series_id, roster_id,
			plan_id, branch_id, reservation_id, snapshot_id,
			task_id, task_version, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10, $11, $11
		)`,
		uuid.New(),
		otherAttemptID,
		otherDraft.seriesID,
		otherDraft.rosterID,
		planID,
		branchID,
		reservation.reservationID,
		reservation.snapshotID,
		reservation.taskID,
		reservation.taskVersion,
		createdAt.Add(time.Second),
	)
	require.Error(t, err)
}

func createConservativeArenaAssignmentPlan(
	t testing.TB,
	ctx context.Context,
	draft arenaDraftMigrationFixture,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	planID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_assignment_plans (
			id, tournament_id, roster_id, kind, revision_id,
			source_roster_revision, source_pool_revision_id,
			constraint_graph, proof_evidence, created_at
		)
		VALUES (
			$1, $2, $3, 'conservative', $4,
			1, $5,
			'{"type":"capacity"}'::JSONB,
			'{"covers":"full_roster"}'::JSONB,
			$6
		)`,
		planID,
		draft.tournamentID,
		draft.rosterID,
		uuid.New(),
		draft.categoryRevisionID,
		createdAt,
	)
	require.NoError(t, err)
	return planID
}

func createExactArenaAssignmentPlan(
	t testing.TB,
	ctx context.Context,
	draft arenaDraftMigrationFixture,
	parentPlanID uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	planID := uuid.New()
	seed := bytes.Repeat([]byte{5}, 32)
	digest := bytes.Repeat([]byte{6}, 32)
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_assignment_plans (
			id, tournament_id, roster_id, kind, parent_plan_id, revision_id,
			source_roster_revision, source_pool_revision_id,
			source_draft_revision_id, reachable_branch_count,
			constraint_graph, proof_evidence,
			decision_evidence_id, decision_algorithm_version,
			decision_inputs, decision_seed, decision_result,
			decision_replay_digest, decision_owner_id, decided_at, created_at
		)
		VALUES (
			$1, $2, $3, 'exact', $4, $5,
			1, $6,
			$7, 2,
			'{"nodes":6,"edges":6}'::JSONB,
			'{"eligible":true}'::JSONB,
			$8, 'hmac-sha256-order-v1',
			'["crypto-final","pwn-final"]'::JSONB,
			$9,
			'["crypto-final","pwn-final"]'::JSONB,
			$10, $1, $11, $11
		)`,
		planID,
		draft.tournamentID,
		draft.rosterID,
		parentPlanID,
		uuid.New(),
		draft.categoryRevisionID,
		draft.initialRevisionID,
		uuid.New(),
		seed,
		digest,
		createdAt,
	)
	require.NoError(t, err)
	return planID
}

func createArenaAssignmentBranch(
	t testing.TB,
	ctx context.Context,
	planID uuid.UUID,
	draft arenaDraftMigrationFixture,
	branchKey string,
	categorySequence string,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	branchID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_assignment_branches (
			id, plan_id, draft_id, draft_revision_id,
			branch_key, category_sequence, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6::JSONB, $7)`,
		branchID,
		planID,
		draft.draftID,
		draft.initialRevisionID,
		branchKey,
		categorySequence,
		createdAt,
	)
	require.NoError(t, err)
	return branchID
}

func createArenaAssignmentBranchReservations(
	t testing.TB,
	ctx context.Context,
	planID uuid.UUID,
	branchID uuid.UUID,
	category string,
	createdAt time.Time,
) []arenaAssignmentReservationFixture {
	t.Helper()

	reservations := make([]arenaAssignmentReservationFixture, 3)
	for i := range reservations {
		taskID := createArenaAssignmentMigrationTask(t, ctx, category, i)
		edgeID := uuid.New()
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO arena_assignment_plan_edges (
				id, plan_id, branch_id, position,
				task_id, task_version, selection_evidence, created_at
			)
			VALUES (
				$1, $2, $3, $4,
				$5, 1, '{"eligible":true}'::JSONB, $6
			)`, edgeID, planID, branchID, i+1, taskID, createdAt)
		require.NoError(t, err)

		reservationID := uuid.New()
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO arena_task_version_reservations (
				id, edge_id, plan_id, branch_id,
				task_id, task_version, created_at
			)
			VALUES ($1, $2, $3, $4, $5, 1, $6)`,
			reservationID,
			edgeID,
			planID,
			branchID,
			taskID,
			createdAt,
		)
		require.NoError(t, err)

		snapshotID := uuid.New()
		digest := bytes.Repeat([]byte{byte(i + 10)}, 32)
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO arena_task_snapshots (
				id, reservation_id, task_id, task_version, kind,
				title, description, category, difficulty,
				time_limit, flag, hints, content_digest, created_at
			)
			VALUES (
				$1, $2, $3, 1, 'normal',
				$4, 'immutable description', $5, 'medium',
				180, 'FLAG{snapshot}', '["hint"]'::JSONB, $6, $7
			)`,
			snapshotID,
			reservationID,
			taskID,
			fmt.Sprintf("snapshot %d", i+1),
			category,
			digest,
			createdAt,
		)
		require.NoError(t, err)

		reservations[i] = arenaAssignmentReservationFixture{
			reservationID: reservationID,
			snapshotID:    snapshotID,
			taskID:        taskID,
			taskVersion:   1,
		}
	}
	return reservations
}

func createArenaAssignmentMigrationTask(
	t testing.TB,
	ctx context.Context,
	category string,
	position int,
) uuid.UUID {
	t.Helper()

	var taskID uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO tasks (
			title, description, category, difficulty, time_limit, flag
		)
		VALUES ($1, 'assignment task', $2, 'medium', 180, 'FLAG{catalog}')
		RETURNING id`,
		fmt.Sprintf("arena_%s_%d_%s", category, position, uuid.NewString()[:8]),
		category,
	).Scan(&taskID)
	require.NoError(t, err)
	return taskID
}

func createActiveArenaMigrationAssignment(
	t testing.TB,
	ctx context.Context,
	attemptID uuid.UUID,
	draft arenaDraftMigrationFixture,
	planID uuid.UUID,
	branchID uuid.UUID,
	reservation arenaAssignmentReservationFixture,
	supersedesAssignmentID any,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	assignmentID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_assignments (
			id, attempt_id, series_id, roster_id,
			plan_id, branch_id, reservation_id, snapshot_id,
			task_id, task_version, supersedes_assignment_id,
			created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10, $11,
			$12, $12
		)`,
		assignmentID,
		attemptID,
		draft.seriesID,
		draft.rosterID,
		planID,
		branchID,
		reservation.reservationID,
		reservation.snapshotID,
		reservation.taskID,
		reservation.taskVersion,
		supersedesAssignmentID,
		createdAt,
	)
	require.NoError(t, err)
	return assignmentID
}
