//go:build integration

package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/gameseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/waveseed"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type reconnectContinuationInput struct {
	id                 uuid.UUID
	continuedFromID    *uuid.UUID
	suspendedByPauseID *uuid.UUID
	owningPauseID      *uuid.UUID
	gameAttemptID      *uuid.UUID
	participantID      uuid.UUID
	presenceEpoch      int64
	intervalNumber     int
	continuationNumber int
	state              string
	openedAt           time.Time
	deadlineAt         time.Time
	closedAt           *time.Time
	revision           int64
	createdAt          time.Time
	updatedAt          time.Time
}

func createMigrationWave(
	ctx context.Context,
	tb testing.TB,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	participantIDs []uuid.UUID,
	createdAt time.Time,
) (uuid.UUID, uuid.UUID) {
	tb.Helper()
	require.GreaterOrEqual(tb, len(participantIDs), 2)

	seed, err := waveseed.CreateWave(ctx, sharedPool, waveseed.Input{
		TournamentID:   tournamentID,
		RosterID:       rosterID,
		ParticipantIDs: participantIDs,
		CreatedAt:      createdAt,
	})
	require.NoError(tb, err)
	return seed.WaveID, seed.RevisionID
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
