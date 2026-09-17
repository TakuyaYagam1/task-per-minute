//go:build integration

package draft

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/seriesseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/swissseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/tournamentseed"
)

func resetDraftRepositoryTables(ctx context.Context, tb testing.TB) {
	tb.Helper()
	_, err := migrationPool.Exec(ctx, `TRUNCATE TABLE tournaments, tasks, players RESTART IDENTITY CASCADE`)
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
	roster, err := tournamentseed.CreateRoster(ctx, migrationPool, tournamentID)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, roster.Revision)
	return roster.ID
}

func createMigrationPlayers(ctx context.Context, tb testing.TB, count int) []uuid.UUID {
	tb.Helper()
	playerIDs, err := tournamentseed.CreatePlayers(ctx, migrationPool, "draft_repository", count)
	require.NoError(tb, err)
	return playerIDs
}

func createSwissMigrationParticipants(
	ctx context.Context,
	tb testing.TB,
	rosterID uuid.UUID,
	playerIDs []uuid.UUID,
) []uuid.UUID {
	tb.Helper()
	participantIDs, err := swissseed.CreateParticipants(ctx, migrationPool, rosterID, playerIDs)
	require.NoError(tb, err)
	return participantIDs
}

func createMigrationSeries(
	ctx context.Context,
	tb testing.TB,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	format string,
) uuid.UUID {
	tb.Helper()
	seriesID, err := seriesseed.CreateSeries(ctx, migrationPool, seriesseed.Input{
		TournamentID:   tournamentID,
		RosterID:       rosterID,
		ParticipantIDs: participantIDs,
		Format:         format,
	})
	require.NoError(tb, err)
	return seriesID
}
