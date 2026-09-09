//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

var errMigrationWaveStartConflict = errors.New("wave already started")

func TestExecutionMigration(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	tournamentID := createMigrationTournament(ctx, t)
	rosterID := createMigrationRoster(ctx, t, tournamentID)
	playerIDs := createMigrationPlayers(ctx, t, 4)
	participantIDs := createSwissMigrationParticipants(ctx, t, rosterID, playerIDs)
	createdAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
	openedAt := createdAt.Add(time.Minute)
	deadline := openedAt.Add(30 * time.Second)

	waveID, waveRevisionID := createMigrationWave(
		ctx, t, tournamentID, rosterID, participantIDs[:2], createdAt,
	)
	windowID, windowRevisionID := openMigrationReadyWindow(
		ctx, t, waveID, rosterID, openedAt, deadline,
	)

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO ready_windows (
			wave_id, roster_id, revision_id, opened_at, deadline, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $4)`,
		waveID, rosterID, uuid.New(), openedAt, deadline)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE waves
		SET state = 'active', started_at = $2, updated_at = $2
		WHERE id = $1`, waveID, openedAt.Add(time.Second))
	require.Error(t, err)

	markMigrationReady(ctx, t, windowID, waveID, rosterID, participantIDs[0], openedAt.Add(time.Second))
	_, err = sharedPool.Exec(ctx, `
		UPDATE ready_windows
		SET state = 'consumed', consumed_at = $2
		WHERE id = $1`, windowID, openedAt.Add(2*time.Second))
	require.Error(t, err)

	markMigrationReady(ctx, t, windowID, waveID, rosterID, participantIDs[1], openedAt.Add(2*time.Second))
	_, err = sharedPool.Exec(ctx, `
		UPDATE waves
		SET state = 'ready', revision = revision + 1, updated_at = $2
		WHERE id = $1`, waveID, openedAt.Add(2*time.Second))
	require.NoError(t, err)

	startedAt := assertOneConcurrentWaveStart(ctx, t, waveID, windowID, openedAt.Add(3*time.Second))
	pausedAt := startedAt.Add(time.Second)
	_, err = sharedPool.Exec(ctx, `
		UPDATE waves
		SET state = 'paused', paused_at = $2, revision = revision + 1, updated_at = $2
		WHERE id = $1`, waveID, pausedAt)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE waves
		SET started_at = $2, revision = revision + 1, updated_at = $2
		WHERE id = $1`, waveID, startedAt.Add(time.Second))
	require.Error(t, err)

	replacementCreatedAt := pausedAt.Add(time.Second)
	replacementWaveID, replacementWindowID := replaceMigrationWave(
		ctx, t,
		tournamentID,
		rosterID,
		waveID,
		windowID,
		participantIDs[:2],
		replacementCreatedAt,
	)

	var (
		readinessCount int
		readyCount     int
	)
	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE ready)
		FROM wave_readiness
		WHERE ready_window_id = $1`, windowID).Scan(&readinessCount, &readyCount)
	require.NoError(t, err)
	require.Equal(t, 2, readinessCount)
	require.Equal(t, 2, readyCount)

	var (
		storedReplacesID  uuid.UUID
		storedWaveState   string
		storedWindowState string
	)
	err = sharedPool.QueryRow(ctx, `
		SELECT replacement.replaces_wave_id, original.state, original_window.state
		FROM waves AS replacement
		JOIN waves AS original ON original.id = replacement.replaces_wave_id
		JOIN ready_windows AS original_window ON original_window.wave_id = original.id
		WHERE replacement.id = $1`, replacementWaveID).Scan(
		&storedReplacesID,
		&storedWaveState,
		&storedWindowState,
	)
	require.NoError(t, err)
	require.Equal(t, waveID, storedReplacesID)
	require.Equal(t, "superseded", storedWaveState)
	require.Equal(t, "superseded", storedWindowState)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO wave_readiness (
			ready_window_id, wave_id, roster_id, participant_id, ready_at
		)
		VALUES ($1, $2, $3, $4, $5)`,
		windowID, waveID, rosterID, participantIDs[0], replacementCreatedAt)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO waves (
			tournament_id, roster_id, revision_id, replaces_wave_id,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $5)`,
		tournamentID, rosterID, uuid.New(), waveID, replacementCreatedAt)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO waves (
			tournament_id, roster_id, revision_id, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $4)`,
		tournamentID, rosterID, waveRevisionID, replacementCreatedAt)
	require.Error(t, err)

	assertExpiredReadinessCleared(
		ctx, t, tournamentID, rosterID, participantIDs[2:], replacementCreatedAt.Add(time.Minute),
	)
	assertReadyWindowRevisionCannotBeReused(
		ctx, t,
		tournamentID,
		rosterID,
		participantIDs[2:],
		windowRevisionID,
		replacementCreatedAt.Add(2*time.Minute),
	)

	var replacementState string
	err = sharedPool.QueryRow(ctx, `
		SELECT wave.state
		FROM waves AS wave
		JOIN ready_windows AS ready_window ON ready_window.wave_id = wave.id
		WHERE wave.id = $1 AND ready_window.id = $2`, replacementWaveID, replacementWindowID).Scan(&replacementState)
	require.NoError(t, err)
	require.Equal(t, "ready_window_open", replacementState)
}
