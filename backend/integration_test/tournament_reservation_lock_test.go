//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	rosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/roster"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestRosterLockReusesOnlyOwnedReservations(t *testing.T) {
	ctx := t.Context()
	pool, _ := SetupTestDB(t)

	t.Run("reuses same tournament reservations and adds missing player", func(t *testing.T) {
		require.NoError(t, truncateTables(ctx, pool))

		tournamentID, rosterID, playerIDs := createReservationLockFixture(ctx, t, pool)

		acquiredAt := time.Date(2026, time.September, 27, 12, 0, 0, 0, time.UTC)
		updatedAt := acquiredAt.Add(time.Second)
		for _, playerID := range playerIDs[:3] {
			insertTournamentReservation(ctx, t, pool, playerID, tournamentID, 7, acquiredAt, updatedAt)
		}

		lockedAt := updatedAt.Add(time.Second)
		repository := rosterrepo.NewRosterPostgres(postgres.NewTxManager(pool))
		locked, changed, err := repository.LockRosterAndReserve(ctx, rosterID, 1, lockedAt)
		require.NoError(t, err)
		require.True(t, changed)
		require.EqualValues(t, 2, locked.Revision)

		reservations, err := repository.ListReservations(ctx, tournamentID)
		require.NoError(t, err)
		require.Len(t, reservations, 4)
		byPlayer := reservationRecordsByPlayer(reservations)
		for _, playerID := range playerIDs[:3] {
			reservation := byPlayer[playerID]
			require.EqualValues(t, 7, reservation.Revision)
			requireSameInstant(t, acquiredAt, reservation.AcquiredAt)
			requireSameInstant(t, updatedAt, reservation.UpdatedAt)
		}
		reservation := byPlayer[playerIDs[3]]
		require.EqualValues(t, 1, reservation.Revision)
		requireSameInstant(t, lockedAt, reservation.AcquiredAt)
		requireSameInstant(t, lockedAt, reservation.UpdatedAt)
	})

	t.Run("foreign reservation aborts and preserves all original rows", func(t *testing.T) {
		require.NoError(t, truncateTables(ctx, pool))

		tournamentID, rosterID, playerIDs := createReservationLockFixture(ctx, t, pool)
		foreignTournamentID := createReservationLockTournament(ctx, t, pool)

		acquiredAt := time.Date(2026, time.September, 27, 13, 0, 0, 0, time.UTC)
		updatedAt := acquiredAt.Add(time.Second)
		for _, playerID := range playerIDs[:2] {
			insertTournamentReservation(ctx, t, pool, playerID, tournamentID, 7, acquiredAt, updatedAt)
		}
		foreignAcquiredAt := acquiredAt.Add(2 * time.Second)
		foreignUpdatedAt := foreignAcquiredAt.Add(time.Second)
		insertTournamentReservation(ctx, t, pool, playerIDs[2], foreignTournamentID, 9, foreignAcquiredAt, foreignUpdatedAt)

		lockedAt := foreignUpdatedAt.Add(time.Second)
		repository := rosterrepo.NewRosterPostgres(postgres.NewTxManager(pool))
		locked, changed, err := repository.LockRosterAndReserve(ctx, rosterID, 1, lockedAt)
		require.ErrorIs(t, err, domain.ErrConflict)
		require.Nil(t, locked)
		require.False(t, changed)

		roster, err := repository.GetRoster(ctx, rosterID)
		require.NoError(t, err)
		require.EqualValues(t, 1, roster.Revision)
		require.Nil(t, roster.LockedAt)

		reservations, err := repository.ListReservations(ctx, tournamentID)
		require.NoError(t, err)
		require.Len(t, reservations, 2)
		for _, reservation := range reservations {
			require.EqualValues(t, 7, reservation.Revision)
			requireSameInstant(t, acquiredAt, reservation.AcquiredAt)
			requireSameInstant(t, updatedAt, reservation.UpdatedAt)
		}

		foreignReservations, err := repository.ListReservations(ctx, foreignTournamentID)
		require.NoError(t, err)
		require.Len(t, foreignReservations, 1)
		require.Equal(t, playerIDs[2], foreignReservations[0].PlayerID)
		require.EqualValues(t, 9, foreignReservations[0].Revision)
		requireSameInstant(t, foreignAcquiredAt, foreignReservations[0].AcquiredAt)
		requireSameInstant(t, foreignUpdatedAt, foreignReservations[0].UpdatedAt)

		var unreservedCount int
		err = pool.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM participant_reservations
			WHERE player_id = $1`, playerIDs[3]).Scan(&unreservedCount)
		require.NoError(t, err)
		require.Zero(t, unreservedCount)
	})
}

func createReservationLockFixture(
	ctx context.Context,
	tb testing.TB,
	pool *pgxpool.Pool,
) (uuid.UUID, uuid.UUID, []uuid.UUID) {
	tb.Helper()
	tournamentID := createReservationLockTournament(ctx, tb, pool)
	var rosterID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO rosters (tournament_id)
		VALUES ($1)
		RETURNING id`, tournamentID).Scan(&rosterID)
	require.NoError(tb, err)

	playerIDs := make([]uuid.UUID, 4)
	for index := range playerIDs {
		err = pool.QueryRow(ctx, `
			INSERT INTO players (username)
			VALUES ($1)
			RETURNING id`, fmt.Sprintf("reservation-player-%d", index+1)).Scan(&playerIDs[index])
		require.NoError(tb, err)
		_, err = pool.Exec(ctx, `
			INSERT INTO participants (roster_id, player_id, seed, attendance)
			VALUES ($1, $2, $3, 'checked_in')`, rosterID, playerIDs[index], index+1)
		require.NoError(tb, err)
	}
	return tournamentID, rosterID, playerIDs
}

func createReservationLockTournament(ctx context.Context, tb testing.TB, pool *pgxpool.Pool) uuid.UUID {
	tb.Helper()
	var tournamentID uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO tournaments DEFAULT VALUES
		RETURNING id`).Scan(&tournamentID)
	require.NoError(tb, err)
	return tournamentID
}

func insertTournamentReservation(
	ctx context.Context,
	tb testing.TB,
	pool *pgxpool.Pool,
	playerID uuid.UUID,
	tournamentID uuid.UUID,
	revision int64,
	acquiredAt time.Time,
	updatedAt time.Time,
) {
	tb.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO participant_reservations (
			player_id, tournament_id, revision, acquired_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5)`,
		playerID, tournamentID, revision, acquiredAt, updatedAt,
	)
	require.NoError(tb, err)
}

func reservationRecordsByPlayer(
	reservations []rosterrepo.ReservationRecord,
) map[uuid.UUID]rosterrepo.ReservationRecord {
	byPlayer := make(map[uuid.UUID]rosterrepo.ReservationRecord, len(reservations))
	for _, reservation := range reservations {
		byPlayer[reservation.PlayerID] = reservation
	}
	return byPlayer
}

func requireSameInstant(tb testing.TB, expected, actual time.Time) {
	tb.Helper()
	require.Truef(tb, expected.Equal(actual), "expected %s, got %s", expected, actual)
}
