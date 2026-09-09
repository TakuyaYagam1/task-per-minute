//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func beginResultAuditLockProbe(ctx context.Context, tb testing.TB) pgx.Tx {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, "SET LOCAL lock_timeout = '100ms'")
	require.NoError(tb, err)
	return tx
}

func assertResultParticipantIntegrity(
	ctx context.Context, tb testing.TB,
	fixture resultAuditMigrationFixture,
	submissionID uuid.UUID,
) {
	tb.Helper()

	playerIDs := createMigrationPlayers(ctx, tb, 1)
	var outsideParticipantID uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO participants (roster_id, player_id, seed, attendance)
		VALUES ($1, $2, 3, 'checked_in')
		RETURNING id`, fixture.draft.rosterID, playerIDs[0]).Scan(&outsideParticipantID)
	require.NoError(tb, err)

	createdAt := fixture.lockedAt.Add(2 * time.Second)
	_, err = sharedPool.Exec(
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
		outsideParticipantID,
		uuid.New(),
		bytes.Repeat([]byte{23}, 32),
		createdAt,
	)
	require.ErrorContains(tb, err, "submission participant is outside the Series")

	_, err = sharedPool.Exec(
		ctx, `
		INSERT INTO result_events (
			tournament_id, roster_id, series_id, attempt_id,
			server_sequence, idempotency_key, result_state,
			result_reason, winner_id, occurred_at, created_at
		)
		VALUES (
			$1, $2, $3, $4,
			1, $5, 'completed',
			'operator_forfeit', $6, $7, $7
		)`,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		uuid.New(),
		outsideParticipantID,
		createdAt,
	)
	require.ErrorContains(tb, err, "result winner is outside the Series")

	_, err = sharedPool.Exec(
		ctx, `
		INSERT INTO result_events (
			tournament_id, roster_id, series_id, attempt_id,
			submission_event_id, server_sequence, idempotency_key,
			result_state, result_reason, winner_id, occurred_at, created_at
		)
		VALUES (
			$1, $2, $3, $4,
			$5, 1, $6,
			'completed', 'solved', $7, $8, $8
		)`,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		submissionID,
		uuid.New(),
		fixture.draft.participantIDs[1],
		createdAt,
	)
	require.ErrorContains(
		tb,
		err,
		"solved result winner must match the submission participant",
	)

	_, err = sharedPool.Exec(ctx, `
		UPDATE series
		SET first_participant_id = $2
		WHERE id = $1`, fixture.draft.seriesID, outsideParticipantID)
	require.ErrorContains(tb, err, "Series result identity is immutable")
}

func createResultAuditMigrationFixture(
	ctx context.Context, tb testing.TB,
) resultAuditMigrationFixture {
	tb.Helper()

	draft := createDraftMigrationFixture(ctx, tb)
	createdAt := draft.createdAt.Add(5 * time.Second)
	conservativePlanID := createConservativeAssignmentPlan(ctx, tb, draft, createdAt)
	exactPlanID := createExactAssignmentPlan(
		ctx, tb,
		draft,
		conservativePlanID,
		createdAt.Add(time.Second),
	)
	branchID := createAssignmentBranch(
		ctx, tb,
		exactPlanID,
		draft,
		"web-final",
		`["web"]`,
		createdAt.Add(2*time.Second),
	)
	releasedBranchID := createAssignmentBranch(
		ctx, tb,
		exactPlanID,
		draft,
		"web-fallback",
		`["web"]`,
		createdAt.Add(2*time.Second),
	)
	reservations := createAssignmentBranchReservations(
		ctx, tb,
		exactPlanID,
		branchID,
		"web",
		createdAt.Add(3*time.Second),
	)
	releasedReservations := createAssignmentBranchReservations(
		ctx, tb,
		exactPlanID,
		releasedBranchID,
		"web",
		createdAt.Add(3*time.Second),
	)
	committedAt := createdAt.Add(4 * time.Second)
	for _, reservation := range reservations {
		_, err := sharedPool.Exec(ctx, `
			UPDATE task_version_reservations
			SET state = 'committed',
				revision = revision + 1,
				committed_at = $2
			WHERE id = $1`, reservation.reservationID, committedAt)
		require.NoError(tb, err)
	}
	for _, reservation := range releasedReservations {
		_, err := sharedPool.Exec(ctx, `
			UPDATE task_version_reservations
			SET state = 'released',
				revision = revision + 1,
				released_at = $2,
				release_reason = 'unused fixture branch'
			WHERE id = $1`, reservation.reservationID, committedAt)
		require.NoError(tb, err)
	}

	_, err := sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'active', activated_at = $2
		WHERE id = $1`, branchID, committedAt.Add(time.Second))
	require.NoError(tb, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_branches
		SET state = 'released',
			released_at = $2,
			release_reason = 'unused fixture branch'
		WHERE id = $1`, releasedBranchID, committedAt.Add(time.Second))
	require.NoError(tb, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE assignment_plans
		SET state = 'committed', active_branch_id = $2, committed_at = $3
		WHERE id = $1`, exactPlanID, branchID, committedAt.Add(2*time.Second))
	require.NoError(tb, err)

	slotID := createMigrationGameSlot(ctx, tb, draft.seriesID, draft.rosterID, 1, "web")
	attemptID := createActiveMigrationAttempt(
		ctx, tb,
		slotID,
		draft.seriesID,
		draft.rosterID,
		committedAt.Add(3*time.Second),
	)
	assignmentID := createActiveMigrationAssignment(
		ctx, tb,
		attemptID,
		draft,
		exactPlanID,
		branchID,
		reservations[0],
		nil,
		committedAt.Add(4*time.Second),
	)

	lockedAt := time.Now().UTC().Add(5 * time.Second).Truncate(time.Microsecond)
	initialScoreRevisionID := lockMigrationSeries(ctx, tb, draft, lockedAt)

	return resultAuditMigrationFixture{
		draft:                  draft,
		attemptID:              attemptID,
		assignmentID:           assignmentID,
		initialScoreRevisionID: initialScoreRevisionID,
		lockedAt:               lockedAt,
	}
}

func lockMigrationSeries(
	ctx context.Context, tb testing.TB,
	draft draftMigrationFixture,
	lockedAt time.Time,
) uuid.UUID {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var initialScoreRevisionID uuid.UUID
	var revisionNumber int64
	err = tx.QueryRow(ctx, `
		SELECT score_head.current_revision_id, score_revision.revision_number
		FROM series
		INNER JOIN series_score_heads AS score_head
			ON score_head.series_id = series.id
			AND score_head.roster_id = series.roster_id
		INNER JOIN series_score_revisions AS score_revision
			ON score_revision.id = score_head.current_revision_id
			AND score_revision.series_id = series.id
			AND score_revision.roster_id = series.roster_id
		WHERE series.id = $1
			AND series.tournament_id = $2
			AND series.roster_id = $3
			AND series.state = 'planned'
			AND series.current_score_revision_id = score_head.current_revision_id
			AND score_revision.revision_number = 1
			AND score_revision.operation = 'initialize'
			AND score_revision.result_event_id IS NULL
			AND score_revision.previous_revision_id IS NULL
			AND score_revision.command_attempt_id IS NULL
			AND score_revision.first_participant_wins = 0
			AND score_revision.second_participant_wins = 0
		FOR UPDATE OF series, score_head`,
		draft.seriesID,
		draft.tournamentID,
		draft.rosterID,
	).Scan(&initialScoreRevisionID, &revisionNumber)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, revisionNumber)

	_, err = tx.Exec(ctx, `
		UPDATE series
		SET state = 'locked',
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, draft.seriesID, lockedAt)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
	return initialScoreRevisionID
}

func createAcceptedSubmission(
	ctx context.Context, tb testing.TB,
	fixture resultAuditMigrationFixture,
) (uuid.UUID, uuid.UUID) {
	tb.Helper()

	submissionID := uuid.New()
	idempotencyKey := uuid.New()
	receivedAt := fixture.lockedAt.Add(time.Second)
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO submission_events (
			id, tournament_id, roster_id, series_id, attempt_id, assignment_id,
			participant_id, server_sequence, idempotency_key, status,
			payload_digest, submitted_at, received_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, 1, $8, 'accepted',
			$9, $10, $10, $10
		)`,
		submissionID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		fixture.assignmentID,
		fixture.draft.participantIDs[0],
		idempotencyKey,
		bytes.Repeat([]byte{20}, 32),
		receivedAt,
	)
	require.NoError(tb, err)
	return submissionID, idempotencyKey
}

func createAtomicResultCommit(
	ctx context.Context, tb testing.TB,
	fixture resultAuditMigrationFixture,
	submissionID uuid.UUID,
) resultAuditCommit {
	tb.Helper()

	commit, err := createResultCommit(ctx, tb, fixture, submissionID, true)
	require.NoError(tb, err)
	return commit
}

func assertResultCommitRequiresCurrentHeads(
	ctx context.Context, tb testing.TB,
	fixture resultAuditMigrationFixture,
	submissionID uuid.UUID,
) {
	tb.Helper()

	_, err := createResultCommit(ctx, tb, fixture, submissionID, false)
	require.ErrorContains(tb, err, "result commit revisions must be current heads")
}
