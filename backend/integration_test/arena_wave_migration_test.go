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

var errArenaMigrationWaveStartConflict = errors.New("Arena Wave already started")

func TestArenaWaveMigration(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	tournamentID := createArenaMigrationTournament(t, ctx)
	rosterID := createArenaMigrationRoster(t, ctx, tournamentID)
	playerIDs := createArenaMigrationPlayers(t, ctx, 4)
	participantIDs := createSwissMigrationParticipants(t, ctx, rosterID, playerIDs)
	createdAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
	openedAt := createdAt.Add(time.Minute)
	deadline := openedAt.Add(30 * time.Second)

	waveID, waveRevisionID := createArenaMigrationWave(
		t, ctx, tournamentID, rosterID, participantIDs[:2], nil, createdAt,
	)
	windowID, windowRevisionID := openArenaMigrationReadyWindow(
		t, ctx, waveID, rosterID, openedAt, deadline,
	)

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_ready_windows (
			wave_id, roster_id, revision_id, opened_at, deadline, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $4)`,
		waveID, rosterID, uuid.New(), openedAt, deadline)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_waves
		SET state = 'active', started_at = $2, updated_at = $2
		WHERE id = $1`, waveID, openedAt.Add(time.Second))
	require.Error(t, err)

	markArenaMigrationReady(t, ctx, windowID, waveID, rosterID, participantIDs[0], openedAt.Add(time.Second))
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_ready_windows
		SET state = 'consumed', consumed_at = $2
		WHERE id = $1`, windowID, openedAt.Add(2*time.Second))
	require.Error(t, err)

	markArenaMigrationReady(t, ctx, windowID, waveID, rosterID, participantIDs[1], openedAt.Add(2*time.Second))
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_waves
		SET state = 'ready', revision = revision + 1, updated_at = $2
		WHERE id = $1`, waveID, openedAt.Add(2*time.Second))
	require.NoError(t, err)

	startedAt := assertOneConcurrentArenaWaveStart(t, ctx, waveID, windowID, openedAt.Add(3*time.Second))
	pausedAt := startedAt.Add(time.Second)
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_waves
		SET state = 'paused', paused_at = $2, revision = revision + 1, updated_at = $2
		WHERE id = $1`, waveID, pausedAt)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_waves
		SET started_at = $2, revision = revision + 1, updated_at = $2
		WHERE id = $1`, waveID, startedAt.Add(time.Second))
	require.Error(t, err)

	replacementCreatedAt := pausedAt.Add(time.Second)
	replacementWaveID, replacementWindowID := replaceArenaMigrationWave(
		t,
		ctx,
		tournamentID,
		rosterID,
		waveID,
		windowID,
		participantIDs[:2],
		replacementCreatedAt,
	)

	var readinessCount int
	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_wave_readiness
		WHERE ready_window_id = $1`, windowID).Scan(&readinessCount)
	require.NoError(t, err)
	require.Zero(t, readinessCount)

	var (
		storedReplacesID  uuid.UUID
		storedWaveState   string
		storedWindowState string
	)
	err = sharedPool.QueryRow(ctx, `
		SELECT replacement.replaces_wave_id, original.state, original_window.state
		FROM arena_waves AS replacement
		JOIN arena_waves AS original ON original.id = replacement.replaces_wave_id
		JOIN arena_ready_windows AS original_window ON original_window.wave_id = original.id
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
		INSERT INTO arena_wave_readiness (
			ready_window_id, wave_id, roster_id, participant_id, ready_at
		)
		VALUES ($1, $2, $3, $4, $5)`,
		windowID, waveID, rosterID, participantIDs[0], replacementCreatedAt)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_waves (
			tournament_id, roster_id, revision_id, replaces_wave_id,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $5)`,
		tournamentID, rosterID, uuid.New(), waveID, replacementCreatedAt)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_waves (
			tournament_id, roster_id, revision_id, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $4)`,
		tournamentID, rosterID, waveRevisionID, replacementCreatedAt)
	require.Error(t, err)

	assertExpiredArenaReadinessCleared(
		t, ctx, tournamentID, rosterID, participantIDs[2:], replacementCreatedAt.Add(time.Minute),
	)
	assertArenaReadyWindowRevisionCannotBeReused(
		t,
		ctx,
		tournamentID,
		rosterID,
		participantIDs[2:],
		windowRevisionID,
		replacementCreatedAt.Add(2*time.Minute),
	)

	var replacementState string
	err = sharedPool.QueryRow(ctx, `
		SELECT wave.state
		FROM arena_waves AS wave
		JOIN arena_ready_windows AS ready_window ON ready_window.wave_id = wave.id
		WHERE wave.id = $1 AND ready_window.id = $2`, replacementWaveID, replacementWindowID).Scan(&replacementState)
	require.NoError(t, err)
	require.Equal(t, "ready_window_open", replacementState)
}

func createArenaMigrationWave(
	t testing.TB,
	ctx context.Context,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	replacesWaveID any,
	createdAt time.Time,
) (uuid.UUID, uuid.UUID) {
	t.Helper()
	require.GreaterOrEqual(t, len(participantIDs), 2)

	waveID := uuid.New()
	revisionID := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_waves (
			id, tournament_id, roster_id, revision_id, replaces_wave_id,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		waveID, tournamentID, rosterID, revisionID, replacesWaveID, createdAt)
	require.NoError(t, err)

	for _, participantID := range participantIDs {
		_, err = sharedPool.Exec(ctx, `
			INSERT INTO arena_wave_members (
				wave_id, roster_id, participant_id, created_at
			)
			VALUES ($1, $2, $3, $4)`, waveID, rosterID, participantID, createdAt)
		require.NoError(t, err)
	}
	return waveID, revisionID
}

func openArenaMigrationReadyWindow(
	t testing.TB,
	ctx context.Context,
	waveID uuid.UUID,
	rosterID uuid.UUID,
	openedAt time.Time,
	deadline time.Time,
) (uuid.UUID, uuid.UUID) {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	windowID := uuid.New()
	revisionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_ready_windows (
			id, wave_id, roster_id, revision_id,
			opened_at, deadline, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $5)`,
		windowID, waveID, rosterID, revisionID, openedAt, deadline)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		UPDATE arena_waves
		SET state = 'ready_window_open', revision = revision + 1, updated_at = $2
		WHERE id = $1`, waveID, openedAt)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return windowID, revisionID
}

func markArenaMigrationReady(
	t testing.TB,
	ctx context.Context,
	windowID uuid.UUID,
	waveID uuid.UUID,
	rosterID uuid.UUID,
	participantID uuid.UUID,
	readyAt time.Time,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_wave_readiness (
			ready_window_id, wave_id, roster_id, participant_id, ready_at
		)
		VALUES ($1, $2, $3, $4, $5)`,
		windowID, waveID, rosterID, participantID, readyAt)
	require.NoError(t, err)
}

func assertOneConcurrentArenaWaveStart(
	t *testing.T,
	ctx context.Context,
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
				err:       startArenaMigrationWave(ctx, waveID, windowID, startedAt),
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
		require.ErrorIs(t, result.err, errArenaMigrationWaveStartConflict)
	}
	require.Equal(t, 1, successes)

	var (
		storedWaveStart   time.Time
		storedWindowStart time.Time
	)
	err := sharedPool.QueryRow(ctx, `
		SELECT wave.started_at, ready_window.consumed_at
		FROM arena_waves AS wave
		JOIN arena_ready_windows AS ready_window ON ready_window.wave_id = wave.id
		WHERE wave.id = $1`, waveID).Scan(&storedWaveStart, &storedWindowStart)
	require.NoError(t, err)
	require.True(t, committedStart.Equal(storedWaveStart))
	require.True(t, committedStart.Equal(storedWindowStart))
	return committedStart
}

func startArenaMigrationWave(
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
		UPDATE arena_ready_windows
		SET state = 'consumed', consumed_at = $2
		WHERE id = $1 AND state = 'open'`, windowID, startedAt)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errArenaMigrationWaveStartConflict
	}

	result, err = tx.Exec(ctx, `
		UPDATE arena_waves
		SET state = 'active',
			started_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1 AND started_at IS NULL`, waveID, startedAt)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return errArenaMigrationWaveStartConflict
	}
	return tx.Commit(ctx)
}

func replaceArenaMigrationWave(
	t testing.TB,
	ctx context.Context,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	originalWaveID uuid.UUID,
	originalWindowID uuid.UUID,
	participantIDs []uuid.UUID,
	createdAt time.Time,
) (uuid.UUID, uuid.UUID) {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		UPDATE arena_ready_windows
		SET state = 'superseded', consumed_at = NULL
		WHERE id = $1`, originalWindowID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE arena_waves
		SET state = 'superseded',
			paused_at = NULL,
			closed_at = $2,
			revision = revision + 1,
			updated_at = $2
		WHERE id = $1`, originalWaveID, createdAt)
	require.NoError(t, err)

	replacementWaveID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_waves (
			id, tournament_id, roster_id, revision_id, replaces_wave_id,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		replacementWaveID, tournamentID, rosterID, uuid.New(), originalWaveID, createdAt)
	require.NoError(t, err)
	for _, participantID := range participantIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO arena_wave_members (
				wave_id, roster_id, participant_id, created_at
			)
			VALUES ($1, $2, $3, $4)`, replacementWaveID, rosterID, participantID, createdAt)
		require.NoError(t, err)
	}

	replacementWindowID := uuid.New()
	openedAt := createdAt.Add(time.Second)
	_, err = tx.Exec(ctx, `
		INSERT INTO arena_ready_windows (
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
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE arena_waves
		SET state = 'ready_window_open', revision = revision + 1, updated_at = $2
		WHERE id = $1`, replacementWaveID, openedAt)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	return replacementWaveID, replacementWindowID
}

func assertExpiredArenaReadinessCleared(
	t *testing.T,
	ctx context.Context,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	createdAt time.Time,
) {
	t.Helper()
	waveID, _ := createArenaMigrationWave(
		t, ctx, tournamentID, rosterID, participantIDs, nil, createdAt,
	)
	openedAt := createdAt.Add(time.Second)
	windowID, _ := openArenaMigrationReadyWindow(
		t, ctx, waveID, rosterID, openedAt, openedAt.Add(30*time.Second),
	)
	markArenaMigrationReady(t, ctx, windowID, waveID, rosterID, participantIDs[0], openedAt.Add(time.Second))

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		UPDATE arena_ready_windows
		SET state = 'expired'
		WHERE id = $1`, windowID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE arena_waves
		SET state = 'ready_window_expired', revision = revision + 1, updated_at = $2
		WHERE id = $1`, waveID, openedAt.Add(31*time.Second))
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	var readinessCount int
	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_wave_readiness
		WHERE ready_window_id = $1`, windowID).Scan(&readinessCount)
	require.NoError(t, err)
	require.Zero(t, readinessCount)
}

func assertArenaReadyWindowRevisionCannotBeReused(
	t *testing.T,
	ctx context.Context,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	revisionID uuid.UUID,
	createdAt time.Time,
) {
	t.Helper()
	waveID, _ := createArenaMigrationWave(
		t, ctx, tournamentID, rosterID, participantIDs, nil, createdAt,
	)
	openedAt := createdAt.Add(time.Second)
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_ready_windows (
			wave_id, roster_id, revision_id, opened_at, deadline, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $4)`,
		waveID, rosterID, revisionID, openedAt, openedAt.Add(30*time.Second))
	require.Error(t, err)
}
