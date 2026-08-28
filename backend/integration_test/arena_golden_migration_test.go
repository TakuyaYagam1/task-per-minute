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

type arenaGoldenMigrationFixture struct {
	tournamentID   uuid.UUID
	rosterID       uuid.UUID
	participantIDs []uuid.UUID
	createdAt      time.Time
}

func TestArenaGoldenMigration(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	fixture := createArenaGoldenMigrationFixture(t, ctx, 5)
	firstAttemptID := createArenaGoldenAttempt(t, ctx, fixture, 1, nil, fixture.createdAt)
	selectedAt := fixture.createdAt.Add(time.Second)
	readyAt := fixture.createdAt.Add(2 * time.Second)
	noShowAt := fixture.createdAt.Add(3 * time.Second)

	firstMembershipID := createArenaGoldenMembership(
		t, ctx, fixture, firstAttemptID, fixture.participantIDs[0], "direct",
		nil, selectedAt, readyAt, nil, nil, nil,
	)
	secondMembershipID := createArenaGoldenMembership(
		t, ctx, fixture, firstAttemptID, fixture.participantIDs[1], "direct",
		nil, selectedAt, readyAt, nil, nil, nil,
	)
	noShowMembershipID := createArenaGoldenMembership(
		t, ctx, fixture, firstAttemptID, fixture.participantIDs[2], "direct",
		nil, selectedAt, nil, noShowAt, nil, nil,
	)
	reserveMembershipID := createArenaGoldenMembership(
		t, ctx, fixture, firstAttemptID, fixture.participantIDs[3], "reserve",
		1, selectedAt, readyAt, nil, nil, nil,
	)
	excludedAt := fixture.createdAt.Add(1500 * time.Millisecond)
	excludedMembershipID := createArenaGoldenMembership(
		t, ctx, fixture, firstAttemptID, fixture.participantIDs[4], "excluded",
		nil, selectedAt, nil, nil, excludedAt, "preflight exclusion",
	)

	promotedAt := fixture.createdAt.Add(4 * time.Second)
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_golden_reserve_promotions (
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
	advanceArenaGoldenAttemptToReady(t, ctx, firstAttemptID, disclosedAt, readyStateAt)

	disconnectedAt := fixture.createdAt.Add(7 * time.Second)
	disconnectID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_golden_ready_disconnects (
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
	establishArenaGoldenParticipation(t, ctx, firstMembershipID, participationAt)
	establishArenaGoldenParticipation(t, ctx, secondMembershipID, participationAt)
	establishArenaGoldenParticipation(t, ctx, reserveMembershipID, participationAt)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_golden_attempts
		SET state = 'active', started_at = $2
		WHERE id = $1`, firstAttemptID, participationAt.Add(time.Second))
	require.ErrorContains(t, err, "without open disconnects")

	reconnectedAt := fixture.createdAt.Add(9 * time.Second)
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_golden_ready_disconnects
		SET state = 'reconnected', reconnected_at = $2
		WHERE id = $1`, disconnectID, reconnectedAt)
	require.NoError(t, err)

	startedAt := fixture.createdAt.Add(10 * time.Second)
	advanceArenaGoldenAttemptToActive(t, ctx, firstAttemptID, startedAt)

	var retainedReadyAt time.Time
	err = sharedPool.QueryRow(ctx, `
		SELECT ready_at
		FROM arena_golden_memberships
		WHERE id = $1`, secondMembershipID).Scan(&retainedReadyAt)
	require.NoError(t, err)
	require.Equal(t, readyAt, retainedReadyAt)

	firstSubmissionID := createArenaGoldenSubmission(
		t, ctx, fixture, firstAttemptID, firstMembershipID,
		fixture.participantIDs[0], 1, 1, "accepted", nil, startedAt.Add(time.Second),
	)
	secondSubmissionID := createArenaGoldenSubmission(
		t, ctx, fixture, firstAttemptID, secondMembershipID,
		fixture.participantIDs[1], 2, 2, "accepted", nil, startedAt.Add(2*time.Second),
	)
	reserveSubmissionID := createArenaGoldenSubmission(
		t, ctx, fixture, firstAttemptID, reserveMembershipID,
		fixture.participantIDs[3], 3, 3, "accepted", nil, startedAt.Add(3*time.Second),
	)
	rejectedSubmissionID := createArenaGoldenSubmission(
		t, ctx, fixture, firstAttemptID, firstMembershipID,
		fixture.participantIDs[0], 4, 4, "rejected", "late duplicate", startedAt.Add(4*time.Second),
	)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_golden_position_commits (
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

	firstPositionCommitID := createArenaGoldenPositionCommit(
		t, ctx, fixture, firstAttemptID, firstMembershipID,
		fixture.participantIDs[0], firstSubmissionID, nil, 1, startedAt.Add(5*time.Second),
	)
	createArenaGoldenPositionCommit(
		t, ctx, fixture, firstAttemptID, secondMembershipID,
		fixture.participantIDs[1], secondSubmissionID, nil, 2, startedAt.Add(6*time.Second),
	)
	createArenaGoldenPositionCommit(
		t, ctx, fixture, firstAttemptID, reserveMembershipID,
		fixture.participantIDs[3], reserveSubmissionID, nil, 3, startedAt.Add(7*time.Second),
	)

	completedAt := startedAt.Add(8 * time.Second)
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_golden_attempts
		SET state = 'completed', completed_at = $2
		WHERE id = $1`, firstAttemptID, completedAt)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_golden_provisional_submissions
		SET provisional_position = 2
		WHERE id = $1`, firstSubmissionID)
	require.ErrorContains(t, err, "immutable evidence")

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_golden_memberships
		SET participation_established_at = NULL
		WHERE id = $1`, firstMembershipID)
	require.ErrorContains(t, err, "closed after start")

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_golden_memberships
		SET exclusion_reason = 'rewritten'
		WHERE id = $1`, excludedMembershipID)
	require.ErrorContains(t, err, "closed after start")

	secondAttemptAt := fixture.createdAt.Add(time.Minute)
	secondAttemptID := createArenaGoldenAttempt(
		t, ctx, fixture, 2, firstAttemptID, secondAttemptAt,
	)
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_golden_attempts
		SET state = 'superseded',
			superseded_at = $2,
			supersession_reason = 'retained undisclosed fallback'
		WHERE id = $1`, secondAttemptID, secondAttemptAt.Add(time.Second))
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `DELETE FROM arena_golden_attempts WHERE id = $1`, secondAttemptID)
	require.ErrorContains(t, err, "retained stage history")

	thirdAttemptAt := fixture.createdAt.Add(2 * time.Minute)
	thirdAttemptID := createArenaGoldenAttempt(
		t, ctx, fixture, 3, secondAttemptID, thirdAttemptAt,
	)
	thirdReadyAt := thirdAttemptAt.Add(time.Second)
	thirdFirstMembershipID := createArenaGoldenMembership(
		t, ctx, fixture, thirdAttemptID, fixture.participantIDs[0], "direct",
		nil, thirdAttemptAt, thirdReadyAt, nil, nil, nil,
	)
	thirdSecondMembershipID := createArenaGoldenMembership(
		t, ctx, fixture, thirdAttemptID, fixture.participantIDs[1], "direct",
		nil, thirdAttemptAt, thirdReadyAt, nil, nil, nil,
	)
	thirdReserveMembershipID := createArenaGoldenMembership(
		t, ctx, fixture, thirdAttemptID, fixture.participantIDs[3], "reserve",
		1, thirdAttemptAt, thirdReadyAt, nil, nil, nil,
	)
	advanceArenaGoldenAttemptToReady(
		t,
		ctx,
		thirdAttemptID,
		thirdAttemptAt.Add(2*time.Second),
		thirdAttemptAt.Add(3*time.Second),
	)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_golden_memberships
		SET participation_established_at = $2
		WHERE id = $1`, thirdReserveMembershipID, thirdAttemptAt.Add(4*time.Second))
	require.ErrorContains(t, err, "requires promotion evidence")

	establishArenaGoldenParticipation(
		t, ctx, thirdFirstMembershipID, thirdAttemptAt.Add(4*time.Second),
	)
	establishArenaGoldenParticipation(
		t, ctx, thirdSecondMembershipID, thirdAttemptAt.Add(4*time.Second),
	)
	advanceArenaGoldenAttemptToActive(
		t, ctx, thirdAttemptID, thirdAttemptAt.Add(5*time.Second),
	)

	thirdSubmissionID := createArenaGoldenSubmission(
		t, ctx, fixture, thirdAttemptID, thirdFirstMembershipID,
		fixture.participantIDs[0], 1, 2, "accepted", nil, thirdAttemptAt.Add(6*time.Second),
	)
	thirdPositionCommitID := createArenaGoldenPositionCommit(
		t, ctx, fixture, thirdAttemptID, thirdFirstMembershipID,
		fixture.participantIDs[0], thirdSubmissionID, firstPositionCommitID,
		2, thirdAttemptAt.Add(7*time.Second),
	)

	var previousPositionCommitID uuid.UUID
	err = sharedPool.QueryRow(ctx, `
		SELECT previous_position_commit_id
		FROM arena_golden_position_commits
		WHERE id = $1`, thirdPositionCommitID).Scan(&previousPositionCommitID)
	require.NoError(t, err)
	require.Equal(t, firstPositionCommitID, previousPositionCommitID)

	assertArenaGoldenRecoveryRevisionChain(
		t,
		ctx,
		fixture,
		thirdAttemptID,
		thirdAttemptAt.Add(8*time.Second),
	)
}

func createArenaGoldenMigrationFixture(
	t testing.TB,
	ctx context.Context,
	participantCount int,
) arenaGoldenMigrationFixture {
	t.Helper()

	tournamentID := createArenaMigrationTournament(t, ctx)
	rosterID := createArenaMigrationRoster(t, ctx, tournamentID)
	playerIDs := createArenaMigrationPlayers(t, ctx, participantCount)
	participantIDs := createSwissMigrationParticipants(t, ctx, rosterID, playerIDs)

	return arenaGoldenMigrationFixture{
		tournamentID:   tournamentID,
		rosterID:       rosterID,
		participantIDs: participantIDs,
		createdAt:      time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Microsecond),
	}
}

func createArenaGoldenAttempt(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	attemptNumber int,
	previousAttemptID any,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_golden_attempts (
			id, tournament_id, roster_id, attempt_number,
			previous_attempt_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		id,
		fixture.tournamentID,
		fixture.rosterID,
		attemptNumber,
		previousAttemptID,
		createdAt,
	)
	require.NoError(t, err)
	return id
}

func createArenaGoldenMembership(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	attemptID uuid.UUID,
	participantID uuid.UUID,
	selectionKind string,
	reservePosition any,
	selectedAt time.Time,
	readyAt any,
	noShowAt any,
	excludedAt any,
	exclusionReason any,
) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_golden_memberships (
			id, attempt_id, tournament_id, roster_id, participant_id,
			selection_kind, reserve_position, selected_at,
			ready_at, no_show_at, excluded_at, exclusion_reason
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $11, $12
		)`,
		id,
		attemptID,
		fixture.tournamentID,
		fixture.rosterID,
		participantID,
		selectionKind,
		reservePosition,
		selectedAt,
		readyAt,
		noShowAt,
		excludedAt,
		exclusionReason,
	)
	require.NoError(t, err)
	return id
}

func advanceArenaGoldenAttemptToReady(
	t testing.TB,
	ctx context.Context,
	attemptID uuid.UUID,
	disclosedAt time.Time,
	readyAt time.Time,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		UPDATE arena_golden_attempts
		SET state = 'ready', disclosed_at = $2, ready_at = $3
		WHERE id = $1`, attemptID, disclosedAt, readyAt)
	require.NoError(t, err)
}

func establishArenaGoldenParticipation(
	t testing.TB,
	ctx context.Context,
	membershipID uuid.UUID,
	establishedAt time.Time,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		UPDATE arena_golden_memberships
		SET participation_established_at = $2
		WHERE id = $1`, membershipID, establishedAt)
	require.NoError(t, err)
}

func advanceArenaGoldenAttemptToActive(
	t testing.TB,
	ctx context.Context,
	attemptID uuid.UUID,
	startedAt time.Time,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		UPDATE arena_golden_attempts
		SET state = 'active', started_at = $2
		WHERE id = $1`, attemptID, startedAt)
	require.NoError(t, err)
}

func createArenaGoldenSubmission(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	attemptID uuid.UUID,
	membershipID uuid.UUID,
	participantID uuid.UUID,
	serverSequence int,
	position int,
	status string,
	rejectionReason any,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_golden_provisional_submissions (
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
	require.NoError(t, err)
	return id
}

func createArenaGoldenPositionCommit(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	attemptID uuid.UUID,
	membershipID uuid.UUID,
	participantID uuid.UUID,
	submissionID uuid.UUID,
	previousPositionCommitID any,
	position int,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_golden_position_commits (
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
	require.NoError(t, err)
	return id
}

func assertArenaGoldenRecoveryRevisionChain(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	attemptID uuid.UUID,
	createdAt time.Time,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		UPDATE arena_golden_attempts
		SET state = 'technical_pause', paused_at = $2
		WHERE id = $1`, attemptID, createdAt)
	require.ErrorContains(t, err, "requires current recovery evidence")

	states := []string{"stable", "technical_pause", "recovering", "resumed"}
	var previousRevisionID any
	var lastRevisionID uuid.UUID
	for index, state := range states {
		lastRevisionID = uuid.New()
		recordedAt := createdAt.Add(time.Duration(index+1) * time.Second)
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO arena_golden_recovery_revisions (
				id, attempt_id, tournament_id, roster_id,
				revision_number, previous_revision_id, state,
				recovery_evidence, recorded_at, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::JSONB, $9, $9)`,
			lastRevisionID,
			attemptID,
			fixture.tournamentID,
			fixture.rosterID,
			index+1,
			previousRevisionID,
			state,
			`{"source":"synthetic migration test"}`,
			recordedAt,
		)
		require.NoError(t, err)
		previousRevisionID = lastRevisionID

		if state == "technical_pause" {
			_, err = sharedPool.Exec(ctx, `
				UPDATE arena_golden_attempts
				SET state = 'technical_pause', paused_at = $2
				WHERE id = $1`, attemptID, recordedAt)
			require.NoError(t, err)
		} else if state == "resumed" {
			_, err = sharedPool.Exec(ctx, `
				UPDATE arena_golden_attempts
				SET state = 'active', paused_at = NULL
				WHERE id = $1`, attemptID)
			require.NoError(t, err)
		}
	}

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_golden_recovery_revisions
		SET recovery_evidence = '{"source":"rewritten"}'::JSONB
		WHERE id = $1`, lastRevisionID)
	require.ErrorContains(t, err, "immutable evidence")
}
