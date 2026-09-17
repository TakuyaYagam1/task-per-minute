//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/gameseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/seriesseed"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func createMigrationSeries(
	ctx context.Context,
	tb testing.TB,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	format string,
) uuid.UUID {
	tb.Helper()
	require.Len(tb, participantIDs, 2)

	seriesID, err := seriesseed.CreateSeries(ctx, sharedPool, seriesseed.Input{
		TournamentID:   tournamentID,
		RosterID:       rosterID,
		ParticipantIDs: participantIDs,
		Format:         format,
	})
	require.NoError(tb, err)
	return seriesID
}

func createMigrationGameSlot(
	ctx context.Context,
	tb testing.TB,
	seriesID uuid.UUID,
	rosterID uuid.UUID,
	slotNumber int,
	category string,
) uuid.UUID {
	tb.Helper()

	slotID, err := gameseed.CreateSlot(ctx, sharedPool, gameseed.SlotInput{
		SeriesID:   seriesID,
		RosterID:   rosterID,
		SlotNumber: slotNumber,
		Category:   domain.Category(category),
	})
	require.NoError(tb, err)
	return slotID
}

func createActiveMigrationAttempt(
	ctx context.Context,
	tb testing.TB,
	slotID uuid.UUID,
	seriesID uuid.UUID,
	rosterID uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	attemptID, err := gameseed.CreateAttempt(ctx, sharedPool, slotID, seriesID, rosterID, createdAt)
	require.NoError(tb, err)
	return attemptID
}
