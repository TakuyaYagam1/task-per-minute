//go:build integration

package tournament

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/tournamentseed"
)

func RunTournamentRosterMigration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	require.NotNil(t, pool)

	ctx := context.Background()
	resetMigrationTables(ctx, pool, t)
	t.Cleanup(func() { resetMigrationTables(ctx, pool, t) })

	tournamentID := createMigrationTournament(ctx, pool, t)
	rosterID := createMigrationRoster(ctx, pool, t, tournamentID)
	playerIDs := createMigrationPlayers(ctx, pool, t, 6)

	attendanceStates := []string{"invited", "registered", "checked_in", "withdrawn"}
	for i, attendance := range attendanceStates {
		_, err := pool.Exec(ctx, `
			INSERT INTO participants (roster_id, player_id, seed, attendance)
			VALUES ($1, $2, $3, $4)`, rosterID, playerIDs[i], i+1, attendance)
		require.NoError(t, err)
	}

	var defaultAttendance string
	err := pool.QueryRow(ctx, `
		INSERT INTO participants (roster_id, player_id, seed)
		VALUES ($1, $2, 5)
		RETURNING attendance`, rosterID, playerIDs[4]).Scan(&defaultAttendance)
	require.NoError(t, err)
	require.Equal(t, "invited", defaultAttendance)

	_, err = pool.Exec(ctx, `
		INSERT INTO participants (roster_id, player_id, seed)
		VALUES ($1, $2, 6)`, rosterID, playerIDs[0])
	require.Error(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO participants (roster_id, player_id, seed)
		VALUES ($1, $2, 5)`, rosterID, playerIDs[5])
	require.Error(t, err)

	_, err = pool.Exec(ctx, `
		UPDATE participants
		SET attendance = 'unknown'
		WHERE roster_id = $1 AND player_id = $2`, rosterID, playerIDs[0])
	require.Error(t, err)

	startedAt := time.Now().UTC().Truncate(time.Microsecond)
	_, err = pool.Exec(ctx, `
		UPDATE rosters
		SET execution_started_at = $2
		WHERE id = $1`, rosterID, startedAt)
	require.Error(t, err)

	_, err = pool.Exec(ctx, `
		UPDATE rosters
		SET locked_at = $2, execution_started_at = $2, revision = revision + 1, updated_at = $2
		WHERE id = $1`, rosterID, startedAt)
	require.NoError(t, err)

	assertParticipantReservationBoundary(ctx, t, pool, tournamentID, playerIDs[0], playerIDs[1])
}

func createMigrationTournament(ctx context.Context, pool *pgxpool.Pool, tb testing.TB) uuid.UUID {
	tb.Helper()
	id, err := tournamentseed.CreateTournament(ctx, pool)
	require.NoError(tb, err)
	return id
}

func createMigrationRoster(ctx context.Context, pool *pgxpool.Pool, tb testing.TB, tournamentID uuid.UUID) uuid.UUID {
	tb.Helper()
	seed, err := tournamentseed.CreateRoster(ctx, pool, tournamentID)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, seed.Revision)
	return seed.ID
}

func createMigrationPlayers(ctx context.Context, pool *pgxpool.Pool, tb testing.TB, count int) []uuid.UUID {
	tb.Helper()
	ids, err := tournamentseed.CreatePlayers(ctx, pool, "tournament_migration", count)
	require.NoError(tb, err)
	return ids
}

func assertParticipantReservationBoundary(
	ctx context.Context, t *testing.T, pool *pgxpool.Pool,
	tournamentID uuid.UUID,
	tournamentPlayerID uuid.UUID,
	otherPlayerID uuid.UUID,
) {
	t.Helper()

	var (
		reservationID uuid.UUID
		revision      int64
		acquiredAt    time.Time
		updatedAt     time.Time
	)
	err := pool.QueryRow(ctx, `
		INSERT INTO participant_reservations (
			player_id, tournament_id
		)
		VALUES ($1, $2)
		RETURNING reservation_id, revision, acquired_at, updated_at`,
		tournamentPlayerID, tournamentID,
	).Scan(&reservationID, &revision, &acquiredAt, &updatedAt)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, reservationID)
	require.EqualValues(t, 1, revision)
	require.Equal(t, acquiredAt, updatedAt)

	_, err = pool.Exec(ctx, `
		INSERT INTO participant_reservations (player_id, tournament_id)
		VALUES ($1, $2)`, otherPlayerID, uuid.New())
	require.Error(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO participant_reservations (
			player_id, tournament_id
		)
		VALUES ($1, $2)`, tournamentPlayerID, tournamentID)
	require.Error(t, err)
}
