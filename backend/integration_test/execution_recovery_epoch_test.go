//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func TestExecutionRecoveryEpochEvidenceIsImmutable(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createResultAuditMigrationFixture(ctx, t)
	boundAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	waveID, _ := createMigrationWave(
		ctx,
		t,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.participantIDs,
		boundAt,
	)
	var slotID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(
		ctx,
		"SELECT slot_id FROM game_attempts WHERE id = $1",
		fixture.attemptID,
	).Scan(&slotID))

	holderID := uuid.New()
	leaseID := uuid.New()
	currentHolderID := uuid.New()
	currentLeaseID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO execution_authority_leases (
			tournament_id, command_id, holder_id, lease_id, epoch,
			process_kind, revision, acquired_at, renewed_at, expires_at, created_at
		)
		VALUES ($1, $2, $3, $4, 1, 'authority', 1, $5, $5, $6, $5)`,
		fixture.draft.tournamentID,
		uuid.New(),
		holderID,
		leaseID,
		boundAt,
		boundAt.Add(time.Second),
	)
	require.NoError(t, err)
	currentAt := boundAt.Add(time.Second)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO execution_authority_leases (
			tournament_id, command_id, holder_id, lease_id, epoch,
			process_kind, revision, previous_revision, previous_lease_id, previous_epoch,
			acquired_at, renewed_at, expires_at, created_at
		)
		VALUES ($1, $2, $3, $4, 2, 'authority', 2, 1, $5, 1, $6, $6, $7, $6)`,
		fixture.draft.tournamentID,
		uuid.New(),
		currentHolderID,
		currentLeaseID,
		leaseID,
		currentAt,
		currentAt.Add(time.Minute),
	)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO execution_game_epochs (
			game_attempt_id, tournament_id, roster_id, wave_id, series_id, slot_id,
			authority_holder_id, authority_lease_id, authority_epoch, authority_revision,
			bound_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, 1, $9, $9)`,
		fixture.attemptID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		waveID,
		fixture.draft.seriesID,
		slotID,
		holderID,
		leaseID,
		boundAt,
	)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE execution_game_epochs
		SET authority_epoch = 2
		WHERE game_attempt_id = $1`, fixture.attemptID)
	require.ErrorContains(t, err, "Execution epoch evidence is immutable")

	rebindCommandID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	// The fenced rebind is deliberately written before durable command receipt.
	// Deferred command evidence must still make the transaction commit.
	_, err = tx.Exec(ctx, `
		INSERT INTO execution_game_epoch_rebinds (
			game_attempt_id, rebind_sequence, command_id,
			tournament_id, roster_id, wave_id, series_id, slot_id,
			previous_authority_holder_id, previous_authority_lease_id,
			previous_authority_epoch, previous_authority_revision,
			authority_holder_id, authority_lease_id, authority_epoch, authority_revision,
			rebound_at, created_at
		)
		VALUES (
			$1, 1, $2,
			$3, $4, $5, $6, $7,
			$8, $9, 1, 1,
			$10, $11, 2, 2,
			$12, $12
		)`,
		fixture.attemptID,
		rebindCommandID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		waveID,
		fixture.draft.seriesID,
		slotID,
		holderID,
		leaseID,
		currentHolderID,
		currentLeaseID,
		currentAt,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO wave_control_commands (
			command_id, tournament_id, roster_id, wave_id, actor_id, action,
			source_projection_revision_id, source_projection_revision,
			source_tournament_revision, source_roster_revision, source_wave_revision,
			resulting_wave_revision, source_revisions, source_graph, request_digest,
			reason, result_document, executed_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5, 'resume',
			$6, 1, 1, 1, 1,
			2, '{"wave":1}'::JSONB, '{"game_count":1}'::JSONB, $7,
			NULL, '{"state":"active"}'::JSONB, $8, $8
		)`,
		rebindCommandID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		waveID,
		uuid.New(),
		uuid.New(),
		bytes.Repeat([]byte{19}, 32),
		currentAt,
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	orphanTx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = orphanTx.Rollback(ctx) }()
	_, err = orphanTx.Exec(ctx, `
		INSERT INTO execution_game_epoch_rebinds (
			game_attempt_id, rebind_sequence, command_id,
			tournament_id, roster_id, wave_id, series_id, slot_id,
			previous_authority_holder_id, previous_authority_lease_id,
			previous_authority_epoch, previous_authority_revision,
			authority_holder_id, authority_lease_id, authority_epoch, authority_revision,
			rebound_at, created_at
		)
		VALUES (
			$1, 2, $2,
			$3, $4, $5, $6, $7,
			$8, $9, 2, 2,
			$8, $9, 2, 2,
			$10, $10
		)`,
		fixture.attemptID,
		uuid.New(),
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		waveID,
		fixture.draft.seriesID,
		slotID,
		currentHolderID,
		currentLeaseID,
		currentAt,
	)
	require.NoError(t, err)
	require.Error(t, orphanTx.Commit(ctx))

	var reboundLeaseID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COALESCE(rebind.authority_lease_id, epoch.authority_lease_id)
		FROM execution_game_epochs AS epoch
		LEFT JOIN LATERAL (
			SELECT authority_lease_id
			FROM execution_game_epoch_rebinds
			WHERE game_attempt_id = epoch.game_attempt_id
			ORDER BY rebind_sequence DESC
			LIMIT 1
		) AS rebind ON TRUE
		WHERE epoch.game_attempt_id = $1`, fixture.attemptID).Scan(&reboundLeaseID))
	require.Equal(t, currentLeaseID, reboundLeaseID)

	_, err = sharedPool.Exec(ctx, `
		UPDATE execution_game_epoch_rebinds
		SET authority_epoch = 3
		WHERE game_attempt_id = $1`, fixture.attemptID)
	require.ErrorContains(t, err, "Execution epoch rebind evidence is immutable")

	commandID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO execution_epoch_replays (
			command_id, game_attempt_id, tournament_id, roster_id, wave_id, series_id, slot_id,
			assignment_id, assignment_attempt_id, current_holder_id, current_lease_id,
			current_epoch, expected_lease_revision, broken_lease_id, broken_epoch,
			expected_attempt_revision, command_digest, record_document, replayed_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $2, $9, $10,
			2, 2, $11, 1,
			1, $12, '{"terminal":"execution_epoch_break"}'::JSONB, $13, $13
		)`,
		commandID,
		fixture.attemptID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		waveID,
		fixture.draft.seriesID,
		slotID,
		fixture.assignmentID,
		currentHolderID,
		currentLeaseID,
		leaseID,
		bytes.Repeat([]byte{42}, 32),
		boundAt,
	)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE execution_epoch_replays
		SET command_digest = $2
		WHERE command_id = $1`, commandID, bytes.Repeat([]byte{43}, 32))
	require.ErrorContains(t, err, "Execution epoch evidence is immutable")

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO execution_epoch_replays (
			command_id, game_attempt_id, tournament_id, roster_id, wave_id, series_id, slot_id,
			assignment_id, assignment_attempt_id, current_holder_id, current_lease_id,
			current_epoch, expected_lease_revision, broken_lease_id, broken_epoch,
			expected_attempt_revision, command_digest, record_document, replayed_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $2, $9, $10,
			2, 2, $11, 1,
			1, $12, '{"terminal":"execution_epoch_break"}'::JSONB, $13, $13
		)`,
		uuid.New(),
		fixture.attemptID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		waveID,
		fixture.draft.seriesID,
		slotID,
		fixture.assignmentID,
		currentHolderID,
		currentLeaseID,
		leaseID,
		bytes.Repeat([]byte{44}, 32),
		boundAt,
	)
	require.Error(t, err)
}

func TestExecutionRecoveryPausedEpochRebindFencesResumeTransaction(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createPausedExecutionEpochFixture(ctx, t)
	queries := sqlc.New(sharedPool)

	// An older authority revision cannot append resume evidence after the
	// expired lease has been replaced by a newer live lease head.
	oldRows, err := queries.RebindPausedExecutionGameEpochs(ctx, sqlc.RebindPausedExecutionGameEpochsParams{
		TournamentID:      fixture.tournamentID,
		WaveID:            fixture.waveID,
		CommandID:         uuid.New(),
		AuthorityHolderID: fixture.previousHolderID,
		AuthorityLeaseID:  fixture.previousLeaseID,
		AuthorityEpoch:    1,
		ReboundAt:         pgtype.Timestamptz{Time: fixture.reboundAt, Valid: true},
	})
	require.NoError(t, err)
	require.Empty(t, oldRows)

	// The rebind may precede its command receipt in the outer transaction, but
	// a non-resume receipt is rejected by the deferred durable invariant.
	wrongActionTx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = wrongActionTx.Rollback(ctx) }()
	wrongActionCommandID := uuid.New()
	wrongActionRows, err := sqlc.New(wrongActionTx).RebindPausedExecutionGameEpochs(
		ctx,
		fixture.rebindParams(wrongActionCommandID),
	)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{fixture.gameID}, wrongActionRows)
	require.NoError(t, insertExecutionResumeCommand(
		ctx,
		wrongActionTx,
		fixture,
		wrongActionCommandID,
		"pause",
	))
	err = wrongActionTx.Commit(ctx)
	require.ErrorContains(t, err, "Execution epoch rebind requires its durable resume command")

	var rebindCount int
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM execution_game_epoch_rebinds
		WHERE game_attempt_id = $1`, fixture.gameID).Scan(&rebindCount))
	require.Zero(t, rebindCount)

	// Cancellation of an outer transaction also rolls back the appended epoch
	// evidence before a Game can be resumed.
	rollbackTx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = rollbackTx.Rollback(ctx) }()
	rollbackRows, err := sqlc.New(rollbackTx).RebindPausedExecutionGameEpochs(
		ctx,
		fixture.rebindParams(uuid.New()),
	)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{fixture.gameID}, rollbackRows)
	require.NoError(t, rollbackTx.Rollback(ctx))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM execution_game_epoch_rebinds
		WHERE game_attempt_id = $1`, fixture.gameID).Scan(&rebindCount))
	require.Zero(t, rebindCount)

	// One transaction obtains the latest live authority fence. A concurrent
	// resume cannot insert a competing successor while its locks are held.
	firstCommandID := uuid.New()
	firstTx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = firstTx.Rollback(ctx) }()
	firstRows, err := sqlc.New(firstTx).RebindPausedExecutionGameEpochs(
		ctx,
		fixture.rebindParams(firstCommandID),
	)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{fixture.gameID}, firstRows)

	secondTx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = secondTx.Rollback(ctx) }()
	_, err = secondTx.Exec(ctx, "SET LOCAL lock_timeout = '100ms'")
	require.NoError(t, err)
	_, err = sqlc.New(secondTx).RebindPausedExecutionGameEpochs(
		ctx,
		fixture.rebindParams(uuid.New()),
	)
	require.ErrorContains(t, err, "lock timeout")
	require.NoError(t, secondTx.Rollback(ctx))

	require.NoError(t, insertExecutionResumeCommand(ctx, firstTx, fixture, firstCommandID, "resume"))
	require.NoError(t, firstTx.Commit(ctx))
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM execution_game_epoch_rebinds
		WHERE game_attempt_id = $1`, fixture.gameID).Scan(&rebindCount))
	require.Equal(t, 1, rebindCount)
}

type pausedExecutionEpochFixture struct {
	tournamentID     uuid.UUID
	rosterID         uuid.UUID
	waveID           uuid.UUID
	seriesID         uuid.UUID
	slotID           uuid.UUID
	gameID           uuid.UUID
	previousHolderID uuid.UUID
	previousLeaseID  uuid.UUID
	currentHolderID  uuid.UUID
	currentLeaseID   uuid.UUID
	reboundAt        time.Time
}

func (fixture pausedExecutionEpochFixture) rebindParams(
	commandID uuid.UUID,
) sqlc.RebindPausedExecutionGameEpochsParams {
	return sqlc.RebindPausedExecutionGameEpochsParams{
		TournamentID:      fixture.tournamentID,
		WaveID:            fixture.waveID,
		CommandID:         commandID,
		AuthorityHolderID: fixture.currentHolderID,
		AuthorityLeaseID:  fixture.currentLeaseID,
		AuthorityEpoch:    2,
		ReboundAt:         pgtype.Timestamptz{Time: fixture.reboundAt, Valid: true},
	}
}

func createPausedExecutionEpochFixture(
	ctx context.Context,
	t *testing.T,
) pausedExecutionEpochFixture {
	t.Helper()

	result := createResultAuditMigrationFixture(ctx, t)
	waveAt := result.lockedAt.Add(time.Second).UTC().Truncate(time.Microsecond)
	_, err := sharedPool.Exec(ctx, `
		UPDATE tournaments
		SET state = 'swiss',
			started_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, result.draft.tournamentID, waveAt)
	require.NoError(t, err)

	waveID, _ := createMigrationWave(
		ctx,
		t,
		result.draft.tournamentID,
		result.draft.rosterID,
		result.draft.participantIDs,
		waveAt,
	)
	windowID, _ := openMigrationReadyWindow(
		ctx,
		t,
		waveID,
		result.draft.rosterID,
		waveAt.Add(time.Second),
		waveAt.Add(time.Minute),
	)
	for _, participantID := range result.draft.participantIDs {
		markMigrationReady(
			ctx,
			t,
			windowID,
			waveID,
			result.draft.rosterID,
			participantID,
			waveAt.Add(2*time.Second),
		)
	}
	require.NoError(t, startMigrationWave(ctx, waveID, windowID, waveAt.Add(3*time.Second)))
	pausedAt := waveAt.Add(4 * time.Second)
	_, err = sharedPool.Exec(ctx, `
		UPDATE waves
		SET state = 'paused',
			paused_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, waveID, pausedAt)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO wave_series (wave_id, tournament_id, roster_id, series_id, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		waveID,
		result.draft.tournamentID,
		result.draft.rosterID,
		result.draft.seriesID,
		waveAt,
	)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE series
		SET state = 'technical_pause',
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, result.draft.seriesID, pausedAt)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		UPDATE game_attempts
		SET state = 'paused',
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, result.attemptID, pausedAt)
	require.NoError(t, err)

	var slotID uuid.UUID
	require.NoError(t, sharedPool.QueryRow(ctx, `
		SELECT slot_id FROM game_attempts WHERE id = $1`, result.attemptID).Scan(&slotID))
	leaseAt := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	previousLeaseAt := leaseAt.Add(-time.Second)
	previousHolderID := uuid.New()
	previousLeaseID := uuid.New()
	currentHolderID := uuid.New()
	currentLeaseID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO execution_authority_leases (
			tournament_id, command_id, holder_id, lease_id, epoch,
			process_kind, revision, acquired_at, renewed_at, expires_at, created_at
		)
		VALUES ($1, $2, $3, $4, 1, 'authority', 1, $5, $5, $6, $5)`,
		result.draft.tournamentID,
		uuid.New(),
		previousHolderID,
		previousLeaseID,
		previousLeaseAt,
		leaseAt,
	)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO execution_authority_leases (
			tournament_id, command_id, holder_id, lease_id, epoch,
			process_kind, revision, previous_revision, previous_lease_id, previous_epoch,
			acquired_at, renewed_at, expires_at, created_at
		)
		VALUES ($1, $2, $3, $4, 2, 'authority', 2, 1, $5, 1, $6, $6, $7, $6)`,
		result.draft.tournamentID,
		uuid.New(),
		currentHolderID,
		currentLeaseID,
		previousLeaseID,
		leaseAt,
		leaseAt.Add(time.Minute),
	)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO execution_game_epochs (
			game_attempt_id, tournament_id, roster_id, wave_id, series_id, slot_id,
			authority_holder_id, authority_lease_id, authority_epoch, authority_revision,
			bound_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 1, 1, $9, $9)`,
		result.attemptID,
		result.draft.tournamentID,
		result.draft.rosterID,
		waveID,
		result.draft.seriesID,
		slotID,
		previousHolderID,
		previousLeaseID,
		previousLeaseAt,
	)
	require.NoError(t, err)

	return pausedExecutionEpochFixture{
		tournamentID:     result.draft.tournamentID,
		rosterID:         result.draft.rosterID,
		waveID:           waveID,
		seriesID:         result.draft.seriesID,
		slotID:           slotID,
		gameID:           result.attemptID,
		previousHolderID: previousHolderID,
		previousLeaseID:  previousLeaseID,
		currentHolderID:  currentHolderID,
		currentLeaseID:   currentLeaseID,
		reboundAt:        time.Now().UTC().Add(-time.Millisecond).Truncate(time.Microsecond),
	}
}

func insertExecutionResumeCommand(
	ctx context.Context,
	tx pgx.Tx,
	fixture pausedExecutionEpochFixture,
	commandID uuid.UUID,
	action string,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO wave_control_commands (
			command_id, tournament_id, roster_id, wave_id, actor_id, action,
			source_projection_revision_id, source_projection_revision,
			source_tournament_revision, source_roster_revision, source_wave_revision,
			resulting_wave_revision, source_revisions, source_graph, request_digest,
			reason, result_document, executed_at, created_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, 1, 1, 1, 1,
			2, '{"wave":1}'::JSONB, '{"game_count":1}'::JSONB, $8,
			NULL, '{"state":"active"}'::JSONB, $9, $9
		)`,
		commandID,
		fixture.tournamentID,
		fixture.rosterID,
		fixture.waveID,
		uuid.New(),
		action,
		uuid.New(),
		bytes.Repeat([]byte{21}, 32),
		fixture.reboundAt,
	)
	return err
}
