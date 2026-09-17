//go:build integration

package golden

import (
	"context"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/goldenseed"
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
	_, err := migrationPool.Exec(ctx, `
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
	_, err := migrationPool.Exec(ctx, `
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
	_, err := migrationPool.Exec(ctx, `
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

	id, err := goldenseed.CreateSubmission(ctx, migrationPool, goldenseed.SubmissionInput{
		AttemptID:       attemptID,
		TournamentID:    fixture.tournamentID,
		RosterID:        fixture.rosterID,
		MembershipID:    membershipID,
		ParticipantID:   participantID,
		ServerSequence:  serverSequence,
		Position:        position,
		Status:          status,
		RejectionReason: rejectionReason,
		CreatedAt:       createdAt,
	})
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

	id, err := goldenseed.CreatePositionCommit(ctx, migrationPool, goldenseed.PositionCommitInput{
		AttemptID:                attemptID,
		TournamentID:             fixture.tournamentID,
		RosterID:                 fixture.rosterID,
		MembershipID:             membershipID,
		ParticipantID:            participantID,
		SubmissionID:             submissionID,
		PreviousPositionCommitID: previousPositionCommitID,
		Position:                 position,
		CreatedAt:                createdAt,
	})
	require.NoError(tb, err)
	return id
}
