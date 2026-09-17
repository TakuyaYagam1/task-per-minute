//go:build integration

package assignment

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/gameseed"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func createMigrationGameSlot(
	ctx context.Context,
	tb testing.TB,
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
	ctx context.Context,
	tb testing.TB,
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
