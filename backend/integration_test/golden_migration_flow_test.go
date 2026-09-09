//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type goldenMigrationFixture struct {
	tournamentID   uuid.UUID
	rosterID       uuid.UUID
	participantIDs []uuid.UUID
	createdAt      time.Time
}

func TestGoldenMigration(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createGoldenMigrationFixture(ctx, t, 5)
	firstAttemptID := createGoldenAttempt(ctx, t, fixture, 1, nil, fixture.createdAt)
	selectedAt := fixture.createdAt.Add(time.Second)
	readyAt := fixture.createdAt.Add(2 * time.Second)
	noShowAt := fixture.createdAt.Add(3 * time.Second)

	firstMembershipID := createGoldenMembership(
		ctx, t, fixture, firstAttemptID, fixture.participantIDs[0], "direct",
		nil, selectedAt, readyAt, nil, nil, nil,
	)
	secondMembershipID := createGoldenMembership(
		ctx, t, fixture, firstAttemptID, fixture.participantIDs[1], "direct",
		nil, selectedAt, readyAt, nil, nil, nil,
	)
	noShowMembershipID := createGoldenMembership(
		ctx, t, fixture, firstAttemptID, fixture.participantIDs[2], "direct",
		nil, selectedAt, nil, noShowAt, nil, nil,
	)
	reserveMembershipID := createGoldenMembership(
		ctx, t, fixture, firstAttemptID, fixture.participantIDs[3], "reserve",
		1, selectedAt, readyAt, nil, nil, nil,
	)
	excludedAt := fixture.createdAt.Add(1500 * time.Millisecond)
	excludedMembershipID := createGoldenMembership(
		ctx, t, fixture, firstAttemptID, fixture.participantIDs[4], "excluded",
		nil, selectedAt, nil, nil, excludedAt, "preflight exclusion",
	)

	promotedAt := fixture.createdAt.Add(4 * time.Second)
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO golden_reserve_promotions (
			attempt_id, tournament_id, roster_id,
			reserve_membership_id, reserve_participant_id,
			replaced_membership_id, replaced_participant_id,
			reason, promoted_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		firstAttemptID,
		fixture.tournamentID,
		fixture.rosterID,
		reserveMembershipID,
		fixture.participantIDs[3],
		noShowMembershipID,
		fixture.participantIDs[2],
		"replace direct no-show",
		promotedAt,
	)
	require.NoError(t, err)

	disclosedAt := fixture.createdAt.Add(5 * time.Second)
	readyStateAt := fixture.createdAt.Add(6 * time.Second)
	advanceGoldenAttemptToReady(ctx, t, firstAttemptID, disclosedAt, readyStateAt)

	disconnectedAt := fixture.createdAt.Add(7 * time.Second)
	disconnectID := uuid.New()
	_, err = sharedPool.Exec(
		ctx, `
		INSERT INTO golden_ready_disconnects (
			id, membership_id, attempt_id, tournament_id, roster_id,
			participant_id, sequence_number, disconnected_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 1, $7, $7)`,
		disconnectID,
		secondMembershipID,
		firstAttemptID,
		fixture.tournamentID,
		fixture.rosterID,
		fixture.participantIDs[1],
		disconnectedAt,
	)
	require.NoError(t, err)

	participationAt := fixture.createdAt.Add(8 * time.Second)
	establishGoldenParticipation(ctx, t, firstMembershipID, participationAt)
	establishGoldenParticipation(ctx, t, secondMembershipID, participationAt)
	establishGoldenParticipation(ctx, t, reserveMembershipID, participationAt)

	_, err = sharedPool.Exec(ctx, `
		UPDATE golden_attempts
		SET state = 'active', started_at = $2
		WHERE id = $1`, firstAttemptID, participationAt.Add(time.Second))
	require.ErrorContains(t, err, "without open disconnects")

	reconnectedAt := fixture.createdAt.Add(9 * time.Second)
	_, err = sharedPool.Exec(ctx, `
		UPDATE golden_ready_disconnects
		SET state = 'reconnected', reconnected_at = $2
		WHERE id = $1`, disconnectID, reconnectedAt)
	require.NoError(t, err)

	startedAt := fixture.createdAt.Add(10 * time.Second)
	advanceGoldenAttemptToActive(ctx, t, firstAttemptID, startedAt)

	var retainedReadyAt time.Time
	err = sharedPool.QueryRow(ctx, `
		SELECT ready_at
		FROM golden_memberships
		WHERE id = $1`, secondMembershipID).Scan(&retainedReadyAt)
	require.NoError(t, err)
	require.WithinDuration(t, readyAt, retainedReadyAt, 0)

	firstSubmissionID := createGoldenSubmission(
		ctx, t, fixture, firstAttemptID, firstMembershipID,
		fixture.participantIDs[0], 1, 1, "accepted", nil, startedAt.Add(time.Second),
	)
	secondSubmissionID := createGoldenSubmission(
		ctx, t, fixture, firstAttemptID, secondMembershipID,
		fixture.participantIDs[1], 2, 2, "accepted", nil, startedAt.Add(2*time.Second),
	)
	reserveSubmissionID := createGoldenSubmission(
		ctx, t, fixture, firstAttemptID, reserveMembershipID,
		fixture.participantIDs[3], 3, 3, "accepted", nil, startedAt.Add(3*time.Second),
	)
	rejectedSubmissionID := createGoldenSubmission(
		ctx, t, fixture, firstAttemptID, firstMembershipID,
		fixture.participantIDs[0], 4, 4, "rejected", "late duplicate", startedAt.Add(4*time.Second),
	)

	_, err = sharedPool.Exec(
		ctx, `
		INSERT INTO golden_position_commits (
			attempt_id, tournament_id, roster_id, membership_id, participant_id,
			provisional_submission_id, position, committed_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 4, $7, $7)`,
		firstAttemptID,
		fixture.tournamentID,
		fixture.rosterID,
		firstMembershipID,
		fixture.participantIDs[0],
		rejectedSubmissionID,
		startedAt.Add(5*time.Second),
	)
	require.ErrorContains(t, err, "accepted active submission")

	firstPositionCommitID := createGoldenPositionCommit(
		ctx, t, fixture, firstAttemptID, firstMembershipID,
		fixture.participantIDs[0], firstSubmissionID, nil, 1, startedAt.Add(5*time.Second),
	)
	createGoldenPositionCommit(
		ctx, t, fixture, firstAttemptID, secondMembershipID,
		fixture.participantIDs[1], secondSubmissionID, nil, 2, startedAt.Add(6*time.Second),
	)
	createGoldenPositionCommit(
		ctx, t, fixture, firstAttemptID, reserveMembershipID,
		fixture.participantIDs[3], reserveSubmissionID, nil, 3, startedAt.Add(7*time.Second),
	)

	completedAt := startedAt.Add(8 * time.Second)
	_, err = sharedPool.Exec(ctx, `
		UPDATE golden_attempts
		SET state = 'completed', completed_at = $2
		WHERE id = $1`, firstAttemptID, completedAt)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE golden_provisional_submissions
		SET provisional_position = 2
		WHERE id = $1`, firstSubmissionID)
	require.ErrorContains(t, err, "immutable evidence")

	_, err = sharedPool.Exec(ctx, `
		UPDATE golden_memberships
		SET participation_established_at = NULL
		WHERE id = $1`, firstMembershipID)
	require.ErrorContains(t, err, "closed after start")

	_, err = sharedPool.Exec(ctx, `
		UPDATE golden_memberships
		SET exclusion_reason = 'rewritten'
		WHERE id = $1`, excludedMembershipID)
	require.ErrorContains(t, err, "closed after start")

	secondAttemptAt := fixture.createdAt.Add(time.Minute)
	secondAttemptID := createGoldenAttempt(
		ctx, t, fixture, 2, firstAttemptID, secondAttemptAt,
	)
	_, err = sharedPool.Exec(ctx, `
		UPDATE golden_attempts
		SET state = 'superseded',
			superseded_at = $2,
			supersession_reason = 'retained undisclosed fallback'
		WHERE id = $1`, secondAttemptID, secondAttemptAt.Add(time.Second))
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `DELETE FROM golden_attempts WHERE id = $1`, secondAttemptID)
	require.ErrorContains(t, err, "retained stage history")

	thirdAttemptAt := fixture.createdAt.Add(2 * time.Minute)
	thirdAttemptID := createGoldenAttempt(
		ctx, t, fixture, 3, secondAttemptID, thirdAttemptAt,
	)
	thirdReadyAt := thirdAttemptAt.Add(time.Second)
	thirdFirstMembershipID := createGoldenMembership(
		ctx, t, fixture, thirdAttemptID, fixture.participantIDs[0], "direct",
		nil, thirdAttemptAt, thirdReadyAt, nil, nil, nil,
	)
	thirdSecondMembershipID := createGoldenMembership(
		ctx, t, fixture, thirdAttemptID, fixture.participantIDs[1], "direct",
		nil, thirdAttemptAt, thirdReadyAt, nil, nil, nil,
	)
	thirdReserveMembershipID := createGoldenMembership(
		ctx, t, fixture, thirdAttemptID, fixture.participantIDs[3], "reserve",
		1, thirdAttemptAt, thirdReadyAt, nil, nil, nil,
	)
	advanceGoldenAttemptToReady(
		ctx, t,
		thirdAttemptID,
		thirdAttemptAt.Add(2*time.Second),
		thirdAttemptAt.Add(3*time.Second),
	)

	_, err = sharedPool.Exec(ctx, `
		UPDATE golden_memberships
		SET participation_established_at = $2
		WHERE id = $1`, thirdReserveMembershipID, thirdAttemptAt.Add(4*time.Second))
	require.ErrorContains(t, err, "requires promotion evidence")

	establishGoldenParticipation(
		ctx, t, thirdFirstMembershipID, thirdAttemptAt.Add(4*time.Second),
	)
	establishGoldenParticipation(
		ctx, t, thirdSecondMembershipID, thirdAttemptAt.Add(4*time.Second),
	)
	advanceGoldenAttemptToActive(
		ctx, t, thirdAttemptID, thirdAttemptAt.Add(5*time.Second),
	)

	thirdSubmissionID := createGoldenSubmission(
		ctx, t, fixture, thirdAttemptID, thirdFirstMembershipID,
		fixture.participantIDs[0], 1, 2, "accepted", nil, thirdAttemptAt.Add(6*time.Second),
	)
	thirdPositionCommitID := createGoldenPositionCommit(
		ctx, t, fixture, thirdAttemptID, thirdFirstMembershipID,
		fixture.participantIDs[0], thirdSubmissionID, firstPositionCommitID,
		2, thirdAttemptAt.Add(7*time.Second),
	)

	var previousPositionCommitID uuid.UUID
	err = sharedPool.QueryRow(ctx, `
		SELECT previous_position_commit_id
		FROM golden_position_commits
		WHERE id = $1`, thirdPositionCommitID).Scan(&previousPositionCommitID)
	require.NoError(t, err)
	require.Equal(t, firstPositionCommitID, previousPositionCommitID)

	assertGoldenRecoveryRevisionChain(
		ctx, t,
		fixture,
		thirdAttemptID,
		thirdAttemptAt.Add(8*time.Second),
	)
}
