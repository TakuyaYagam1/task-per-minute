//go:build integration

package integration_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/swissseed"
)

func createSwissMigrationParticipants(
	ctx context.Context, tb testing.TB,
	rosterID uuid.UUID,
	playerIDs []uuid.UUID,
) []uuid.UUID {
	tb.Helper()
	participantIDs, err := swissseed.CreateParticipants(ctx, sharedPool, rosterID, playerIDs)
	require.NoError(tb, err)
	return participantIDs
}

func createSwissMigrationPairing(
	ctx context.Context, tb testing.TB,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	slotNumber int,
	overrideID uuid.UUID,
) uuid.UUID {
	tb.Helper()
	var override *uuid.UUID
	if overrideID != uuid.Nil {
		override = &overrideID
	}
	pairingID, err := swissseed.CreatePairing(
		ctx,
		sharedPool,
		roundID,
		rosterID,
		slotNumber,
		override,
	)
	require.NoError(tb, err)
	return pairingID
}

func addSwissMigrationPairingMember(
	ctx context.Context, tb testing.TB,
	pairingID uuid.UUID,
	roundID uuid.UUID,
	rosterID uuid.UUID,
	seat int,
	participantID uuid.UUID,
) {
	tb.Helper()
	err := swissseed.AddPairingMember(
		ctx,
		sharedPool,
		pairingID,
		roundID,
		rosterID,
		seat,
		participantID,
	)
	require.NoError(tb, err)
}
