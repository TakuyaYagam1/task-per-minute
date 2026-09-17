//go:build integration

package draft

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type draftMigrationFixture struct {
	tournamentID         uuid.UUID
	rosterID             uuid.UUID
	seriesID             uuid.UUID
	draftID              uuid.UUID
	categoryRevisionID   uuid.UUID
	normalPoolRevisionID uuid.UUID
	initialRevisionID    uuid.UUID
	participantIDs       []uuid.UUID
	initialServiceEpoch  uuid.UUID
	createdAt            time.Time
}

func runDraftMigration(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	defer resetMigrationTables(ctx, t)

	fixture := createDraftMigrationFixture(ctx, t)
	assertDraftTurnIdentityRejected(ctx, t, fixture)

	pausedRevisionID := uuid.New()
	pausedCommandID := uuid.New()
	_, err := migrationPool.Exec(
		ctx, `
		INSERT INTO draft_revisions (
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
	_, err = migrationPool.Exec(
		ctx, `
		INSERT INTO draft_revisions (
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
	_, err = migrationPool.Exec(
		ctx, `
		INSERT INTO draft_revisions (
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
	commitDraftActionRevision(
		ctx, t,
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

	_, err = migrationPool.Exec(
		ctx, `
		INSERT INTO draft_revisions (
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

	assertIllegalDraftActionRejected(
		ctx, t,
		fixture,
		secondTurnRevisionID,
		recoveryEpoch,
		secondDeadline,
	)

	completedRevisionID := uuid.New()
	completedCommandID := uuid.New()
	commitCompletedDraft(
		ctx, t,
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
	err = migrationPool.QueryRow(ctx, `
		SELECT revision.state, revision.turn_number, revision.service_epoch,
			(SELECT COUNT(*) FROM draft_actions AS action WHERE action.draft_id = revision.draft_id)
		FROM draft_revisions AS revision
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

	_, err = migrationPool.Exec(ctx, `
		UPDATE draft_revisions
		SET selected_categories = '["crypto"]'::JSONB
		WHERE id = $1`, completedRevisionID)
	require.Error(t, err)

	_, err = migrationPool.Exec(
		ctx, `
		INSERT INTO draft_revisions (
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
