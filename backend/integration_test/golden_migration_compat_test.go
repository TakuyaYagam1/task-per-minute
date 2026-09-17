//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	goldenintegration "github.com/TakuyaYagam1/task-per-minute/integration_test/golden"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/swissseed"
)

type goldenMigrationFixture struct {
	tournamentID   uuid.UUID
	rosterID       uuid.UUID
	participantIDs []uuid.UUID
	createdAt      time.Time
}

func TestGoldenMigration(t *testing.T) {
	goldenintegration.RunGoldenMigration(t, sharedPool)
}

func createGoldenMigrationFixture(
	ctx context.Context,
	tb testing.TB,
	participantCount int,
) goldenMigrationFixture {
	tb.Helper()

	tournamentID := createMigrationTournament(ctx, tb)
	rosterID := createMigrationRoster(ctx, tb, tournamentID)
	playerIDs := createMigrationPlayers(ctx, tb, participantCount)
	participantIDs, err := swissseed.CreateParticipants(ctx, sharedPool, rosterID, playerIDs)
	require.NoError(tb, err)

	return goldenMigrationFixture{
		tournamentID:   tournamentID,
		rosterID:       rosterID,
		participantIDs: participantIDs,
		createdAt:      time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Microsecond),
	}
}
