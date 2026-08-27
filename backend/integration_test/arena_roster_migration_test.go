//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestArenaRosterMigration(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	tournamentID := createArenaMigrationTournament(t, ctx)
	rosterID := createArenaMigrationRoster(t, ctx, tournamentID)
	playerIDs := createArenaMigrationPlayers(t, ctx, 6)

	attendanceStates := []string{"invited", "registered", "checked_in", "withdrawn"}
	for i, attendance := range attendanceStates {
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO arena_participants (roster_id, player_id, seed, attendance)
			VALUES ($1, $2, $3, $4)`, rosterID, playerIDs[i], i+1, attendance)
		require.NoError(t, err)
	}

	var defaultAttendance string
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO arena_participants (roster_id, player_id, seed)
		VALUES ($1, $2, 5)
		RETURNING attendance`, rosterID, playerIDs[4]).Scan(&defaultAttendance)
	require.NoError(t, err)
	require.Equal(t, "invited", defaultAttendance)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_participants (roster_id, player_id, seed)
		VALUES ($1, $2, 6)`, rosterID, playerIDs[0])
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_participants (roster_id, player_id, seed)
		VALUES ($1, $2, 5)`, rosterID, playerIDs[5])
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_participants
		SET attendance = 'unknown'
		WHERE roster_id = $1 AND player_id = $2`, rosterID, playerIDs[0])
	require.Error(t, err)

	startedAt := time.Now().UTC().Truncate(time.Microsecond)
	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_rosters
		SET execution_started_at = $2
		WHERE id = $1`, rosterID, startedAt)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_rosters
		SET locked_at = $2, execution_started_at = $2, revision = revision + 1, updated_at = $2
		WHERE id = $1`, rosterID, startedAt)
	require.NoError(t, err)

	duelID := createArenaMigrationDuel(t, ctx, playerIDs[0], playerIDs[1])
	assertParticipantReservationBoundary(t, ctx, tournamentID, duelID, playerIDs[0], playerIDs[1])
}

func createArenaMigrationTournament(t testing.TB, ctx context.Context) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO arena_tournaments DEFAULT VALUES
		RETURNING id`).Scan(&id)
	require.NoError(t, err)
	return id
}

func createArenaMigrationRoster(t testing.TB, ctx context.Context, tournamentID uuid.UUID) uuid.UUID {
	t.Helper()
	var (
		id       uuid.UUID
		revision int64
	)
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO arena_rosters (tournament_id)
		VALUES ($1)
		RETURNING id, revision`, tournamentID).Scan(&id, &revision)
	require.NoError(t, err)
	require.EqualValues(t, 1, revision)
	return id
}

func createArenaMigrationPlayers(t testing.TB, ctx context.Context, count int) []uuid.UUID {
	t.Helper()
	ids := make([]uuid.UUID, count)
	for i := range ids {
		err := sharedPool.QueryRow(ctx, `
			INSERT INTO players (username)
			VALUES ($1)
			RETURNING id`, fmt.Sprintf("arena_migration_%s_%d", uuid.NewString()[:8], i)).Scan(&ids[i])
		require.NoError(t, err)
	}
	return ids
}

func createArenaMigrationDuel(
	t testing.TB,
	ctx context.Context,
	player1ID uuid.UUID,
	player2ID uuid.UUID,
) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO duels (player1_id, player2_id, deadline)
		VALUES ($1, $2, $3)
		RETURNING id`, player1ID, player2ID, time.Now().UTC().Add(time.Hour)).Scan(&id)
	require.NoError(t, err)
	return id
}

func assertParticipantReservationBoundary(
	t *testing.T,
	ctx context.Context,
	tournamentID uuid.UUID,
	duelID uuid.UUID,
	arenaPlayerID uuid.UUID,
	queuePlayerID uuid.UUID,
) {
	t.Helper()

	var (
		reservationID uuid.UUID
		revision      int64
		acquiredAt    time.Time
		updatedAt     time.Time
	)
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO participant_reservations (
			player_id, owner_kind, owner_id, arena_tournament_id
		)
		VALUES ($1, 'arena', $2, $2)
		RETURNING reservation_id, revision, acquired_at, updated_at`,
		arenaPlayerID, tournamentID,
	).Scan(&reservationID, &revision, &acquiredAt, &updatedAt)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, reservationID)
	require.EqualValues(t, 1, revision)
	require.Equal(t, acquiredAt, updatedAt)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO participant_reservations (
			player_id, owner_kind, owner_id, casual_duel_id
		)
		VALUES ($1, 'casual_duel', $2, $2)`, arenaPlayerID, duelID)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		DELETE FROM participant_reservations
		WHERE player_id = $1 AND owner_kind = 'arena' AND owner_id = $2`,
		arenaPlayerID, tournamentID)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO participant_reservations (
			player_id, owner_kind, owner_id, casual_duel_id
		)
		VALUES ($1, 'casual_duel', $2, $2)`, arenaPlayerID, duelID)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO participant_reservations (
			player_id, owner_kind, owner_id, arena_tournament_id
		)
		VALUES ($1, 'arena', $2, $2)`, arenaPlayerID, tournamentID)
	require.Error(t, err)

	queueOwnerID := uuid.New()
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO participant_reservations (player_id, owner_kind, owner_id)
		VALUES ($1, 'casual_queue', $2)`, queuePlayerID, queueOwnerID)
	require.NoError(t, err)

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO participant_reservations (
			player_id, owner_kind, owner_id, arena_tournament_id
		)
		VALUES ($1, 'arena', $2, $2)`, queuePlayerID, tournamentID)
	require.Error(t, err)

	_, err = sharedPool.Exec(ctx, `
		UPDATE participant_reservations
		SET owner_kind = 'casual_duel', owner_id = $2
		WHERE player_id = $1`, queuePlayerID, duelID)
	require.Error(t, err)
}
