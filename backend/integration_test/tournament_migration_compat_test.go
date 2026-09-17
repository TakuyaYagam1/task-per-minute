//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/tournamentseed"
	tournamentintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/tournament"
)

func resetMigrationTables(ctx context.Context, tb testing.TB) {
	tb.Helper()
	require.NoError(tb, tournamentintegration.ResetMigrationTables(ctx, sharedPool))
}

func createMigrationTournament(ctx context.Context, tb testing.TB) uuid.UUID {
	tb.Helper()
	id, err := tournamentseed.CreateTournament(ctx, sharedPool)
	require.NoError(tb, err)
	return id
}

func createMigrationRoster(ctx context.Context, tb testing.TB, tournamentID uuid.UUID) uuid.UUID {
	tb.Helper()
	seed, err := tournamentseed.CreateRoster(ctx, sharedPool, tournamentID)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, seed.Revision)
	return seed.ID
}

func createMigrationPlayers(ctx context.Context, tb testing.TB, count int) []uuid.UUID {
	tb.Helper()
	ids, err := tournamentseed.CreatePlayers(ctx, sharedPool, "tournament_migration", count)
	require.NoError(tb, err)
	return ids
}
