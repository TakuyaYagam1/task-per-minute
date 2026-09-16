//go:build integration

package game_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func resetMigrationTables(ctx context.Context, tb testing.TB) {
	tb.Helper()
	_, err := sharedPool.Exec(ctx, `TRUNCATE TABLE participant_reservations, tournaments CASCADE`)
	require.NoError(tb, err)
}

func createMigrationTournament(ctx context.Context, tb testing.TB) uuid.UUID {
	tb.Helper()
	var id uuid.UUID
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO tournaments DEFAULT VALUES
		RETURNING id`).Scan(&id)
	require.NoError(tb, err)
	return id
}

func createMigrationRoster(ctx context.Context, tb testing.TB, tournamentID uuid.UUID) uuid.UUID {
	tb.Helper()
	var (
		id       uuid.UUID
		revision int64
	)
	err := sharedPool.QueryRow(ctx, `
		INSERT INTO rosters (tournament_id)
		VALUES ($1)
		RETURNING id, revision`, tournamentID).Scan(&id, &revision)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, revision)
	return id
}

func createMigrationPlayers(ctx context.Context, tb testing.TB, count int) []uuid.UUID {
	tb.Helper()
	ids := make([]uuid.UUID, count)
	for i := range ids {
		err := sharedPool.QueryRow(ctx, `
			INSERT INTO players (username)
			VALUES ($1)
			RETURNING id`, fmt.Sprintf("tournament_migration_%s_%d", uuid.NewString()[:8], i)).Scan(&ids[i])
		require.NoError(tb, err)
	}
	return ids
}

func createSwissMigrationParticipants(
	ctx context.Context, tb testing.TB,
	rosterID uuid.UUID,
	playerIDs []uuid.UUID,
) []uuid.UUID {
	tb.Helper()
	participantIDs := make([]uuid.UUID, len(playerIDs))
	for i, playerID := range playerIDs {
		err := sharedPool.QueryRow(ctx, `
			INSERT INTO participants (roster_id, player_id, seed, attendance)
			VALUES ($1, $2, $3, 'checked_in')
			RETURNING id`, rosterID, playerID, i+1).Scan(&participantIDs[i])
		require.NoError(tb, err)
	}
	return participantIDs
}

func createMigrationSeries(
	ctx context.Context, tb testing.TB,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	format string,
) uuid.UUID {
	tb.Helper()
	require.Len(tb, participantIDs, 2)

	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, "SET CONSTRAINTS ALL DEFERRED")
	require.NoError(tb, err)

	var sourceProjectionID uuid.UUID
	var sourceProjectionRevision int64
	err = tx.QueryRow(ctx, `
		SELECT id, revision_number
		FROM projection_revisions
		WHERE tournament_id = $1
			AND roster_id = $2
		ORDER BY revision_number DESC
		LIMIT 1
		FOR KEY SHARE`, tournamentID, rosterID).Scan(&sourceProjectionID, &sourceProjectionRevision)
	if err != nil {
		require.ErrorIs(tb, err, pgx.ErrNoRows)

		cutoffID := uuid.New()
		sourceProjectionID = uuid.New()
		_, err = tx.Exec(ctx, `
			INSERT INTO projection_cutoffs (
				id, tournament_id, roster_id, sequence_number, source_kind,
				reason, cutoff_at, created_at
			)
			VALUES ($1, $2, $3, 1, 'initial', 'series genesis source', $4, $4)`,
			cutoffID,
			tournamentID,
			rosterID,
			createdAt,
		)
		require.NoError(tb, err)

		_, err = tx.Exec(ctx, `
			INSERT INTO projection_revisions (
				id, tournament_id, roster_id, revision_number, cutoff_id, created_at
			)
			VALUES ($1, $2, $3, 1, $4, $5)`,
			sourceProjectionID,
			tournamentID,
			rosterID,
			cutoffID,
			createdAt,
		)
		require.NoError(tb, err)
		sourceProjectionRevision = 1
	}

	var seriesID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO series (
			tournament_id, roster_id, first_participant_id, second_participant_id, format,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		RETURNING id`, tournamentID, rosterID, participantIDs[0], participantIDs[1], format, createdAt).Scan(&seriesID)
	require.NoError(tb, err)

	genesisScoreRevisionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO series_score_revisions (
			id, tournament_id, roster_id, series_id,
			revision_number, operation, command_id, actor_kind,
			source_projection_revision_id, source_projection_revision,
			first_participant_wins, second_participant_wins, created_at
		)
		VALUES ($1, $2, $3, $4, 1, 'initialize', $5, 'server', $6, $7, 0, 0, $8)`,
		genesisScoreRevisionID,
		tournamentID,
		rosterID,
		seriesID,
		uuid.New(),
		sourceProjectionID,
		sourceProjectionRevision,
		createdAt,
	)
	require.NoError(tb, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO series_score_heads (
			series_id, roster_id, current_revision_id, updated_at
		)
		VALUES ($1, $2, $3, $4)`, seriesID, rosterID, genesisScoreRevisionID, createdAt)
	require.NoError(tb, err)

	_, err = tx.Exec(ctx, `
		UPDATE series
		SET current_score_revision_id = $2,
			updated_at = $3
		WHERE id = $1`, seriesID, genesisScoreRevisionID, createdAt)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
	return seriesID
}
