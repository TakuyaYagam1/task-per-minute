//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
)

func insertReconnectRootAtEpoch(
	ctx context.Context,
	executor reconnectExecutor,
	fixture reconnectMigrationFixture,
	participantID uuid.UUID,
	intervalID uuid.UUID,
	presenceEpoch int64,
	intervalNumber int,
	openedAt time.Time,
) error {
	_, err := executor.Exec(
		ctx, `
		INSERT INTO reconnect_intervals (
			id, pause_id, roster_id, series_id, game_attempt_id,
			participant_id, presence_epoch, interval_number,
			opened_at, deadline_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $9, $9
		)`,
		intervalID,
		fixture.gamePauseID,
		fixture.draft.rosterID,
		fixture.draft.seriesID,
		fixture.attemptID,
		participantID,
		presenceEpoch,
		intervalNumber,
		openedAt,
		openedAt.Add(2*time.Minute),
	)
	if err != nil {
		return err
	}
	_, err = executor.Exec(
		ctx, `
		UPDATE reconnect_slot_counters
		SET slots_used = slots_used + 1,
			revision = revision + 1,
			updated_at = $3
		WHERE pause_id = $1 AND participant_id = $2`,
		fixture.gamePauseID,
		participantID,
		openedAt,
	)
	return err
}

func createNormalWavePauseAndSuspendInterval(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
	waveID uuid.UUID,
	intervalID uuid.UUID,
	suspendedAt time.Time,
) uuid.UUID {
	tb.Helper()

	pauseID := uuid.New()
	revisionID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(
		ctx, `
		INSERT INTO pauses (
			id, tournament_id, roster_id, scope_kind, scope_id,
			wave_id, depth, reason, paused_from_state,
			current_revision_id, started_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, 'wave', $4,
			$4, 0, 'operator', 'active',
			$5, $6, $6, $6
		)`,
		pauseID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		waveID,
		revisionID,
		suspendedAt,
	)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO pause_revisions (
			id, pause_id, revision_number, state, created_at
		)
		VALUES ($1, $2, 1, 'active', $3)`, revisionID, pauseID, suspendedAt)
	require.NoError(tb, err)

	for _, participantID := range fixture.draft.participantIDs {
		_, err = tx.Exec(
			ctx, `
			INSERT INTO pause_presence_snapshots (
				pause_id, roster_id, series_id, participant_id,
				presence_state, presence_epoch, presence_revision,
				captured_at, created_at
			)
			SELECT
				$1, presence.roster_id, presence.series_id, presence.participant_id,
				presence.state, presence.presence_epoch, presence.revision,
				$3, $3
			FROM presence_states AS presence
			WHERE presence.series_id = $2 AND presence.participant_id = $4`,
			pauseID,
			fixture.draft.seriesID,
			suspendedAt,
			participantID,
		)
		require.NoError(tb, err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE reconnect_intervals
		SET state = 'cancelled',
			closed_at = $2,
			suspended_by_pause_id = $3,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, intervalID, suspendedAt, pauseID)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
	return pauseID
}

func resumeNormalWavePause(
	ctx context.Context, tb testing.TB,
	pauseID uuid.UUID,
	resumedAt time.Time,
) {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var previousRevisionID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT current_revision_id
		FROM pauses
		WHERE id = $1
		FOR UPDATE`, pauseID).Scan(&previousRevisionID)
	require.NoError(tb, err)
	revisionID := uuid.New()
	_, err = tx.Exec(
		ctx, `
		INSERT INTO pause_revisions (
			id, pause_id, previous_revision_id, revision_number,
			state, transition_reason, created_at
		)
		VALUES ($1, $2, $3, 2, 'resumed', 'continuation opened', $4)`,
		revisionID,
		pauseID,
		previousRevisionID,
		resumedAt,
	)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE pauses
		SET state = 'resumed',
			current_revision_id = $2,
			revision = revision + 1,
			resolved_at = $3,
			updated_at = $3
		WHERE id = $1`, pauseID, revisionID, resumedAt)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
}

func createCoveringWave(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	waveID, _ := createMigrationWave(
		ctx, tb,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		fixture.draft.participantIDs,
		createdAt,
	)
	return waveID
}

func createWaveWithMembers(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
	participantIDs []uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	waveID := uuid.New()
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO waves (
			id, tournament_id, roster_id, revision_id, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $5)`,
		waveID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		uuid.New(),
		createdAt,
	)
	require.NoError(tb, err)
	for _, participantID := range participantIDs {
		_, err = sharedPool.Exec(
			ctx, `
			INSERT INTO wave_members (
				wave_id, roster_id, participant_id, created_at
			)
			VALUES ($1, $2, $3, $4)`,
			waveID,
			fixture.draft.rosterID,
			participantID,
			createdAt,
		)
		require.NoError(tb, err)
	}
	return waveID
}

func attemptNormalWavePauseAndSuspendInterval(
	ctx context.Context, tb testing.TB,
	fixture reconnectMigrationFixture,
	waveID uuid.UUID,
	participantIDs []uuid.UUID,
	intervalID uuid.UUID,
	suspendedAt time.Time,
) error {
	tb.Helper()

	pauseID := uuid.New()
	revisionID := uuid.New()
	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(
		ctx, `
		INSERT INTO pauses (
			id, tournament_id, roster_id, scope_kind, scope_id,
			wave_id, depth, reason, paused_from_state,
			current_revision_id, started_at, created_at, updated_at
		)
		VALUES (
			$1, $2, $3, 'wave', $4,
			$4, 0, 'operator', 'active',
			$5, $6, $6, $6
		)`,
		pauseID,
		fixture.draft.tournamentID,
		fixture.draft.rosterID,
		waveID,
		revisionID,
		suspendedAt,
	)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO pause_revisions (
			id, pause_id, revision_number, state, created_at
		)
		VALUES ($1, $2, 1, 'active', $3)`, revisionID, pauseID, suspendedAt)
	require.NoError(tb, err)
	for _, participantID := range participantIDs {
		_, err = tx.Exec(
			ctx, `
			INSERT INTO pause_presence_snapshots (
				pause_id, roster_id, series_id, participant_id,
				presence_state, presence_epoch, presence_revision,
				captured_at, created_at
			)
			SELECT
				$1, presence.roster_id, presence.series_id, presence.participant_id,
				presence.state, presence.presence_epoch, presence.revision,
				$3, $3
			FROM presence_states AS presence
			WHERE presence.series_id = $2 AND presence.participant_id = $4`,
			pauseID,
			fixture.draft.seriesID,
			suspendedAt,
			participantID,
		)
		require.NoError(tb, err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE reconnect_intervals
		SET state = 'cancelled',
			closed_at = $2,
			suspended_by_pause_id = $3,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, intervalID, suspendedAt, pauseID)
	return err
}
