//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/goldenseed"
)

func createGoldenMigrationFixture(
	ctx context.Context, tb testing.TB,
	participantCount int,
) goldenMigrationFixture {
	tb.Helper()

	tournamentID := createMigrationTournament(ctx, tb)
	rosterID := createMigrationRoster(ctx, tb, tournamentID)
	playerIDs := createMigrationPlayers(ctx, tb, participantCount)
	participantIDs := createSwissMigrationParticipants(ctx, tb, rosterID, playerIDs)

	return goldenMigrationFixture{
		tournamentID:   tournamentID,
		rosterID:       rosterID,
		participantIDs: participantIDs,
		createdAt:      time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Microsecond),
	}
}

func createGoldenAttempt(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	attemptNumber int,
	previousAttemptID any,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	id, err := goldenseed.CreateAttempt(ctx, sharedPool, goldenseed.AttemptInput{
		TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		AttemptNumber: attemptNumber, PreviousAttemptID: previousAttemptID,
		CreatedAt: createdAt,
	})
	require.NoError(tb, err)
	return id
}

func createGoldenMembership(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	attemptID uuid.UUID,
	participantID uuid.UUID,
	selectionKind string,
	reservePosition any,
	selectedAt time.Time,
	readyAt any,
	noShowAt any,
	excludedAt any,
	exclusionReason any,
) uuid.UUID {
	tb.Helper()

	id, err := goldenseed.CreateMembership(ctx, sharedPool, goldenseed.MembershipInput{
		AttemptID: attemptID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ParticipantID: participantID, SelectionKind: selectionKind,
		ReservePosition: reservePosition, SelectedAt: selectedAt, ReadyAt: readyAt,
		NoShowAt: noShowAt, ExcludedAt: excludedAt, ExclusionReason: exclusionReason,
	})
	require.NoError(tb, err)
	return id
}
