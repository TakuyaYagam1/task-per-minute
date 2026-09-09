//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func startMigrationWave(
	ctx context.Context,
	waveID uuid.UUID,
	windowID uuid.UUID,
	startedAt time.Time,
) error {
	tx, err := sharedPool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := tx.Exec(ctx, `
		UPDATE ready_windows
		SET state = 'consumed', consumed_at = $2
		WHERE id = $1 AND state = 'open'`, windowID, startedAt)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errMigrationWaveStartConflict
	}

	result, err = tx.Exec(ctx, `
		UPDATE waves
		SET state = 'active',
			started_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1 AND started_at IS NULL`, waveID, startedAt)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errMigrationWaveStartConflict
	}
	return tx.Commit(ctx)
}

func replaceMigrationWave(
	ctx context.Context, tb testing.TB,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	originalWaveID uuid.UUID,
	originalWindowID uuid.UUID,
	participantIDs []uuid.UUID,
	createdAt time.Time,
) (uuid.UUID, uuid.UUID) {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		UPDATE wave_readiness
		SET ready = false,
			ready_at = NULL,
			revision = revision + 1,
			updated_at = $2
		WHERE wave_id = $1 AND ready`, originalWaveID, createdAt)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE ready_windows
		SET state = 'superseded', consumed_at = NULL
		WHERE id = $1`, originalWindowID)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE waves
		SET state = 'superseded',
			paused_at = NULL,
			closed_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, originalWaveID, createdAt)
	require.NoError(tb, err)

	replacementWaveID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO waves (
			id, tournament_id, roster_id, revision_id, replaces_wave_id,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		replacementWaveID, tournamentID, rosterID, uuid.New(), originalWaveID, createdAt)
	require.NoError(tb, err)
	for _, participantID := range participantIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO wave_members (
				wave_id, roster_id, participant_id, created_at
			)
			VALUES ($1, $2, $3, $4)`, replacementWaveID, rosterID, participantID, createdAt)
		require.NoError(tb, err)
		_, err = tx.Exec(ctx, `
			INSERT INTO wave_readiness (
				wave_id, roster_id, participant_id, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $4)`, replacementWaveID, rosterID, participantID, createdAt)
		require.NoError(tb, err)
	}

	replacementWindowID := uuid.New()
	openedAt := createdAt.Add(time.Second)
	_, err = tx.Exec(
		ctx, `
		INSERT INTO ready_windows (
			id, wave_id, roster_id, revision_id,
			opened_at, deadline, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $5)`,
		replacementWindowID,
		replacementWaveID,
		rosterID,
		uuid.New(),
		openedAt,
		openedAt.Add(30*time.Second),
	)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE wave_readiness
		SET ready_window_id = $2, revision = revision + 1, updated_at = $3
		WHERE wave_id = $1 AND ready_window_id IS NULL`, replacementWaveID, replacementWindowID, openedAt)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE waves
		SET state = 'ready_window_open', revision = revision + 1, updated_at = $2
		WHERE id = $1`, replacementWaveID, openedAt)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
	return replacementWaveID, replacementWindowID
}
