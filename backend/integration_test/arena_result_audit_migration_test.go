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

type arenaResultAuditMigrationFixture struct {
	draft                  arenaDraftMigrationFixture
	attemptID              uuid.UUID
	assignmentID           uuid.UUID
	initialScoreRevisionID uuid.UUID
	lockedAt               time.Time
}

type arenaResultAuditCommit struct {
	resultEventID          uuid.UUID
	gameResultRevisionID   uuid.UUID
	seriesResultRevisionID uuid.UUID
	scoreRevisionID        uuid.UUID
	auditEventID           uuid.UUID
	outboxEventID          uuid.UUID
	projectionEvidenceID   uuid.UUID
	settledAt              time.Time
}

func TestArenaResultAuditMigration(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	fixture := createArenaResultAuditMigrationFixture(t, ctx)
	submissionID, submissionKey := createAcceptedArenaSubmission(t, ctx, fixture)
	assertArenaResultParticipantIntegrity(t, ctx, fixture, submissionID)

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_submission_events (
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

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_submission_events (
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

	commit := createAtomicArenaResultCommit(t, ctx, fixture, submissionID)

	var (
		gameHeadCount   int
		seriesHeadCount int
		scoreHeadCount  int
		commitCount     int
	)
	err = sharedPool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE entity_kind = 'game_attempt'),
			COUNT(*) FILTER (WHERE entity_kind = 'series')
		FROM arena_official_result_heads
		WHERE entity_id IN ($1, $2)`,
		fixture.attemptID,
		fixture.draft.seriesID,
	).Scan(&gameHeadCount, &seriesHeadCount)
	require.NoError(t, err)
	require.Equal(t, 1, gameHeadCount)
	require.Equal(t, 1, seriesHeadCount)

	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_series_score_heads
		WHERE series_id = $1 AND current_revision_id = $2`,
		fixture.draft.seriesID,
		commit.scoreRevisionID,
	).Scan(&scoreHeadCount)
	require.NoError(t, err)
	require.Equal(t, 1, scoreHeadCount)

	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_result_commits
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

	assertArenaResultEvidenceIsImmutable(t, ctx, fixture, commit)
	assertArenaResultEventCannotCommitPartially(t, ctx, fixture)
	assertArenaAuditRejectsNestedFlag(t, ctx, fixture)
	assertArenaOutboxRetainsPublishedEvidence(t, ctx, commit)
}

func assertArenaResultParticipantIntegrity(
	t testing.TB,
	ctx context.Context,
	fixture arenaResultAuditMigrationFixture,
	submissionID uuid.UUID,
) {
	t.Helper()

	playerIDs := createArenaMigrationPlayers(t, ctx, 1)
	var outsideParticipantID uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO arena_participants (roster_id, player_id, seed, attendance)
		VALUES ($1, $2, 3, 'checked_in')
		RETURNING id`, fixture.draft.rosterID, playerIDs[0]).Scan(&outsideParticipantID)
	require.NoError(t, err)

	createdAt := fixture.lockedAt.Add(2 * time.Second)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_submission_events (
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
	require.ErrorContains(t, err, "Arena submission participant is outside the Series")

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_result_events (
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
	require.ErrorContains(t, err, "Arena result winner is outside the Series")

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_result_events (
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
		t,
		err,
		"Arena solved result winner must match the submission participant",
	)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_series
		SET first_participant_id = $2
		WHERE id = $1`, fixture.draft.seriesID, outsideParticipantID)
	require.ErrorContains(t, err, "Arena Series result identity is immutable")
}

func createArenaResultAuditMigrationFixture(
	t testing.TB,
	ctx context.Context,
) arenaResultAuditMigrationFixture {
	t.Helper()

	draft := createArenaDraftMigrationFixture(t, ctx)
	createdAt := draft.createdAt.Add(5 * time.Second)
	conservativePlanID := createConservativeArenaAssignmentPlan(t, ctx, draft, createdAt)
	exactPlanID := createExactArenaAssignmentPlan(
		t,
		ctx,
		draft,
		conservativePlanID,
		createdAt.Add(time.Second),
	)
	branchID := createArenaAssignmentBranch(
		t,
		ctx,
		exactPlanID,
		draft,
		"web-final",
		`["web"]`,
		createdAt.Add(2*time.Second),
	)
	reservations := createArenaAssignmentBranchReservations(
		t,
		ctx,
		exactPlanID,
		branchID,
		"web",
		createdAt.Add(3*time.Second),
	)
	committedAt := createdAt.Add(4 * time.Second)
	for _, reservation := range reservations {
		_, err := sharedPool.Exec(ctx, `
			UPDATE arena_task_version_reservations
			SET state = 'committed', committed_at = $2
			WHERE id = $1`, reservation.reservationID, committedAt)
		require.NoError(t, err)
	}

	_, err := sharedPool.Exec(ctx, `
		UPDATE arena_assignment_branches
		SET state = 'active', activated_at = $2
		WHERE id = $1`, branchID, committedAt.Add(time.Second))
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_assignment_plans
		SET state = 'committed', active_branch_id = $2, committed_at = $3
		WHERE id = $1`, exactPlanID, branchID, committedAt.Add(2*time.Second))
	require.NoError(t, err)

	slotID := createArenaMigrationGameSlot(t, ctx, draft.seriesID, draft.rosterID, 1, "web")
	attemptID := createActiveArenaMigrationAttempt(
		t,
		ctx,
		slotID,
		draft.seriesID,
		draft.rosterID,
		committedAt.Add(3*time.Second),
	)
	assignmentID := createActiveArenaMigrationAssignment(
		t,
		ctx,
		attemptID,
		draft,
		exactPlanID,
		branchID,
		reservations[0],
		nil,
		committedAt.Add(4*time.Second),
	)

	lockedAt := time.Now().UTC().Add(5 * time.Second).Truncate(time.Microsecond)
	initialScoreRevisionID := lockArenaMigrationSeries(t, ctx, draft, lockedAt)

	return arenaResultAuditMigrationFixture{
		draft:                  draft,
		attemptID:              attemptID,
		assignmentID:           assignmentID,
		initialScoreRevisionID: initialScoreRevisionID,
		lockedAt:               lockedAt,
	}
}

func lockArenaMigrationSeries(
	t testing.TB,
	ctx context.Context,
	draft arenaDraftMigrationFixture,
	lockedAt time.Time,
) uuid.UUID {
	t.Helper()

	initialScoreRevisionID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_series_score_revisions (
			id, tournament_id, roster_id, series_id,
			revision_number, first_participant_wins, second_participant_wins,
			created_at
		)
		VALUES ($1, $2, $3, $4, 1, 0, 0, $5)`,
		initialScoreRevisionID,
		draft.tournamentID,
		draft.rosterID,
		draft.seriesID,
		lockedAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_series_score_heads (
			series_id, roster_id, current_revision_id, updated_at
		)
		VALUES ($1, $2, $3, $4)`,
		draft.seriesID,
		draft.rosterID,
		initialScoreRevisionID,
		lockedAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE arena_series
		SET state = 'locked',
			current_score_revision_id = $2,
			revision = revision + 1,
			updated_at = $3
		WHERE id = $1`, draft.seriesID, initialScoreRevisionID, lockedAt)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return initialScoreRevisionID
}

func createAcceptedArenaSubmission(
	t testing.TB,
	ctx context.Context,
	fixture arenaResultAuditMigrationFixture,
) (uuid.UUID, uuid.UUID) {
	t.Helper()

	submissionID := uuid.New()
	idempotencyKey := uuid.New()
	receivedAt := fixture.lockedAt.Add(time.Second)
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_submission_events (
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
	require.NoError(t, err)
	return submissionID, idempotencyKey
}

func createAtomicArenaResultCommit(
	t testing.TB,
	ctx context.Context,
	fixture arenaResultAuditMigrationFixture,
	submissionID uuid.UUID,
) arenaResultAuditCommit {
	t.Helper()

	commit := arenaResultAuditCommit{
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
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO arena_result_events (
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
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO arena_official_result_revisions (
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
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO arena_official_result_revisions (
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
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO arena_series_score_revisions (
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
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO arena_audit_events (
			id, tournament_id, roster_id, series_id, result_event_id,
			actor_kind, action, payload, occurred_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			'server', 'arena.result.committed',
			jsonb_build_object('reason', 'solved'), $6, $6
		)`,
		commit.auditEventID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		commit.resultEventID,
		commit.settledAt,
	)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO arena_result_projection_evidence (
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
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO arena_outbox_events (
			id, tournament_id, roster_id, series_id, result_event_id,
			idempotency_key, topic, payload, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, 'arena.result.committed',
			jsonb_build_object('result_event_id', $5::TEXT), $7
		)`,
		commit.outboxEventID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		commit.resultEventID,
		uuid.New(),
		commit.settledAt,
	)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO arena_result_commits (
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
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO arena_official_result_heads (
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
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_official_result_heads (
			entity_kind, entity_id, series_id, roster_id,
			current_revision_id, updated_at
		)
		VALUES ('series', $1, $1, $2, $3, $4)`,
		fixture.draft.seriesID,
		fixture.draft.rosterID,
		commit.seriesResultRevisionID,
		commit.settledAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE arena_series_score_heads
		SET current_revision_id = $2,
			revision = revision + 1,
			updated_at = $3
		WHERE series_id = $1`,
		fixture.draft.seriesID,
		commit.scoreRevisionID,
		commit.settledAt,
	)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		UPDATE arena_game_attempts
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
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE arena_series
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
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return commit
}

func assertArenaResultEvidenceIsImmutable(
	t testing.TB,
	ctx context.Context,
	fixture arenaResultAuditMigrationFixture,
	commit arenaResultAuditCommit,
) {
	t.Helper()

	_, err := sharedPool.Exec(ctx, `
		UPDATE arena_official_result_revisions
		SET result_reason = 'operator_forfeit'
		WHERE id = $1`, commit.gameResultRevisionID)
	require.Error(t, err)
	_, err = sharedPool.Exec(ctx, `
		DELETE FROM arena_result_events WHERE id = $1`, commit.resultEventID)
	require.Error(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_audit_events
		SET payload = '{"reason":"changed"}'::JSONB
		WHERE id = $1`, commit.auditEventID)
	require.Error(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_official_result_heads (
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
	require.Error(t, err)
}

func assertArenaResultEventCannotCommitPartially(
	t testing.TB,
	ctx context.Context,
	fixture arenaResultAuditMigrationFixture,
) {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_result_events (
			tournament_id, roster_id, series_id, attempt_id,
			server_sequence, idempotency_key, result_state,
			result_reason, occurred_at, created_at
		)
		VALUES (
			$1, $2, $3, $4,
			2, $5, 'superseded',
			'derived_revision_superseded', $6, $6
		)`,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		uuid.New(),
		fixture.lockedAt.Add(6*time.Second),
	)
	require.NoError(t, err)
	require.Error(t, tx.Commit(ctx))
}

func assertArenaAuditRejectsNestedFlag(
	t testing.TB,
	ctx context.Context,
	fixture arenaResultAuditMigrationFixture,
) {
	t.Helper()

	resultEventID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	createdAt := fixture.lockedAt.Add(7 * time.Second)
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_result_events (
			id, tournament_id, roster_id, series_id, attempt_id,
			server_sequence, idempotency_key, result_state,
			result_reason, occurred_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			2, $6, 'superseded',
			'derived_revision_superseded', $7, $7
		)`,
		resultEventID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		uuid.New(),
		createdAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_audit_events (
			tournament_id, roster_id, series_id, result_event_id,
			actor_kind, action, payload, occurred_at, created_at
		)
		VALUES (
			$1, $2, $3, $4,
			'server', 'arena.result.rejected',
			'{"details":{"flag":"FLAG{secret}"}}'::JSONB, $5, $5
		)`,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		resultEventID,
		createdAt,
	)
	require.Error(t, err)
}

func assertArenaOutboxRetainsPublishedEvidence(
	t testing.TB,
	ctx context.Context,
	commit arenaResultAuditCommit,
) {
	t.Helper()

	publishedAt := commit.settledAt.Add(time.Second)
	_, err := sharedPool.Exec(ctx, `
		UPDATE arena_outbox_events
		SET published_at = $2
		WHERE id = $1`, commit.outboxEventID, publishedAt)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_outbox_events
		SET payload = '{"changed":true}'::JSONB
		WHERE id = $1`, commit.outboxEventID)
	require.Error(t, err)
	_, err = sharedPool.Exec(ctx, `
		DELETE FROM arena_outbox_events WHERE id = $1`, commit.outboxEventID)
	require.Error(t, err)
}
