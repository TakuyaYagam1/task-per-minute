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

func createAssignmentBranch(
	ctx context.Context, tb testing.TB,
	planID uuid.UUID,
	draft draftMigrationFixture,
	branchKey string,
	categorySequence string,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	branchID := uuid.New()
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO assignment_branches (
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
	require.NoError(tb, err)
	return branchID
}

func createAssignmentBranchReservations(
	ctx context.Context, tb testing.TB,
	planID uuid.UUID,
	branchID uuid.UUID,
	category string,
	createdAt time.Time,
) []assignmentReservationFixture {
	tb.Helper()

	reservations := make([]assignmentReservationFixture, 3)
	for i := range reservations {
		taskID, taskVersion := createAssignmentMigrationTask(ctx, tb, category, i, &planID)
		edgeID := uuid.New()
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO assignment_plan_edges (
				id, plan_id, branch_id, position,
				task_id, task_version, selection_evidence, created_at
			)
			VALUES (
				$1, $2, $3, $4,
				$5, $6, '{"eligible":true}'::JSONB, $7
			)`, edgeID, planID, branchID, i+1, taskID, taskVersion, createdAt)
		require.NoError(tb, err)

		reservationID := uuid.New()
		_, err = sharedPool.Exec(
			ctx, `
			INSERT INTO task_version_reservations (
				id, edge_id, plan_id, branch_id,
				task_id, task_version, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			reservationID,
			edgeID,
			planID,
			branchID,
			taskID,
			taskVersion,
			createdAt,
		)
		require.NoError(tb, err)

		snapshotID := uuid.New()
		digest := bytes.Repeat([]byte{byte(i + 10)}, 32)
		_, err = sharedPool.Exec(
			ctx, `
			INSERT INTO task_snapshots (
				id, reservation_id, task_id, task_version, kind,
				title, description, category, difficulty,
				time_limit, flag, hints, content_digest, created_at
			)
			VALUES (
				$1, $2, $3, $4, 'normal',
				$5, 'immutable description', $6, 'medium',
				180, 'FLAG{snapshot}', '["hint"]'::JSONB, $7, $8
			)`,
			snapshotID,
			reservationID,
			taskID,
			taskVersion,
			fmt.Sprintf("snapshot %d", i+1),
			category,
			digest,
			createdAt,
		)
		require.NoError(tb, err)

		reservations[i] = assignmentReservationFixture{
			reservationID: reservationID,
			snapshotID:    snapshotID,
			taskID:        taskID,
			taskVersion:   taskVersion,
		}
	}
	return reservations
}

func createAssignmentMigrationTask(
	ctx context.Context, tb testing.TB,
	category string,
	position int,
	excludedPlanID *uuid.UUID,
) (uuid.UUID, int) {
	tb.Helper()

	var taskID uuid.UUID
	var taskVersion int
	err := sharedPool.QueryRow(ctx, `
		WITH pinned_normal_pool AS (
			SELECT pool.id
			FROM assignment_plans AS plan
			INNER JOIN task_pool_revisions AS pool
				ON pool.id = plan.source_pool_revision_id
				AND pool.kind = 'normal'
			WHERE plan.id = $3::UUID
		)
		SELECT task.id, membership.task_version
		FROM pinned_normal_pool AS pool
		INNER JOIN task_pool_version_memberships AS membership
			ON membership.task_pool_revision_id = pool.id
		INNER JOIN tasks AS task
			ON task.id = membership.task_id
		INNER JOIN task_versions AS version
			ON version.task_id = membership.task_id
			AND version.version = membership.task_version
		LEFT JOIN LATERAL (
			SELECT attestation.healthy
			FROM task_version_health_attestations AS attestation
			WHERE attestation.task_id = membership.task_id
				AND attestation.task_version = membership.task_version
			ORDER BY attestation.revision DESC
			LIMIT 1
		) AS health ON true
		WHERE task.kind = 'normal'
			AND task.category = $1
			AND task.enabled
			AND task.deleted_at IS NULL
			AND health.healthy
			AND (
				$3::UUID IS NULL
				OR NOT EXISTS (
					SELECT 1
					FROM task_version_reservations AS reservation
					WHERE reservation.plan_id = $3::UUID
						AND reservation.task_id = membership.task_id
						AND reservation.task_version = membership.task_version
				)
			)
		ORDER BY task.id
		OFFSET $2
		LIMIT 1`, category, position, excludedPlanID).Scan(&taskID, &taskVersion)
	require.NoError(tb, err)
	return taskID, taskVersion
}

func createActiveMigrationAssignment(
	ctx context.Context, tb testing.TB,
	attemptID uuid.UUID,
	draft draftMigrationFixture,
	planID uuid.UUID,
	branchID uuid.UUID,
	reservation assignmentReservationFixture,
	supersedesAssignmentID any,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	assignmentID := uuid.New()
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO assignments (
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
	require.NoError(tb, err)
	return assignmentID
}
