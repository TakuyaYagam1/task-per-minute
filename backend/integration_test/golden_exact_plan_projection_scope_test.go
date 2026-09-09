//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestGoldenExactPlanSourceRejectsCrossTournamentAndWrongRevision(t *testing.T) {
	ctx := context.Background()
	first := createGoldenMigrationFixture(ctx, t, 4)
	second := createGoldenMigrationFixture(ctx, t, 4)
	firstProjectionID, firstRevision := createRoundProofProjection(
		ctx,
		t,
		first.tournamentID,
		first.rosterID,
		first.participantIDs[0],
		first.createdAt,
	)
	secondProjectionID, secondRevision := createRoundProofProjection(
		ctx,
		t,
		second.tournamentID,
		second.rosterID,
		second.participantIDs[0],
		second.createdAt,
	)

	t.Run("cross tournament", func(t *testing.T) {
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { require.NoError(t, tx.Rollback(ctx)) }()

		insertGoldenExactPlanSource(t, ctx, tx, first, secondProjectionID, secondRevision)
		_, err = tx.Exec(ctx, "SET CONSTRAINTS golden_exact_plan_snapshot_source_projection_fk IMMEDIATE")
		require.ErrorContains(t, err, "golden_exact_plan_snapshot_source_projection_fk")
	})

	t.Run("wrong revision", func(t *testing.T) {
		tx, err := sharedPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { require.NoError(t, tx.Rollback(ctx)) }()

		insertGoldenExactPlanSource(t, ctx, tx, first, firstProjectionID, firstRevision+1)
		_, err = tx.Exec(ctx, "SET CONSTRAINTS golden_exact_plan_snapshot_source_projection_fk IMMEDIATE")
		require.ErrorContains(t, err, "golden_exact_plan_snapshot_source_projection_fk")
	})
}

func insertGoldenExactPlanSource(
	t testing.TB,
	ctx context.Context,
	tx pgx.Tx,
	fixture goldenMigrationFixture,
	projectionID uuid.UUID,
	revision int64,
) {
	t.Helper()
	digest := make([]byte, 32)
	digest[0] = 1
	_, err := tx.Exec(ctx, `
		INSERT INTO golden_exact_plan_snapshots (
			plan_id, plan_revision_id, tournament_id, roster_id, plan_set_id,
			source_projection_revision_id, source_projection_revision,
			source_standings_artifact_id, source_standings_payload_digest,
			group_set_revision_id, group_set_revision, pool_revision_id, pool_revision,
			history_revision_id, history_revision, task_health_revision_id, task_health_revision,
			artifact_revision_id, artifact_revision, reservation_revision_id, reservation_revision,
			membership_revision_id, membership_revision, source_payload_digest, group_digest,
			pool_digest, history_digest, task_health_digest, artifact_digest, reservation_digest,
			membership_digest, proof_hash, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7,
			$8, $9,
			$10, 1, $11, 1,
			$12, 1, $13, 1,
			$14, 1, $15, 1,
			$16, 1, $9, $9,
			$9, $9, $9, $9, $9,
			$9, 'scope-proof', $17
		)`,
		uuid.New(), uuid.New(), fixture.tournamentID, fixture.rosterID, uuid.New(),
		projectionID, revision, uuid.New(), digest,
		uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(),
		fixture.createdAt,
	)
	require.NoError(t, err)
}
