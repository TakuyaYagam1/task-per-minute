//go:build integration

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func assertExpiredReadinessCleared(
	ctx context.Context, t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	createdAt time.Time,
) {
	t.Helper()
	waveID, _ := createMigrationWave(
		ctx, t, tournamentID, rosterID, participantIDs, createdAt,
	)
	openedAt := createdAt.Add(time.Second)
	windowID, _ := openMigrationReadyWindow(
		ctx, t, waveID, rosterID, openedAt, openedAt.Add(30*time.Second),
	)
	markMigrationReady(ctx, t, windowID, waveID, rosterID, participantIDs[0], openedAt.Add(time.Second))

	tx, err := migrationPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		UPDATE wave_readiness
		SET ready = false,
			ready_at = NULL,
			revision = revision + 1,
			updated_at = $2
		WHERE wave_id = $1 AND ready_window_id = $3 AND ready`,
		waveID, openedAt.Add(31*time.Second), windowID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE ready_windows
		SET state = 'expired'
		WHERE id = $1`, windowID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE waves
		SET state = 'ready_window_expired', revision = revision + 1, updated_at = $2
		WHERE id = $1`, waveID, openedAt.Add(31*time.Second))
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	var (
		readinessCount int
		readyCount     int
	)
	err = migrationPool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE ready)
		FROM wave_readiness
		WHERE ready_window_id = $1`, windowID).Scan(&readinessCount, &readyCount)
	require.NoError(t, err)
	require.Equal(t, len(participantIDs), readinessCount)
	require.Zero(t, readyCount)
}

func assertReadyWindowRevisionCannotBeReused(
	ctx context.Context, t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	revisionID uuid.UUID,
	createdAt time.Time,
) {
	t.Helper()
	waveID, _ := createMigrationWave(
		ctx, t, tournamentID, rosterID, participantIDs, createdAt,
	)
	openedAt := createdAt.Add(time.Second)
	_, err := migrationPool.Exec(ctx, `
		INSERT INTO ready_windows (
			wave_id, roster_id, revision_id, opened_at, deadline, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $4)`,
		waveID, rosterID, revisionID, openedAt, openedAt.Add(30*time.Second))
	require.Error(t, err)
}
