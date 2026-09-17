//go:build integration

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/waveseed"
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

	seed, err := waveseed.CreateWave(ctx, migrationPool, waveseed.Input{
		TournamentID:   tournamentID,
		RosterID:       rosterID,
		ParticipantIDs: participantIDs,
		CreatedAt:      createdAt,
	})
	require.NoError(tb, err)
	return seed.WaveID, seed.RevisionID
}

func openMigrationReadyWindow(
	ctx context.Context, tb testing.TB,
	waveID uuid.UUID,
	rosterID uuid.UUID,
	openedAt time.Time,
	deadline time.Time,
) (uuid.UUID, uuid.UUID) {
	tb.Helper()

	seed, err := waveseed.OpenReadyWindow(ctx, migrationPool, waveID, rosterID, openedAt, deadline)
	require.NoError(tb, err)
	return seed.WindowID, seed.RevisionID
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
	rows, err := waveseed.MarkReady(
		ctx,
		migrationPool,
		windowID,
		waveID,
		rosterID,
		participantID,
		readyAt,
	)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, rows)
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
	err := migrationPool.QueryRow(ctx, `
		SELECT wave.started_at, ready_window.consumed_at
		FROM waves AS wave
		JOIN ready_windows AS ready_window ON ready_window.wave_id = wave.id
		WHERE wave.id = $1`, waveID).Scan(&storedWaveStart, &storedWindowStart)
	require.NoError(t, err)
	require.True(t, committedStart.Equal(storedWaveStart))
	require.True(t, committedStart.Equal(storedWindowStart))
	return committedStart
}
