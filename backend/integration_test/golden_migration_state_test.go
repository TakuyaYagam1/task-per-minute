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

func advanceGoldenAttemptToReady(
	ctx context.Context, tb testing.TB,
	attemptID uuid.UUID,
	disclosedAt time.Time,
	readyAt time.Time,
) {
	tb.Helper()
	_, err := sharedPool.Exec(ctx, `
		UPDATE golden_attempts
		SET state = 'ready', disclosed_at = $2, ready_at = $3
		WHERE id = $1`, attemptID, disclosedAt, readyAt)
	require.NoError(tb, err)
}

func establishGoldenParticipation(
	ctx context.Context, tb testing.TB,
	membershipID uuid.UUID,
	establishedAt time.Time,
) {
	tb.Helper()
	_, err := sharedPool.Exec(ctx, `
		UPDATE golden_memberships
		SET participation_established_at = $2
		WHERE id = $1`, membershipID, establishedAt)
	require.NoError(tb, err)
}

func advanceGoldenAttemptToActive(
	ctx context.Context, tb testing.TB,
	attemptID uuid.UUID,
	startedAt time.Time,
) {
	tb.Helper()
	_, err := sharedPool.Exec(ctx, `
		UPDATE golden_attempts
		SET state = 'active', started_at = $2
		WHERE id = $1`, attemptID, startedAt)
	require.NoError(tb, err)
}

func createGoldenSubmission(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	attemptID uuid.UUID,
	membershipID uuid.UUID,
	participantID uuid.UUID,
	serverSequence int,
	position int,
	status string,
	rejectionReason any,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	id := uuid.New()
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO golden_provisional_submissions (
			id, attempt_id, tournament_id, roster_id, membership_id, participant_id,
			server_sequence, idempotency_key, provisional_position,
			elapsed_milliseconds, status, rejection_reason, payload_digest,
			submitted_at, received_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9,
			1000, $10, $11, $12,
			$13, $13, $13
		)`,
		id,
		attemptID,
		fixture.tournamentID,
		fixture.rosterID,
		membershipID,
		participantID,
		serverSequence,
		uuid.New(),
		position,
		status,
		rejectionReason,
		bytes.Repeat([]byte{byte(serverSequence + 1)}, 32),
		createdAt,
	)
	require.NoError(tb, err)
	return id
}

func createGoldenPositionCommit(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	attemptID uuid.UUID,
	membershipID uuid.UUID,
	participantID uuid.UUID,
	submissionID uuid.UUID,
	previousPositionCommitID any,
	position int,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	id := uuid.New()
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO golden_position_commits (
			id, attempt_id, tournament_id, roster_id, membership_id, participant_id,
			provisional_submission_id, previous_position_commit_id,
			position, committed_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
		id,
		attemptID,
		fixture.tournamentID,
		fixture.rosterID,
		membershipID,
		participantID,
		submissionID,
		previousPositionCommitID,
		position,
		createdAt,
	)
	require.NoError(tb, err)
	return id
}
