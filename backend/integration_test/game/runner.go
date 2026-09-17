//go:build integration

package game

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/gameseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/seriesseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/swissseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/tournamentseed"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	migrationPool   *pgxpool.Pool
	migrationPoolMu sync.Mutex
)

// RunGameMigration runs the moved game migration assertions against the
// caller-owned integration pool. The temporary pool binding keeps the
// existing assertion helpers small while serializing callers in one process.
func RunGameMigration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	require.NotNil(t, pool)
	migrationPoolMu.Lock()
	previousPool := migrationPool
	migrationPool = pool
	t.Cleanup(func() {
		migrationPool = previousPool
		migrationPoolMu.Unlock()
	})

	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	tournamentID := createMigrationTournament(ctx, t)
	rosterID := createMigrationRoster(ctx, t, tournamentID)
	playerIDs := createMigrationPlayers(ctx, t, 3)
	participantIDs := createSwissMigrationParticipants(ctx, t, rosterID, playerIDs)
	seriesID := createGameMigrationSeries(ctx, t, tournamentID, rosterID, participantIDs[:2], "bo3")
	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	assertWaveSeriesReplacementLineage(
		ctx, t, tournamentID, rosterID, seriesID, participantIDs[:2], createdAt,
	)

	chainSlotID := createMigrationGameSlot(ctx, t, seriesID, rosterID, 1, "web")
	firstAttemptID := createActiveMigrationAttempt(ctx, t, chainSlotID, seriesID, rosterID, createdAt)

	tx, err := migrationPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		UPDATE game_attempts
		SET state = 'void',
			result_reason = 'no_solve',
			result_revision_id = $2,
			revision = revision + 1,
			updated_at = $3,
			finished_at = $3
		WHERE id = $1`, firstAttemptID, uuid.New(), createdAt.Add(30*time.Second))
	require.NoError(t, err)

	var secondAttemptID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO game_attempts (
			slot_id, series_id, roster_id, attempt_number,
			state, created_at, updated_at, started_at
		)
		VALUES ($1, $2, $3, 2, 'active', $4, $4, $4)
		RETURNING id`, chainSlotID, seriesID, rosterID, createdAt.Add(40*time.Second)).Scan(&secondAttemptID)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, secondAttemptID)

	assertSeriesGameStatementRejected(ctx, t, tx, `
		INSERT INTO game_attempts (
			slot_id, series_id, roster_id, attempt_number,
			state, created_at, updated_at, started_at
		)
		VALUES ($1, $2, $3, 2, 'active', $4, $4, $4)`,
		chainSlotID, seriesID, rosterID, createdAt.Add(50*time.Second))

	assertSeriesGameStatementRejected(ctx, t, tx, `
		UPDATE game_attempts
		SET attempt_number = 3
		WHERE id = $1`, secondAttemptID)

	assertSeriesGameStatementRejected(
		ctx, t,
		tx,
		`DELETE FROM game_attempts WHERE id = $1`,
		firstAttemptID,
	)

	assertSeriesGameStatementRejected(ctx, t, tx, `
		UPDATE game_slots
		SET slot_number = 2
		WHERE id = $1`, chainSlotID)
	require.NoError(t, tx.Rollback(ctx))

	concurrentSlotID := createMigrationGameSlot(ctx, t, seriesID, rosterID, 2, "crypto")
	assertOneConcurrentAttempt(ctx, t, concurrentSlotID, seriesID, rosterID, createdAt)
	assertGameTerminalReasons(ctx, t, tournamentID, rosterID, participantIDs[:2], createdAt)
	assertSeriesResultConstraints(ctx, t, tournamentID, rosterID, participantIDs[:2], createdAt)
	assertNoLegacyGameDependency(ctx, t)
}

func assertWaveSeriesReplacementLineage(
	ctx context.Context, t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	seriesID uuid.UUID,
	participantIDs []uuid.UUID,
	createdAt time.Time,
) {
	t.Helper()
	require.Len(t, participantIDs, 2)

	oldWaveID := uuid.New()
	replacementWaveID := uuid.New()
	closedAt := createdAt.Add(time.Second)
	replacementAt := closedAt.Add(time.Second)
	tx, err := migrationPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO waves (
			id, tournament_id, roster_id, revision_id, state,
			created_at, updated_at, closed_at
		)
		VALUES ($1, $2, $3, $4, 'superseded', $5, $6, $6)`,
		oldWaveID, tournamentID, rosterID, uuid.New(), createdAt, closedAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO ready_windows (
			wave_id, roster_id, revision_id, state,
			opened_at, deadline, created_at
		)
		VALUES ($1, $2, $3, 'superseded', $4, $5, $4)`,
		oldWaveID, rosterID, uuid.New(), createdAt, createdAt.Add(30*time.Second))
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		INSERT INTO waves (
			id, tournament_id, roster_id, revision_id, state,
			replaces_wave_id, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, 'planned', $5, $6, $6)`,
		replacementWaveID, tournamentID, rosterID, uuid.New(), oldWaveID, replacementAt)
	require.NoError(t, err)

	for _, waveID := range []uuid.UUID{oldWaveID, replacementWaveID} {
		for _, participantID := range participantIDs {
			_, err = tx.Exec(ctx, `
				INSERT INTO wave_members (
					wave_id, roster_id, participant_id, created_at
				)
				VALUES ($1, $2, $3, $4)`,
				waveID, rosterID, participantID, createdAt)
			require.NoError(t, err)
			_, err = tx.Exec(ctx, `
				INSERT INTO wave_readiness (
					wave_id, roster_id, participant_id, created_at, updated_at
				)
				VALUES ($1, $2, $3, $4, $4)`,
				waveID, rosterID, participantID, createdAt)
			require.NoError(t, err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO wave_series (
				wave_id, tournament_id, roster_id, series_id, created_at
			)
			VALUES ($1, $2, $3, $4, $5)`,
			waveID, tournamentID, rosterID, seriesID, createdAt)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit(ctx))

	var linkedWaves int
	require.NoError(t, migrationPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM wave_series
		WHERE series_id = $1`, seriesID).Scan(&linkedWaves))
	require.Equal(t, 2, linkedWaves)

	_, err = migrationPool.Exec(ctx, `
		INSERT INTO wave_series (
			wave_id, tournament_id, roster_id, series_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5)`,
		replacementWaveID, tournamentID, rosterID, seriesID, replacementAt)
	require.Error(t, err, "the same wave-series pair must stay unique")
}

func assertSeriesGameStatementRejected(
	ctx context.Context, tb testing.TB,
	tx pgx.Tx,
	query string,
	args ...any,
) {
	tb.Helper()

	_, err := tx.Exec(ctx, "SAVEPOINT expected_failure")
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, query, args...)
	require.Error(tb, err)
	_, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT expected_failure")
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, "RELEASE SAVEPOINT expected_failure")
	require.NoError(tb, err)
}

func createGameMigrationSeries(
	ctx context.Context, tb testing.TB,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	format string,
) uuid.UUID {
	tb.Helper()
	require.Len(tb, participantIDs, 2)

	seriesID, err := seriesseed.CreateSeries(ctx, migrationPool, seriesseed.Input{
		TournamentID:   tournamentID,
		RosterID:       rosterID,
		ParticipantIDs: participantIDs,
		Format:         format,
	})
	require.NoError(tb, err)
	return seriesID
}

func createMigrationGameSlot(
	ctx context.Context, tb testing.TB,
	seriesID uuid.UUID,
	rosterID uuid.UUID,
	slotNumber int,
	category string,
) uuid.UUID {
	tb.Helper()

	slotID, err := gameseed.CreateSlot(ctx, migrationPool, gameseed.SlotInput{
		SeriesID:   seriesID,
		RosterID:   rosterID,
		SlotNumber: slotNumber,
		Category:   domain.Category(category),
	})
	require.NoError(tb, err)
	return slotID
}

func createActiveMigrationAttempt(
	ctx context.Context, tb testing.TB,
	slotID uuid.UUID,
	seriesID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	attemptID, err := gameseed.CreateAttempt(ctx, migrationPool, slotID, seriesID, rosterID, createdAt)
	require.NoError(tb, err)
	return attemptID
}

func assertOneConcurrentAttempt(
	ctx context.Context, t *testing.T,
	slotID uuid.UUID,
	seriesID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) {
	t.Helper()

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := migrationPool.Exec(ctx, `
				INSERT INTO game_attempts (
					slot_id, series_id, roster_id, attempt_number,
					state, created_at, updated_at, started_at
				)
				VALUES ($1, $2, $3, 1, 'active', $4, $4, $4)`,
				slotID, seriesID, rosterID, createdAt)
			results <- err
		}()
	}
	close(start)

	successes := 0
	for range 2 {
		if <-results == nil {
			successes++
		}
	}
	require.Equal(t, 1, successes)

	var nonTerminalCount int
	err := migrationPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM game_attempts
		WHERE slot_id = $1
			AND state IN ('planned', 'ready', 'active', 'paused')`, slotID).Scan(&nonTerminalCount)
	require.NoError(t, err)
	require.Equal(t, 1, nonTerminalCount)
}

func assertGameTerminalReasons(
	ctx context.Context, t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	createdAt time.Time,
) {
	t.Helper()

	legal := []struct {
		state  string
		reason string
		winner *uuid.UUID
	}{
		{state: "completed", reason: "solved", winner: &participantIDs[0]},
		{state: "completed", reason: "surrender", winner: &participantIDs[0]},
		{state: "completed", reason: "operator_forfeit", winner: &participantIDs[0]},
		{state: "void", reason: "no_solve"},
		{state: "void", reason: "task_failure"},
		{state: "void", reason: "common_platform_failure"},
		{state: "void", reason: "disconnect"},
		{state: "void", reason: "execution_epoch_break"},
		{state: "cancelled", reason: "no_show"},
		{state: "cancelled", reason: "series_cancelled"},
		{state: "cancelled", reason: "tournament_cancelled"},
		{state: "superseded", reason: "derived_revision_superseded"},
	}

	for _, tt := range legal {
		t.Run(tt.state+"_"+tt.reason, func(t *testing.T) {
			seriesID := createGameMigrationSeries(ctx, t, tournamentID, rosterID, participantIDs, "bo3")
			slotID := createMigrationGameSlot(ctx, t, seriesID, rosterID, 1, "reverse")
			tx, err := migrationPool.Begin(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback(ctx) }()
			_, err = tx.Exec(ctx, `
				INSERT INTO game_attempts (
					slot_id, series_id, roster_id, attempt_number, state,
					result_reason, winner_id, result_revision_id,
					created_at, updated_at, started_at, finished_at
				)
				VALUES ($1, $2, $3, 1, $4, $5, $6, $7, $8, $8, $8, $9)`,
				slotID, seriesID, rosterID, tt.state, tt.reason, tt.winner,
				uuid.New(), createdAt, createdAt.Add(time.Second))
			require.NoError(t, err)
			require.NoError(t, tx.Rollback(ctx))
		})
	}

	invalidSeriesID := createGameMigrationSeries(ctx, t, tournamentID, rosterID, participantIDs, "bo3")
	invalidSlotID := createMigrationGameSlot(ctx, t, invalidSeriesID, rosterID, 1, "pwn")
	invalid := []struct {
		state    string
		reason   any
		winner   any
		revision any
	}{
		{state: "completed", reason: "no_solve", winner: participantIDs[0], revision: uuid.New()},
		{state: "void", reason: "solved", revision: uuid.New()},
		{state: "active", reason: "solved", winner: participantIDs[0], revision: uuid.New()},
		{state: "completed", reason: "solved", revision: uuid.New()},
		{state: "cancelled", reason: "no_show", winner: participantIDs[0], revision: uuid.New()},
		{state: "completed", reason: "solved", winner: participantIDs[0]},
		{state: "void", revision: uuid.New()},
	}
	for _, tt := range invalid {
		_, err := migrationPool.Exec(ctx, `
			INSERT INTO game_attempts (
				slot_id, series_id, roster_id, attempt_number, state,
				result_reason, winner_id, result_revision_id,
				created_at, updated_at, started_at, finished_at
			)
			VALUES ($1, $2, $3, 1, $4, $5, $6, $7, $8, $8, $8, $9)`,
			invalidSlotID, invalidSeriesID, rosterID, tt.state, tt.reason,
			tt.winner, tt.revision, createdAt, createdAt.Add(time.Second))
		require.Error(t, err)
	}
}

func assertSeriesResultConstraints(
	ctx context.Context, t *testing.T,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	createdAt time.Time,
) {
	t.Helper()
	finishedAt := createdAt.Add(time.Minute)

	// TASK-010 owns the valid terminal settlement path because current result
	// and score pointers now require atomic retained evidence. This assertion
	// keeps the earlier migration focused on the valid non-terminal shape.
	createGameMigrationSeries(ctx, t, tournamentID, rosterID, participantIDs, "bo3")

	_, err := migrationPool.Exec(ctx, `
		INSERT INTO series (
			tournament_id, roster_id, first_participant_id, second_participant_id,
			format, state, first_participant_wins, second_participant_wins,
			winner_id, current_score_revision_id, current_result_revision_id,
			created_at, updated_at, started_at, finished_at
		)
		VALUES ($1, $2, $3, $4, 'bo3', 'completed', 2, 1, $4, $5, $6, $7, $8, $7, $8)`,
		tournamentID, rosterID, participantIDs[0], participantIDs[1],
		uuid.New(), uuid.New(), createdAt, finishedAt)
	require.Error(t, err)

	_, err = migrationPool.Exec(ctx, `
		INSERT INTO series (
			tournament_id, roster_id, first_participant_id, second_participant_id,
			format, state, first_participant_wins, winner_id,
			current_score_revision_id, created_at, updated_at, started_at, finished_at
		)
		VALUES ($1, $2, $3, $4, 'bo1', 'completed', 1, $3, $5, $6, $7, $6, $7)`,
		tournamentID, rosterID, participantIDs[0], participantIDs[1],
		uuid.New(), createdAt, finishedAt)
	require.Error(t, err)

	_, err = migrationPool.Exec(ctx, `
		INSERT INTO series (
			tournament_id, roster_id, first_participant_id, second_participant_id,
			format, first_participant_wins
		)
		VALUES ($1, $2, $3, $4, 'bo1', 2)`,
		tournamentID, rosterID, participantIDs[0], participantIDs[1])
	require.Error(t, err)
}

func assertNoLegacyGameDependency(ctx context.Context, tb testing.TB) {
	tb.Helper()

	var legacyForeignKeys int
	err := migrationPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_constraint AS constraint_row
		JOIN pg_class AS source_table ON source_table.oid = constraint_row.conrelid
		JOIN pg_class AS target_table ON target_table.oid = constraint_row.confrelid
		WHERE constraint_row.contype = 'f'
			AND source_table.relname IN ('series', 'game_slots', 'game_attempts')
			AND target_table.relname = 'duels'`).Scan(&legacyForeignKeys)
	require.NoError(tb, err)
	require.Zero(tb, legacyForeignKeys)
}

func resetMigrationTables(ctx context.Context, tb testing.TB) {
	tb.Helper()
	_, err := migrationPool.Exec(ctx, `TRUNCATE TABLE participant_reservations, tournaments CASCADE`)
	require.NoError(tb, err)
}

func createMigrationTournament(ctx context.Context, tb testing.TB) uuid.UUID {
	tb.Helper()
	tournamentID, err := tournamentseed.CreateTournament(ctx, migrationPool)
	require.NoError(tb, err)
	return tournamentID
}

func createMigrationRoster(ctx context.Context, tb testing.TB, tournamentID uuid.UUID) uuid.UUID {
	tb.Helper()
	seed, err := tournamentseed.CreateRoster(ctx, migrationPool, tournamentID)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, seed.Revision)
	return seed.ID
}

func createMigrationPlayers(ctx context.Context, tb testing.TB, count int) []uuid.UUID {
	tb.Helper()
	players, err := tournamentseed.CreatePlayers(ctx, migrationPool, "tournament_migration", count)
	require.NoError(tb, err)
	return players
}

func createSwissMigrationParticipants(
	ctx context.Context,
	tb testing.TB,
	rosterID uuid.UUID,
	playerIDs []uuid.UUID,
) []uuid.UUID {
	tb.Helper()
	participants, err := swissseed.CreateParticipants(ctx, migrationPool, rosterID, playerIDs)
	require.NoError(tb, err)
	return participants
}
