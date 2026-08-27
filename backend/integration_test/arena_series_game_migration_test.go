//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestArenaSeriesGameMigration(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	tournamentID := createArenaMigrationTournament(t, ctx)
	rosterID := createArenaMigrationRoster(t, ctx, tournamentID)
	playerIDs := createArenaMigrationPlayers(t, ctx, 3)
	participantIDs := createSwissMigrationParticipants(t, ctx, rosterID, playerIDs)
	seriesID := createArenaMigrationSeries(t, ctx, tournamentID, rosterID, participantIDs[:2], "bo3")
	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)

	chainSlotID := createArenaMigrationGameSlot(t, ctx, seriesID, rosterID, 1, "web")
	firstAttemptID := createActiveArenaMigrationAttempt(t, ctx, chainSlotID, seriesID, rosterID, createdAt)

	_, err := sharedPool.Exec(ctx, `
		UPDATE arena_game_attempts
		SET state = 'void',
			result_reason = 'no_solve',
			result_revision_id = $2,
			revision = revision + 1,
			updated_at = $3,
			finished_at = $3
		WHERE id = $1`, firstAttemptID, uuid.New(), createdAt.Add(30*time.Second))
	require.NoError(t, err)

	var secondAttemptID uuid.UUID
	err = sharedPool.QueryRow(ctx, `
		INSERT INTO arena_game_attempts (
			slot_id, series_id, roster_id, attempt_number,
			state, created_at, updated_at, started_at
		)
		VALUES ($1, $2, $3, 2, 'active', $4, $4, $4)
		RETURNING id`, chainSlotID, seriesID, rosterID, createdAt.Add(40*time.Second)).Scan(&secondAttemptID)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, secondAttemptID)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_game_attempts (
			slot_id, series_id, roster_id, attempt_number,
			state, created_at, updated_at, started_at
		)
		VALUES ($1, $2, $3, 2, 'active', $4, $4, $4)`,
		chainSlotID, seriesID, rosterID, createdAt.Add(50*time.Second))
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_game_attempts
		SET attempt_number = 3
		WHERE id = $1`, secondAttemptID)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `DELETE FROM arena_game_attempts WHERE id = $1`, firstAttemptID)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_game_slots
		SET slot_number = 2
		WHERE id = $1`, chainSlotID)
	require.Error(t, err)

	concurrentSlotID := createArenaMigrationGameSlot(t, ctx, seriesID, rosterID, 2, "crypto")
	assertOneConcurrentArenaAttempt(t, ctx, concurrentSlotID, seriesID, rosterID, createdAt)
	assertArenaGameTerminalReasons(t, ctx, tournamentID, rosterID, participantIDs[:2], createdAt)
	assertArenaSeriesResultConstraints(t, ctx, tournamentID, rosterID, participantIDs[:2], createdAt)
	assertNoArenaDuelDependency(t, ctx)
}

func createArenaMigrationSeries(
	t testing.TB,
	ctx context.Context,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	format string,
) uuid.UUID {
	t.Helper()
	require.Len(t, participantIDs, 2)

	var seriesID uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO arena_series (
			tournament_id, roster_id, first_participant_id, second_participant_id, format
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, tournamentID, rosterID, participantIDs[0], participantIDs[1], format).Scan(&seriesID)
	require.NoError(t, err)
	return seriesID
}

func createArenaMigrationGameSlot(
	t testing.TB,
	ctx context.Context,
	seriesID uuid.UUID,
	rosterID uuid.UUID,
	slotNumber int,
	category string,
) uuid.UUID {
	t.Helper()

	var slotID uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO arena_game_slots (series_id, roster_id, slot_number, category)
		VALUES ($1, $2, $3, $4)
		RETURNING id`, seriesID, rosterID, slotNumber, category).Scan(&slotID)
	require.NoError(t, err)
	return slotID
}

func createActiveArenaMigrationAttempt(
	t testing.TB,
	ctx context.Context,
	slotID uuid.UUID,
	seriesID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	var attemptID uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO arena_game_attempts (
			slot_id, series_id, roster_id, attempt_number,
			state, created_at, updated_at, started_at
		)
		VALUES ($1, $2, $3, 1, 'active', $4, $4, $4)
		RETURNING id`, slotID, seriesID, rosterID, createdAt).Scan(&attemptID)
	require.NoError(t, err)
	return attemptID
}

func assertOneConcurrentArenaAttempt(
	t *testing.T,
	ctx context.Context,
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
			_, err := sharedPool.Exec(ctx, `
				INSERT INTO arena_game_attempts (
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
	err := sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_game_attempts
		WHERE slot_id = $1
			AND state IN ('planned', 'ready', 'active', 'paused')`, slotID).Scan(&nonTerminalCount)
	require.NoError(t, err)
	require.Equal(t, 1, nonTerminalCount)
}

func assertArenaGameTerminalReasons(
	t *testing.T,
	ctx context.Context,
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
			seriesID := createArenaMigrationSeries(t, ctx, tournamentID, rosterID, participantIDs, "bo3")
			slotID := createArenaMigrationGameSlot(t, ctx, seriesID, rosterID, 1, "reverse")
			_, err := sharedPool.Exec(ctx, `
				INSERT INTO arena_game_attempts (
					slot_id, series_id, roster_id, attempt_number, state,
					result_reason, winner_id, result_revision_id,
					created_at, updated_at, started_at, finished_at
				)
				VALUES ($1, $2, $3, 1, $4, $5, $6, $7, $8, $8, $8, $9)`,
				slotID, seriesID, rosterID, tt.state, tt.reason, tt.winner,
				uuid.New(), createdAt, createdAt.Add(time.Second))
			require.NoError(t, err)
		})
	}

	invalidSeriesID := createArenaMigrationSeries(t, ctx, tournamentID, rosterID, participantIDs, "bo3")
	invalidSlotID := createArenaMigrationGameSlot(t, ctx, invalidSeriesID, rosterID, 1, "pwn")
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
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO arena_game_attempts (
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

func assertArenaSeriesResultConstraints(
	t *testing.T,
	ctx context.Context,
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
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_series (
			tournament_id, roster_id, first_participant_id, second_participant_id,
			format, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, 'bo3', $5, $5)`,
		tournamentID, rosterID, participantIDs[0], participantIDs[1], createdAt)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_series (
			tournament_id, roster_id, first_participant_id, second_participant_id,
			format, state, first_participant_wins, second_participant_wins,
			winner_id, current_score_revision_id, current_result_revision_id,
			created_at, updated_at, started_at, finished_at
		)
		VALUES ($1, $2, $3, $4, 'bo3', 'completed', 2, 1, $4, $5, $6, $7, $8, $7, $8)`,
		tournamentID, rosterID, participantIDs[0], participantIDs[1],
		uuid.New(), uuid.New(), createdAt, finishedAt)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_series (
			tournament_id, roster_id, first_participant_id, second_participant_id,
			format, state, first_participant_wins, winner_id,
			current_score_revision_id, created_at, updated_at, started_at, finished_at
		)
		VALUES ($1, $2, $3, $4, 'bo1', 'completed', 1, $3, $5, $6, $7, $6, $7)`,
		tournamentID, rosterID, participantIDs[0], participantIDs[1],
		uuid.New(), createdAt, finishedAt)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_series (
			tournament_id, roster_id, first_participant_id, second_participant_id,
			format, first_participant_wins
		)
		VALUES ($1, $2, $3, $4, 'bo1', 2)`,
		tournamentID, rosterID, participantIDs[0], participantIDs[1])
	require.Error(t, err)
}

func assertNoArenaDuelDependency(t testing.TB, ctx context.Context) {
	t.Helper()

	var duelForeignKeys int
	err := sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM pg_constraint AS constraint_row
		JOIN pg_class AS source_table ON source_table.oid = constraint_row.conrelid
		JOIN pg_class AS target_table ON target_table.oid = constraint_row.confrelid
		WHERE constraint_row.contype = 'f'
			AND source_table.relname IN ('arena_series', 'arena_game_slots', 'arena_game_attempts')
			AND target_table.relname = 'duels'`).Scan(&duelForeignKeys)
	require.NoError(t, err)
	require.Zero(t, duelForeignKeys)
}
