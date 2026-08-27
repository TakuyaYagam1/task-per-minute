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

type arenaDraftMigrationFixture struct {
	tournamentID        uuid.UUID
	rosterID            uuid.UUID
	seriesID            uuid.UUID
	draftID             uuid.UUID
	categoryRevisionID  uuid.UUID
	initialRevisionID   uuid.UUID
	participantIDs      []uuid.UUID
	initialServiceEpoch uuid.UUID
	createdAt           time.Time
}

func TestArenaDraftMigration(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	fixture := createArenaDraftMigrationFixture(t, ctx)
	pausedRevisionID := uuid.New()
	pausedCommandID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_draft_revisions (
			id, draft_id, series_id, roster_id, revision,
			previous_revision_id, command_id, service_epoch,
			state, turn_number, current_actor_id, current_action,
			paused_remaining_ms, recovery_reason, recovery_evidence,
			created_at
		)
		VALUES (
			$1, $2, $3, $4, 2,
			$5, $6, $7,
			'paused', 1, $8, 'ban',
			8000, 'operator_pause', '{"reason":"operator"}'::JSONB,
			$9
		)`,
		pausedRevisionID,
		fixture.draftID,
		fixture.seriesID,
		fixture.rosterID,
		fixture.initialRevisionID,
		pausedCommandID,
		fixture.initialServiceEpoch,
		fixture.participantIDs[0],
		fixture.createdAt.Add(time.Second),
	)
	require.NoError(t, err)

	recoveryRevisionID := uuid.New()
	recoveryEpoch := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_draft_revisions (
			id, draft_id, series_id, roster_id, revision,
			previous_revision_id, command_id, service_epoch,
			state, turn_number, current_actor_id, current_action,
			recovery_reason, recovery_evidence, created_at
		)
		VALUES (
			$1, $2, $3, $4, 3,
			$5, $6, $7,
			'recovery_required', 1, $8, 'ban',
			'epoch_mismatch', '{"route":"draft_recovery"}'::JSONB, $9
		)`,
		recoveryRevisionID,
		fixture.draftID,
		fixture.seriesID,
		fixture.rosterID,
		pausedRevisionID,
		uuid.New(),
		recoveryEpoch,
		fixture.participantIDs[0],
		fixture.createdAt.Add(2*time.Second),
	)
	require.NoError(t, err)

	resumedRevisionID := uuid.New()
	resumedDeadline := fixture.createdAt.Add(20 * time.Second)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_draft_revisions (
			id, draft_id, series_id, roster_id, revision,
			previous_revision_id, command_id, service_epoch,
			state, turn_number, current_actor_id, current_action,
			absolute_deadline, created_at
		)
		VALUES (
			$1, $2, $3, $4, 4,
			$5, $6, $7,
			'active', 1, $8, 'ban',
			$9, $10
		)`,
		resumedRevisionID,
		fixture.draftID,
		fixture.seriesID,
		fixture.rosterID,
		recoveryRevisionID,
		uuid.New(),
		recoveryEpoch,
		fixture.participantIDs[0],
		resumedDeadline,
		fixture.createdAt.Add(3*time.Second),
	)
	require.NoError(t, err)

	secondTurnRevisionID := uuid.New()
	secondTurnCommandID := uuid.New()
	secondDeadline := fixture.createdAt.Add(35 * time.Second)
	commitArenaDraftActionRevision(
		t,
		ctx,
		fixture,
		secondTurnRevisionID,
		resumedRevisionID,
		secondTurnCommandID,
		recoveryEpoch,
		5,
		"active",
		2,
		fixture.participantIDs[1],
		"ban",
		secondDeadline,
		1,
		fixture.participantIDs[0],
		"ban",
		"web",
		resumedDeadline,
		false,
	)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_draft_revisions (
			draft_id, series_id, roster_id, revision,
			previous_revision_id, command_id, service_epoch,
			state, turn_number, current_actor_id, current_action,
			absolute_deadline, created_at
		)
		VALUES (
			$1, $2, $3, 6,
			$4, $5, $6,
			'active', 2, $7, 'ban',
			$8, $9
		)`,
		fixture.draftID,
		fixture.seriesID,
		fixture.rosterID,
		resumedRevisionID,
		uuid.New(),
		recoveryEpoch,
		fixture.participantIDs[1],
		secondDeadline,
		fixture.createdAt.Add(5*time.Second),
	)
	require.Error(t, err)

	assertIllegalArenaDraftActionRejected(
		t,
		ctx,
		fixture,
		secondTurnRevisionID,
		recoveryEpoch,
		secondDeadline,
	)

	completedRevisionID := uuid.New()
	completedCommandID := uuid.New()
	commitCompletedArenaDraft(
		t,
		ctx,
		fixture,
		completedRevisionID,
		secondTurnRevisionID,
		completedCommandID,
		recoveryEpoch,
		secondDeadline,
	)

	var (
		state              string
		storedTurn         int
		storedActionCount  int
		storedServiceEpoch uuid.UUID
	)
	err = sharedPool.QueryRow(ctx, `
		SELECT revision.state, revision.turn_number, revision.service_epoch,
			(SELECT COUNT(*) FROM arena_draft_actions AS action WHERE action.draft_id = revision.draft_id)
		FROM arena_draft_revisions AS revision
		WHERE revision.id = $1`, completedRevisionID).Scan(
		&state,
		&storedTurn,
		&storedServiceEpoch,
		&storedActionCount,
	)
	require.NoError(t, err)
	require.Equal(t, "completed", state)
	require.Equal(t, 2, storedTurn)
	require.Equal(t, recoveryEpoch, storedServiceEpoch)
	require.Equal(t, 2, storedActionCount)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_draft_revisions
		SET selected_categories = '["crypto"]'::JSONB
		WHERE id = $1`, completedRevisionID)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_draft_revisions (
			draft_id, series_id, roster_id, revision,
			previous_revision_id, command_id, service_epoch,
			state, turn_number, current_actor_id, current_action,
			absolute_deadline, created_at
		)
		VALUES (
			$1, $2, $3, 7,
			$4, $5, $6,
			'active', 2, $7, 'ban',
			$8, $9
		)`,
		fixture.draftID,
		fixture.seriesID,
		fixture.rosterID,
		completedRevisionID,
		uuid.New(),
		recoveryEpoch,
		fixture.participantIDs[1],
		secondDeadline,
		fixture.createdAt.Add(7*time.Second),
	)
	require.Error(t, err)
}

func createArenaDraftMigrationFixture(
	t testing.TB,
	ctx context.Context,
) arenaDraftMigrationFixture {
	t.Helper()

	tournamentID := createArenaMigrationTournament(t, ctx)
	rosterID := createArenaMigrationRoster(t, ctx, tournamentID)
	playerIDs := createArenaMigrationPlayers(t, ctx, 2)
	participantIDs := createSwissMigrationParticipants(t, ctx, rosterID, playerIDs)
	seriesID := createArenaMigrationSeries(t, ctx, tournamentID, rosterID, participantIDs, "bo1")
	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)

	categoryRevisionID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_category_revisions (
			id, series_id, roster_id, revision, source_pool_revision_id,
			mode, category_pool, created_at
		)
		VALUES (
			$1, $2, $3, 1, $4,
			'draft', '["web","crypto","pwn"]'::JSONB, $5
		)`,
		categoryRevisionID,
		seriesID,
		rosterID,
		uuid.New(),
		createdAt,
	)
	require.NoError(t, err)

	draftID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_drafts (
			id, series_id, roster_id, category_revision_id,
			first_participant_id, second_participant_id, format, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'bo1', $7)`,
		draftID,
		seriesID,
		rosterID,
		categoryRevisionID,
		participantIDs[0],
		participantIDs[1],
		createdAt,
	)
	require.NoError(t, err)

	initialRevisionID := uuid.New()
	initialServiceEpoch := uuid.New()
	seed := bytes.Repeat([]byte{1}, 32)
	digest := bytes.Repeat([]byte{2}, 32)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_draft_revisions (
			id, draft_id, series_id, roster_id, revision,
			command_id, service_epoch, state, turn_number,
			current_actor_id, current_action, absolute_deadline,
			decision_evidence_id, decision_purpose,
			decision_algorithm_version, decision_inputs,
			decision_seed, decision_result, decision_replay_digest,
			decision_owner_id, decided_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, 1,
			$5, $6, 'active', 1,
			$7, 'ban', $8,
			$9, 'draft_order',
			'hmac-sha256-order-v1', $10::JSONB,
			$11, $12::JSONB, $13,
			$2, $14, $14
		)`,
		initialRevisionID,
		draftID,
		seriesID,
		rosterID,
		uuid.New(),
		initialServiceEpoch,
		participantIDs[0],
		createdAt.Add(15*time.Second),
		uuid.New(),
		`["first","second"]`,
		seed,
		`["first","second"]`,
		digest,
		createdAt,
	)
	require.NoError(t, err)

	return arenaDraftMigrationFixture{
		tournamentID:        tournamentID,
		rosterID:            rosterID,
		seriesID:            seriesID,
		draftID:             draftID,
		categoryRevisionID:  categoryRevisionID,
		initialRevisionID:   initialRevisionID,
		participantIDs:      participantIDs,
		initialServiceEpoch: initialServiceEpoch,
		createdAt:           createdAt,
	}
}

func commitArenaDraftActionRevision(
	t testing.TB,
	ctx context.Context,
	fixture arenaDraftMigrationFixture,
	revisionID uuid.UUID,
	previousRevisionID uuid.UUID,
	commandID uuid.UUID,
	serviceEpoch uuid.UUID,
	revision int,
	state string,
	turn int,
	currentActorID any,
	currentAction any,
	absoluteDeadline any,
	actionTurn int,
	actionActorID uuid.UUID,
	action string,
	category string,
	scheduledDeadline time.Time,
	automatic bool,
) {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO arena_draft_revisions (
			id, draft_id, series_id, roster_id, revision,
			previous_revision_id, command_id, service_epoch,
			state, turn_number, current_actor_id, current_action,
			absolute_deadline, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $11, $12,
			$13, $14
		)`,
		revisionID,
		fixture.draftID,
		fixture.seriesID,
		fixture.rosterID,
		revision,
		previousRevisionID,
		commandID,
		serviceEpoch,
		state,
		turn,
		currentActorID,
		currentAction,
		absoluteDeadline,
		scheduledDeadline,
	)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO arena_draft_actions (
			draft_id, result_revision_id, command_id,
			turn_number, actor_id, action, category,
			scheduled_deadline, occurred_at, automatic, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $9)`,
		fixture.draftID,
		revisionID,
		commandID,
		actionTurn,
		actionActorID,
		action,
		category,
		scheduledDeadline,
		scheduledDeadline.Add(-time.Second),
		automatic,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func assertIllegalArenaDraftActionRejected(
	t testing.TB,
	ctx context.Context,
	fixture arenaDraftMigrationFixture,
	previousRevisionID uuid.UUID,
	serviceEpoch uuid.UUID,
	deadline time.Time,
) {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	revisionID := uuid.New()
	commandID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_draft_revisions (
			id, draft_id, series_id, roster_id, revision,
			previous_revision_id, command_id, service_epoch,
			state, turn_number, selected_categories, created_at
		)
		VALUES (
			$1, $2, $3, $4, 6,
			$5, $6, $7,
			'completed', 2, '["pwn"]'::JSONB, $8
		)`,
		revisionID,
		fixture.draftID,
		fixture.seriesID,
		fixture.rosterID,
		previousRevisionID,
		commandID,
		serviceEpoch,
		deadline,
	)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO arena_draft_actions (
			draft_id, result_revision_id, command_id,
			turn_number, actor_id, action, category,
			scheduled_deadline, occurred_at, created_at
		)
		VALUES ($1, $2, $3, 2, $4, 'ban', 'crypto', $5, $6, $6)`,
		fixture.draftID,
		revisionID,
		commandID,
		fixture.participantIDs[0],
		deadline,
		deadline.Add(-time.Second),
	)
	require.Error(t, err)
}

func commitCompletedArenaDraft(
	t testing.TB,
	ctx context.Context,
	fixture arenaDraftMigrationFixture,
	revisionID uuid.UUID,
	previousRevisionID uuid.UUID,
	commandID uuid.UUID,
	serviceEpoch uuid.UUID,
	deadline time.Time,
) {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	seed := bytes.Repeat([]byte{3}, 32)
	digest := bytes.Repeat([]byte{4}, 32)
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_draft_revisions (
			id, draft_id, series_id, roster_id, revision,
			previous_revision_id, command_id, service_epoch,
			state, turn_number, selected_categories,
			decision_evidence_id, decision_purpose,
			decision_algorithm_version, decision_inputs,
			decision_seed, decision_result, decision_replay_digest,
			decision_owner_id, decided_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, 6,
			$5, $6, $7,
			'completed', 2, '["pwn"]'::JSONB,
			$8, 'category',
			'hmac-sha256-order-v1', '["crypto","pwn"]'::JSONB,
			$9, '["crypto","pwn"]'::JSONB, $10,
			$2, $11, $12
		)`,
		revisionID,
		fixture.draftID,
		fixture.seriesID,
		fixture.rosterID,
		previousRevisionID,
		commandID,
		serviceEpoch,
		uuid.New(),
		seed,
		digest,
		deadline.Add(-time.Second),
		deadline,
	)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO arena_draft_actions (
			draft_id, result_revision_id, command_id,
			turn_number, actor_id, action, category,
			scheduled_deadline, occurred_at, automatic, created_at
		)
		VALUES ($1, $2, $3, 2, $4, 'ban', 'crypto', $5, $6, TRUE, $6)`,
		fixture.draftID,
		revisionID,
		commandID,
		fixture.participantIDs[1],
		deadline,
		deadline.Add(-time.Second),
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}
