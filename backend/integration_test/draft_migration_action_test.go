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

func commitDraftActionRevision(
	ctx context.Context, tb testing.TB,
	fixture draftMigrationFixture,
	revisionID uuid.UUID,
	previousRevisionID uuid.UUID,
	commandID uuid.UUID,
	serviceEpoch uuid.UUID,
	revision int,
	state string,
	turn int,
	currentActorID any,
	currentAction any,
	absoluteDeadline any,
	actionTurn int,
	actionActorID uuid.UUID,
	action string,
	category string,
	scheduledDeadline time.Time,
	automatic bool,
) {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(
		ctx, `
		INSERT INTO draft_revisions (
			id, draft_id, series_id, roster_id, revision,
			previous_revision_id, command_id, service_epoch,
			state, turn_number, current_actor_id, current_action,
			absolute_deadline, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $11, $12,
			$13, $14
		)`,
		revisionID,
		fixture.draftID,
		fixture.seriesID,
		fixture.rosterID,
		revision,
		previousRevisionID,
		commandID,
		serviceEpoch,
		state,
		turn,
		currentActorID,
		currentAction,
		absoluteDeadline,
		scheduledDeadline,
	)
	require.NoError(tb, err)

	_, err = tx.Exec(
		ctx, `
		INSERT INTO draft_actions (
			draft_id, result_revision_id, command_id,
			turn_number, actor_id, action, category,
			scheduled_deadline, occurred_at, automatic, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $9)`,
		fixture.draftID,
		revisionID,
		commandID,
		actionTurn,
		actionActorID,
		action,
		category,
		scheduledDeadline,
		scheduledDeadline.Add(-time.Second),
		automatic,
	)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
}

func assertIllegalDraftActionRejected(
	ctx context.Context, tb testing.TB,
	fixture draftMigrationFixture,
	previousRevisionID uuid.UUID,
	serviceEpoch uuid.UUID,
	deadline time.Time,
) {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()

	revisionID := uuid.New()
	commandID := uuid.New()
	_, err = tx.Exec(
		ctx, `
		INSERT INTO draft_revisions (
			id, draft_id, series_id, roster_id, revision,
			previous_revision_id, command_id, service_epoch,
			state, turn_number, selected_categories, created_at
		)
		VALUES (
			$1, $2, $3, $4, 6,
			$5, $6, $7,
			'completed', 2, '["pwn"]'::JSONB, $8
		)`,
		revisionID,
		fixture.draftID,
		fixture.seriesID,
		fixture.rosterID,
		previousRevisionID,
		commandID,
		serviceEpoch,
		deadline,
	)
	require.NoError(tb, err)

	_, err = tx.Exec(
		ctx, `
		INSERT INTO draft_actions (
			draft_id, result_revision_id, command_id,
			turn_number, actor_id, action, category,
			scheduled_deadline, occurred_at, created_at
		)
		VALUES ($1, $2, $3, 2, $4, 'ban', 'crypto', $5, $6, $6)`,
		fixture.draftID,
		revisionID,
		commandID,
		fixture.participantIDs[0],
		deadline,
		deadline.Add(-time.Second),
	)
	require.Error(tb, err)
}

func commitCompletedDraft(
	ctx context.Context, tb testing.TB,
	fixture draftMigrationFixture,
	revisionID uuid.UUID,
	previousRevisionID uuid.UUID,
	commandID uuid.UUID,
	serviceEpoch uuid.UUID,
	deadline time.Time,
) {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()

	seed := bytes.Repeat([]byte{3}, 32)
	digest := bytes.Repeat([]byte{4}, 32)
	_, err = tx.Exec(
		ctx, `
		INSERT INTO draft_revisions (
			id, draft_id, series_id, roster_id, revision,
			previous_revision_id, command_id, service_epoch,
			state, turn_number, selected_categories,
			decision_evidence_id, decision_purpose,
			decision_algorithm_version, decision_inputs,
			decision_seed, decision_result, decision_replay_digest,
			decision_owner_id, decided_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, 6,
			$5, $6, $7,
			'completed', 2, '["pwn"]'::JSONB,
			$8, 'category',
			'hmac-sha256-order-v1', '["crypto","pwn"]'::JSONB,
			$9, '["crypto","pwn"]'::JSONB, $10,
			$2, $11, $12
		)`,
		revisionID,
		fixture.draftID,
		fixture.seriesID,
		fixture.rosterID,
		previousRevisionID,
		commandID,
		serviceEpoch,
		uuid.New(),
		seed,
		digest,
		deadline.Add(-time.Second),
		deadline,
	)
	require.NoError(tb, err)

	_, err = tx.Exec(
		ctx, `
		INSERT INTO draft_actions (
			draft_id, result_revision_id, command_id,
			turn_number, actor_id, action, category,
			scheduled_deadline, occurred_at, automatic, created_at
		)
		VALUES ($1, $2, $3, 2, $4, 'ban', 'crypto', $5, $6, TRUE, $6)`,
		fixture.draftID,
		revisionID,
		commandID,
		fixture.participantIDs[1],
		deadline,
		deadline.Add(-time.Second),
	)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
}
