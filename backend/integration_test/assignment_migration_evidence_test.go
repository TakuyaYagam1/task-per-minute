//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func assertAssignmentTransitionEvidenceImmutable(
	ctx context.Context, tb testing.TB,
	planID uuid.UUID,
	activeBranchID uuid.UUID,
	releasedBranchID uuid.UUID,
	reservationID uuid.UUID,
	committedAt time.Time,
) {
	tb.Helper()

	changedAt := committedAt.Add(time.Second)
	_, err := sharedPool.Exec(ctx, `
		UPDATE assignment_plans
		SET active_branch_id = $2
		WHERE id = $1`, planID, releasedBranchID)
	require.Error(tb, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_plans
		SET committed_at = $2
		WHERE id = $1`, planID, changedAt)
	require.Error(tb, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET activated_at = $2
		WHERE id = $1`, activeBranchID, changedAt)
	require.Error(tb, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE task_version_reservations
		SET committed_at = $2
		WHERE id = $1`, reservationID, changedAt)
	require.Error(tb, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET disclosed_at = $2
		WHERE id = $1`, activeBranchID, changedAt)
	require.NoError(tb, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET disclosed_at = NULL
		WHERE id = $1`, activeBranchID)
	require.Error(tb, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'superseded',
			superseded_at = $2,
			supersession_reason = 'attempted branch replacement'
		WHERE id = $1`, activeBranchID, changedAt)
	require.Error(tb, err)
}

func assertCrossRosterAssignmentRejected(
	ctx context.Context, tb testing.TB,
	planID uuid.UUID,
	branchID uuid.UUID,
	reservation assignmentReservationFixture,
	createdAt time.Time,
) {
	tb.Helper()

	otherDraft := createDraftMigrationFixture(ctx, tb)
	otherSlotID := createMigrationGameSlot(
		ctx, tb,
		otherDraft.seriesID,
		otherDraft.rosterID,
		1,
		"crypto",
	)
	otherAttemptID := createActiveMigrationAttempt(
		ctx, tb,
		otherSlotID,
		otherDraft.seriesID,
		otherDraft.rosterID,
		createdAt,
	)

	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO assignments (
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
	require.Error(tb, err)
}

func createConservativeAssignmentPlan(
	ctx context.Context, tb testing.TB,
	draft draftMigrationFixture,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	planID := uuid.New()
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO assignment_plans (
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
		draft.normalPoolRevisionID,
		createdAt,
	)
	require.NoError(tb, err)
	return planID
}

func createExactAssignmentPlan(
	ctx context.Context, tb testing.TB,
	draft draftMigrationFixture,
	parentPlanID uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	planID := uuid.New()
	seed := bytes.Repeat([]byte{5}, 32)
	digest := bytes.Repeat([]byte{6}, 32)
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO assignment_plans (
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
		draft.normalPoolRevisionID,
		draft.initialRevisionID,
		uuid.New(),
		seed,
		digest,
		createdAt,
	)
	require.NoError(tb, err)
	return planID
}
