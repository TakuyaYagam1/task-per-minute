//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/waveseed"
)

var errMigrationWaveStartConflict = errors.New("wave already started")

func createMigrationWave(
	ctx context.Context, tb testing.TB,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	createdAt time.Time,
) (uuid.UUID, uuid.UUID) {
	tb.Helper()
	require.GreaterOrEqual(tb, len(participantIDs), 2)

	seed, err := waveseed.CreateWave(ctx, sharedPool, waveseed.Input{
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

	seed, err := waveseed.OpenReadyWindow(ctx, sharedPool, waveID, rosterID, openedAt, deadline)
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
	rows, err := waveseed.MarkReady(ctx, sharedPool, windowID, waveID, rosterID, participantID, readyAt)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, rows)
}

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
		SET state = 'active', started_at = $2,
			revision = revision + 1, updated_at = $2
		WHERE id = $1 AND started_at IS NULL`, waveID, startedAt)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errMigrationWaveStartConflict
	}
	return tx.Commit(ctx)
}
