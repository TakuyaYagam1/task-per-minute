//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func createProjectionGoldenSource(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
) uuid.UUID {
	tb.Helper()

	attemptID := createGoldenAttempt(ctx, tb, fixture, 1, nil, fixture.createdAt)
	readyAt := fixture.createdAt.Add(time.Second)
	membershipIDs := make([]uuid.UUID, 2)
	for index := range membershipIDs {
		membershipIDs[index] = createGoldenMembership(
			ctx, tb,
			fixture,
			attemptID,
			fixture.participantIDs[index],
			"direct",
			nil,
			fixture.createdAt,
			readyAt,
			nil,
			nil,
			nil,
		)
	}
	advanceGoldenAttemptToReady(
		ctx, tb,
		attemptID,
		fixture.createdAt.Add(2*time.Second),
		fixture.createdAt.Add(3*time.Second),
	)
	for _, membershipID := range membershipIDs {
		establishGoldenParticipation(
			ctx, tb, membershipID, fixture.createdAt.Add(4*time.Second),
		)
	}
	advanceGoldenAttemptToActive(
		ctx, tb, attemptID, fixture.createdAt.Add(5*time.Second),
	)
	submissionID := createGoldenSubmission(
		ctx, tb,
		fixture,
		attemptID,
		membershipIDs[0],
		fixture.participantIDs[0],
		1,
		1,
		"accepted",
		nil,
		fixture.createdAt.Add(6*time.Second),
	)
	return createGoldenPositionCommit(
		ctx, tb,
		fixture,
		attemptID,
		membershipIDs[0],
		fixture.participantIDs[0],
		submissionID,
		nil,
		1,
		fixture.createdAt.Add(7*time.Second),
	)
}

func createProjectionCutoff(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	sequenceNumber int,
	previousCutoffID any,
	sourceKind string,
	officialResultRevisionID any,
	goldenPositionCommitID any,
	reason string,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	id := uuid.New()
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO projection_cutoffs (
			id, tournament_id, roster_id, sequence_number, previous_cutoff_id,
			source_kind, official_result_revision_id, golden_position_commit_id,
			reason, cutoff_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
		id,
		fixture.tournamentID,
		fixture.rosterID,
		sequenceNumber,
		previousCutoffID,
		sourceKind,
		officialResultRevisionID,
		goldenPositionCommitID,
		reason,
		createdAt,
	)
	require.NoError(tb, err)
	return id
}

func createProjectionRevision(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	revisionNumber int,
	previousRevisionID any,
	cutoffID uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	id := uuid.New()
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO projection_revisions (
			id, tournament_id, roster_id, revision_number,
			previous_revision_id, cutoff_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id,
		fixture.tournamentID,
		fixture.rosterID,
		revisionNumber,
		previousRevisionID,
		cutoffID,
		createdAt,
	)
	require.NoError(tb, err)
	return id
}
