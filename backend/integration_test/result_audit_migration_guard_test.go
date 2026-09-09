//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func assertResultEvidenceIsImmutable(
	ctx context.Context, tb testing.TB,
	fixture resultAuditMigrationFixture,
	commit resultAuditCommit,
) {
	tb.Helper()

	_, err := sharedPool.Exec(ctx, `
		UPDATE official_result_revisions
		SET result_reason = 'operator_forfeit'
		WHERE id = $1`, commit.gameResultRevisionID)
	require.Error(tb, err)
	_, err = sharedPool.Exec(ctx, `
		DELETE FROM result_events WHERE id = $1`, commit.resultEventID)
	require.Error(tb, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE audit_events
		SET payload = '{"reason":"changed"}'::JSONB
		WHERE id = $1`, commit.auditEventID)
	require.Error(tb, err)
	_, err = sharedPool.Exec(
		ctx, `
		INSERT INTO official_result_heads (
			entity_kind, entity_id, series_id, roster_id,
			game_attempt_id, current_revision_id, updated_at
		)
		VALUES ('game_attempt', $1, $2, $3, $1, $4, $5)`,
		fixture.attemptID,
		fixture.draft.seriesID,
		fixture.draft.rosterID,
		commit.gameResultRevisionID,
		commit.settledAt.Add(time.Second),
	)
	require.Error(tb, err)
}

func assertResultEventCannotCommitPartially(
	ctx context.Context, tb testing.TB,
	fixture resultAuditMigrationFixture,
) {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	var serverSequence int64
	err = tx.QueryRow(ctx, `
		UPDATE game_attempts
		SET result_event_sequence = result_event_sequence + 1
		WHERE id = $1
		RETURNING result_event_sequence`, fixture.attemptID).Scan(&serverSequence)
	require.NoError(tb, err)
	_, err = tx.Exec(
		ctx, `
		INSERT INTO result_events (
			tournament_id, roster_id, series_id, attempt_id,
			server_sequence, idempotency_key, result_state,
			result_reason, occurred_at, created_at
		)
		VALUES (
			$1, $2, $3, $4,
			$5, $6, 'superseded',
			'derived_revision_superseded', $7, $7
		)`,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		serverSequence,
		uuid.New(),
		fixture.lockedAt.Add(6*time.Second),
	)
	require.NoError(tb, err)
	require.Error(tb, tx.Commit(ctx))
}

func assertAuditRejectsNestedFlag(
	ctx context.Context, tb testing.TB,
	fixture resultAuditMigrationFixture,
) {
	tb.Helper()

	resultEventID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var serverSequence int64
	err = tx.QueryRow(ctx, `
		UPDATE game_attempts
		SET result_event_sequence = result_event_sequence + 1
		WHERE id = $1
		RETURNING result_event_sequence`, fixture.attemptID).Scan(&serverSequence)
	require.NoError(tb, err)
	createdAt := fixture.lockedAt.Add(7 * time.Second)
	_, err = tx.Exec(
		ctx, `
		INSERT INTO result_events (
			id, tournament_id, roster_id, series_id, attempt_id,
			server_sequence, idempotency_key, result_state,
			result_reason, occurred_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, 'superseded',
			'derived_revision_superseded', $8, $8
		)`,
		resultEventID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		serverSequence,
		uuid.New(),
		createdAt,
	)
	require.NoError(tb, err)
	_, err = tx.Exec(
		ctx, `
		INSERT INTO audit_events (
			tournament_id, roster_id, series_id, result_event_id,
			actor_kind, action, payload, occurred_at, created_at
		)
		VALUES (
			$1, $2, $3, $4,
			'server', 'tournament.result.rejected',
			'{"details":{"flag":"FLAG{secret}"}}'::JSONB, $5, $5
		)`,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		resultEventID,
		createdAt,
	)
	require.Error(tb, err)
}

func assertOutboxRetainsPublishedEvidence(
	ctx context.Context, tb testing.TB,
	commit resultAuditCommit,
) {
	tb.Helper()

	publishedAt := commit.settledAt.Add(time.Second)
	workerID := uuid.New()
	claimToken := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		UPDATE outbox_events
		SET claimed_by = $2,
			claim_token = $3,
			claimed_until = $4,
			attempt_count = attempt_count + 1
		WHERE id = $1`, commit.outboxEventID, workerID, claimToken, publishedAt.Add(time.Minute))
	require.NoError(tb, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE outbox_events
		SET published_at = $2,
			claimed_by = NULL,
			claim_token = NULL,
			claimed_until = NULL
		WHERE id = $1`, commit.outboxEventID, publishedAt)
	require.NoError(tb, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE outbox_events
		SET payload = '{"changed":true}'::JSONB
		WHERE id = $1`, commit.outboxEventID)
	require.Error(tb, err)
	_, err = sharedPool.Exec(ctx, `
		DELETE FROM outbox_events WHERE id = $1`, commit.outboxEventID)
	require.Error(tb, err)
}
