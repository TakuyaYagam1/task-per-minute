//go:build integration

package result

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type resultAuditMigrationFixture struct {
	draft                  draftMigrationFixture
	attemptID              uuid.UUID
	assignmentID           uuid.UUID
	initialScoreRevisionID uuid.UUID
	lockedAt               time.Time
}

type resultAuditCommit struct {
	resultEventID          uuid.UUID
	gameResultRevisionID   uuid.UUID
	seriesResultRevisionID uuid.UUID
	scoreRevisionID        uuid.UUID
	auditEventID           uuid.UUID
	outboxEventID          uuid.UUID
	projectionEvidenceID   uuid.UUID
	settledAt              time.Time
}

func runResultAuditMigration(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createResultAuditMigrationFixture(ctx, t)
	submissionID, submissionKey := createAcceptedSubmission(ctx, t, fixture)
	assertResultParticipantIntegrity(ctx, t, fixture, submissionID)
	assertResultCommitRequiresCurrentHeads(ctx, t, fixture, submissionID)

	_, err := migrationPool.Exec(
		ctx, `
		INSERT INTO submission_events (
			tournament_id, roster_id, series_id, attempt_id, assignment_id,
			participant_id, server_sequence, idempotency_key, status,
			payload_digest, submitted_at, received_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, 2, $7, 'accepted',
			$8, $9, $9, $9
		)`,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		fixture.assignmentID,
		fixture.draft.participantIDs[0],
		submissionKey,
		bytes.Repeat([]byte{21}, 32),
		fixture.lockedAt.Add(time.Second),
	)
	require.Error(t, err)

	_, err = migrationPool.Exec(
		ctx, `
		INSERT INTO submission_events (
			tournament_id, roster_id, series_id, attempt_id, assignment_id,
			participant_id, server_sequence, idempotency_key, status,
			payload_digest, submitted_at, received_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, 3, $7, 'accepted',
			$8, $9, $9, $9
		)`,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		fixture.assignmentID,
		fixture.draft.participantIDs[0],
		uuid.New(),
		bytes.Repeat([]byte{22}, 32),
		fixture.lockedAt.Add(time.Second),
	)
	require.Error(t, err)

	commit := createAtomicResultCommit(ctx, t, fixture, submissionID)

	var (
		gameHeadCount   int
		seriesHeadCount int
		scoreHeadCount  int
		commitCount     int
	)
	err = migrationPool.QueryRow(
		ctx, `
		SELECT
			COUNT(*) FILTER (WHERE entity_kind = 'game_attempt'),
			COUNT(*) FILTER (WHERE entity_kind = 'series')
		FROM official_result_heads
		WHERE entity_id IN ($1, $2)`,
		fixture.attemptID,
		fixture.draft.seriesID,
	).Scan(&gameHeadCount, &seriesHeadCount)
	require.NoError(t, err)
	require.Equal(t, 1, gameHeadCount)
	require.Equal(t, 1, seriesHeadCount)

	err = migrationPool.QueryRow(
		ctx, `
		SELECT COUNT(*)
		FROM series_score_heads
		WHERE series_id = $1 AND current_revision_id = $2`,
		fixture.draft.seriesID,
		commit.scoreRevisionID,
	).Scan(&scoreHeadCount)
	require.NoError(t, err)
	require.Equal(t, 1, scoreHeadCount)

	err = migrationPool.QueryRow(
		ctx, `
		SELECT COUNT(*)
		FROM result_commits
		WHERE result_event_id = $1
			AND game_result_revision_id = $2
			AND series_score_revision_id = $3
			AND series_result_revision_id = $4
			AND audit_event_id = $5
			AND outbox_event_id = $6
			AND projection_evidence_id = $7`,
		commit.resultEventID,
		commit.gameResultRevisionID,
		commit.scoreRevisionID,
		commit.seriesResultRevisionID,
		commit.auditEventID,
		commit.outboxEventID,
		commit.projectionEvidenceID,
	).Scan(&commitCount)
	require.NoError(t, err)
	require.Equal(t, 1, commitCount)

	assertResultEvidenceIsImmutable(ctx, t, fixture, commit)
	assertResultEventCannotCommitPartially(ctx, t, fixture)
	assertAuditRejectsNestedFlag(ctx, t, fixture)
	assertOutboxRetainsPublishedEvidence(ctx, t, commit)
}

func runResultAuditMigrationCommitLocks(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createResultAuditMigrationFixture(ctx, t)
	submissionID, _ := createAcceptedSubmission(ctx, t, fixture)
	commit := createAtomicResultCommit(ctx, t, fixture, submissionID)

	t.Run("official revision locks Game before commit seal check", func(t *testing.T) {
		lockTx, err := migrationPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM game_attempts
			WHERE id = $1
			FOR NO KEY UPDATE`, fixture.attemptID)
		require.NoError(t, err)

		probeTx := beginResultAuditLockProbe(ctx, t)
		defer func() { _ = probeTx.Rollback(ctx) }()
		_, err = probeTx.Exec(
			ctx, `
			INSERT INTO official_result_revisions (
				id, tournament_id, roster_id, entity_kind, entity_id,
				series_id, game_attempt_id, result_event_id,
				previous_revision_id, revision_number,
				result_state, result_reason, winner_id, created_at
			)
			VALUES (
				$1, $2, $3, 'game_attempt', $4,
				$5, $4, $6,
				$7, 2,
				'completed', 'solved', $8, $9
			)`,
			uuid.New(),
			fixture.draft.tournamentID,
			fixture.draft.rosterID,
			fixture.attemptID,
			fixture.draft.seriesID,
			commit.resultEventID,
			commit.gameResultRevisionID,
			fixture.draft.participantIDs[0],
			commit.settledAt.Add(time.Second),
		)
		require.ErrorContains(t, err, "lock timeout")
	})

	t.Run("score revision locks Series before commit seal check", func(t *testing.T) {
		lockTx, err := migrationPool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = lockTx.Rollback(ctx) }()
		_, err = lockTx.Exec(ctx, `
			SELECT 1
			FROM series
			WHERE id = $1
			FOR NO KEY UPDATE`, fixture.draft.seriesID)
		require.NoError(t, err)

		probeTx := beginResultAuditLockProbe(ctx, t)
		defer func() { _ = probeTx.Rollback(ctx) }()
		_, err = probeTx.Exec(
			ctx, `
			INSERT INTO series_score_revisions (
				id, tournament_id, roster_id, series_id, result_event_id,
				previous_revision_id, revision_number,
				first_participant_wins, second_participant_wins, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, 3, 1, 0, $7)`,
			uuid.New(),
			fixture.draft.tournamentID,
			fixture.draft.rosterID,
			fixture.draft.seriesID,
			commit.resultEventID,
			commit.scoreRevisionID,
			commit.settledAt.Add(time.Second),
		)
		require.ErrorContains(t, err, "lock timeout")
	})
}
