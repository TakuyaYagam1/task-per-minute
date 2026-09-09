//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func createMigrationWave(
	ctx context.Context, tb testing.TB,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	createdAt time.Time,
) (uuid.UUID, uuid.UUID) {
	tb.Helper()
	require.GreaterOrEqual(tb, len(participantIDs), 2)

	waveID := uuid.New()
	revisionID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO waves (
			id, tournament_id, roster_id, revision_id, replaces_wave_id,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		waveID, tournamentID, rosterID, revisionID, nil, createdAt)
	require.NoError(tb, err)

	for _, participantID := range participantIDs {
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO wave_members (
				wave_id, roster_id, participant_id, created_at
			)
			VALUES ($1, $2, $3, $4)`, waveID, rosterID, participantID, createdAt)
		require.NoError(tb, err)
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO wave_readiness (
				wave_id, roster_id, participant_id, created_at, updated_at
			)
			VALUES ($1, $2, $3, $4, $4)`, waveID, rosterID, participantID, createdAt)
		require.NoError(tb, err)
	}
	return waveID, revisionID
}

func openMigrationReadyWindow(
	ctx context.Context, tb testing.TB,
	waveID uuid.UUID,
	rosterID uuid.UUID,
	openedAt time.Time,
	deadline time.Time,
) (uuid.UUID, uuid.UUID) {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()

	windowID := uuid.New()
	revisionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO ready_windows (
			id, wave_id, roster_id, revision_id,
			opened_at, deadline, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $5)`,
		windowID, waveID, rosterID, revisionID, openedAt, deadline)
	require.NoError(tb, err)

	_, err = tx.Exec(ctx, `
		UPDATE wave_readiness
		SET ready_window_id = $2, revision = revision + 1, updated_at = $3
		WHERE wave_id = $1 AND ready_window_id IS NULL`, waveID, windowID, openedAt)
	require.NoError(tb, err)

	_, err = tx.Exec(ctx, `
		UPDATE waves
		SET state = 'ready_window_open', revision = revision + 1, updated_at = $2
		WHERE id = $1`, waveID, openedAt)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
	return windowID, revisionID
}

func markMigrationReady(
	ctx context.Context, tb testing.TB,
	windowID uuid.UUID,
	waveID uuid.UUID,
	rosterID uuid.UUID,
	participantID uuid.UUID,
	readyAt time.Time,
) {
	tb.Helper()
	result, err := sharedPool.Exec(ctx, `
		UPDATE wave_readiness
		SET ready = true,
			ready_at = $5,
			revision = revision + 1,
			updated_at = $5
		WHERE ready_window_id = $1
			AND wave_id = $2
			AND roster_id = $3
			AND participant_id = $4
			AND revision = 2
			AND NOT ready`,
		windowID, waveID, rosterID, participantID, readyAt)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, result.RowsAffected())
}

func assertOneConcurrentWaveStart(
	ctx context.Context, t *testing.T,
	waveID uuid.UUID,
	windowID uuid.UUID,
	firstStart time.Time,
) time.Time {
	t.Helper()

	type startResult struct {
		startedAt time.Time
		err       error
	}
	start := make(chan struct{})
	results := make(chan startResult, 2)
	for i := range 2 {
		startedAt := firstStart.Add(time.Duration(i) * time.Millisecond)
		go func() {
			<-start
			results <- startResult{
				startedAt: startedAt,
				err:       startMigrationWave(ctx, waveID, windowID, startedAt),
			}
		}()
	}
	close(start)

	var committedStart time.Time
	successes := 0
	for range 2 {
		result := <-results
		if result.err == nil {
			successes++
			committedStart = result.startedAt
			continue
		}
		require.ErrorIs(t, result.err, errMigrationWaveStartConflict)
	}
	require.Equal(t, 1, successes)

	var (
		storedWaveStart   time.Time
		storedWindowStart time.Time
	)
	err := sharedPool.QueryRow(ctx, `
		SELECT wave.started_at, ready_window.consumed_at
		FROM waves AS wave
		JOIN ready_windows AS ready_window ON ready_window.wave_id = wave.id
		WHERE wave.id = $1`, waveID).Scan(&storedWaveStart, &storedWindowStart)
	require.NoError(t, err)
	require.True(t, committedStart.Equal(storedWaveStart))
	require.True(t, committedStart.Equal(storedWindowStart))
	return committedStart
}
