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

func createResultCommit(
	ctx context.Context, tb testing.TB,
	fixture resultAuditMigrationFixture,
	submissionID uuid.UUID,
	advanceCurrentHeads bool,
) (resultAuditCommit, error) {
	tb.Helper()

	commit := resultAuditCommit{
		resultEventID:          uuid.New(),
		gameResultRevisionID:   uuid.New(),
		seriesResultRevisionID: uuid.New(),
		scoreRevisionID:        uuid.New(),
		auditEventID:           uuid.New(),
		outboxEventID:          uuid.New(),
		projectionEvidenceID:   uuid.New(),
		settledAt:              fixture.lockedAt.Add(5 * time.Second),
	}
	winnerID := fixture.draft.participantIDs[0]
	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(
		ctx, `
		INSERT INTO result_events (
			id, tournament_id, roster_id, series_id, attempt_id,
			submission_event_id, server_sequence, idempotency_key,
			result_state, result_reason, winner_id, occurred_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, 1, $7,
			'completed', 'solved', $8, $9, $9
		)`,
		commit.resultEventID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		submissionID,
		uuid.New(),
		winnerID,
		commit.settledAt,
	)
	require.NoError(tb, err)

	_, err = tx.Exec(
		ctx, `
		INSERT INTO official_result_revisions (
			id, tournament_id, roster_id, entity_kind, entity_id,
			series_id, game_attempt_id, result_event_id, revision_number,
			result_state, result_reason, winner_id, created_at
		)
		VALUES (
			$1, $2, $3, 'game_attempt', $4,
			$5, $4, $6, 1,
			'completed', 'solved', $7, $8
		)`,
		commit.gameResultRevisionID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.attemptID,
		fixture.draft.seriesID,
		commit.resultEventID,
		winnerID,
		commit.settledAt,
	)
	require.NoError(tb, err)

	_, err = tx.Exec(
		ctx, `
		INSERT INTO official_result_revisions (
			id, tournament_id, roster_id, entity_kind, entity_id,
			series_id, result_event_id, revision_number,
			result_state, result_reason, winner_id, created_at
		)
		VALUES (
			$1, $2, $3, 'series', $4,
			$4, $5, 1,
			'completed', 'score_complete', $6, $7
		)`,
		commit.seriesResultRevisionID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		commit.resultEventID,
		winnerID,
		commit.settledAt,
	)
	require.NoError(tb, err)

	_, err = tx.Exec(
		ctx, `
		INSERT INTO series_score_revisions (
			id, tournament_id, roster_id, series_id, result_event_id,
			previous_revision_id, revision_number,
			first_participant_wins, second_participant_wins, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 2, 1, 0, $7)`,
		commit.scoreRevisionID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		commit.resultEventID,
		fixture.initialScoreRevisionID,
		commit.settledAt,
	)
	require.NoError(tb, err)

	_, err = tx.Exec(
		ctx, `
		INSERT INTO audit_events (
			id, tournament_id, roster_id, series_id, result_event_id,
			actor_kind, action, payload, occurred_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			'server', 'tournament.result.committed',
			jsonb_build_object('reason', 'solved'), $6, $6
		)`,
		commit.auditEventID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		commit.resultEventID,
		commit.settledAt,
	)
	require.NoError(tb, err)

	_, err = tx.Exec(
		ctx, `
		INSERT INTO result_projection_evidence (
			id, tournament_id, roster_id, series_id, result_event_id,
			artifact_kinds, payload_digest, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			'["game_result","series_score","series_result"]'::JSONB,
			$6, $7
		)`,
		commit.projectionEvidenceID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		commit.resultEventID,
		bytes.Repeat([]byte{31}, 32),
		commit.settledAt,
	)
	require.NoError(tb, err)

	_, err = tx.Exec(
		ctx, `
		INSERT INTO outbox_events (
			id, tournament_id, roster_id, series_id, result_event_id,
			idempotency_key, topic, payload, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, 'tournament.result.committed',
			jsonb_build_object('result_event_id', $5::UUID::TEXT), $7
		)`,
		commit.outboxEventID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		commit.resultEventID,
		uuid.New(),
		commit.settledAt,
	)
	require.NoError(tb, err)

	_, err = tx.Exec(
		ctx, `
		INSERT INTO result_commits (
			tournament_id, roster_id, series_id, attempt_id, result_event_id,
			game_result_revision_id, series_score_revision_id,
			series_result_revision_id, audit_event_id, outbox_event_id,
			projection_evidence_id, idempotency_key, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7,
			$8, $9, $10,
			$11, $12, $13
		)`,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		commit.resultEventID,
		commit.gameResultRevisionID,
		commit.scoreRevisionID,
		commit.seriesResultRevisionID,
		commit.auditEventID,
		commit.outboxEventID,
		commit.projectionEvidenceID,
		uuid.New(),
		commit.settledAt,
	)
	require.NoError(tb, err)
	if !advanceCurrentHeads {
		return commit, tx.Commit(ctx)
	}

	_, err = tx.Exec(
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
		commit.settledAt,
	)
	require.NoError(tb, err)
	_, err = tx.Exec(
		ctx, `
		INSERT INTO official_result_heads (
			entity_kind, entity_id, series_id, roster_id,
			current_revision_id, updated_at
		)
		VALUES ('series', $1, $1, $2, $3, $4)`,
		fixture.draft.seriesID,
		fixture.draft.rosterID,
		commit.seriesResultRevisionID,
		commit.settledAt,
	)
	require.NoError(tb, err)
	_, err = tx.Exec(
		ctx, `
		UPDATE series_score_heads
		SET current_revision_id = $2,
			revision = revision + 1,
			updated_at = $3
		WHERE series_id = $1`,
		fixture.draft.seriesID,
		commit.scoreRevisionID,
		commit.settledAt,
	)
	require.NoError(tb, err)

	_, err = tx.Exec(
		ctx, `
		UPDATE game_attempts
		SET state = 'completed',
			result_reason = 'solved',
			winner_id = $2,
			result_revision_id = $3,
			revision = revision + 1,
			updated_at = $4,
			finished_at = $4
		WHERE id = $1`,
		fixture.attemptID,
		winnerID,
		commit.gameResultRevisionID,
		commit.settledAt,
	)
	require.NoError(tb, err)
	_, err = tx.Exec(
		ctx, `
		UPDATE series
		SET state = 'completed',
			first_participant_wins = 1,
			winner_id = $2,
			current_score_revision_id = $3,
			current_result_revision_id = $4,
			revision = revision + 1,
			updated_at = $5,
			started_at = $6,
			finished_at = $5
		WHERE id = $1`,
		fixture.draft.seriesID,
		winnerID,
		commit.scoreRevisionID,
		commit.seriesResultRevisionID,
		commit.settledAt,
		fixture.lockedAt,
	)
	require.NoError(tb, err)
	return commit, tx.Commit(ctx)
}
