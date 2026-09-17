//go:build integration

package golden

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/goldenseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/swissseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/tournamentseed"
)

func resetMigrationTables(ctx context.Context, tb testing.TB) {
	tb.Helper()
	_, err := migrationPool.Exec(ctx, `TRUNCATE TABLE participant_reservations, tournaments CASCADE`)
	require.NoError(tb, err)
}

func createGoldenMigrationFixture(
	ctx context.Context, tb testing.TB,
	participantCount int,
) goldenMigrationFixture {
	tb.Helper()

	tournamentID, err := tournamentseed.CreateTournament(ctx, migrationPool)
	require.NoError(tb, err)
	roster, err := tournamentseed.CreateRoster(ctx, migrationPool, tournamentID)
	require.NoError(tb, err)
	require.EqualValues(tb, 1, roster.Revision)
	playerIDs, err := tournamentseed.CreatePlayers(ctx, migrationPool, "tournament_migration", participantCount)
	require.NoError(tb, err)
	participantIDs, err := swissseed.CreateParticipants(ctx, migrationPool, roster.ID, playerIDs)
	require.NoError(tb, err)

	return goldenMigrationFixture{
		tournamentID:   tournamentID,
		rosterID:       roster.ID,
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

	id, err := goldenseed.CreateAttempt(ctx, migrationPool, goldenseed.AttemptInput{
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

	id, err := goldenseed.CreateMembership(ctx, migrationPool, goldenseed.MembershipInput{
		AttemptID: attemptID, TournamentID: fixture.tournamentID, RosterID: fixture.rosterID,
		ParticipantID: participantID, SelectionKind: selectionKind,
		ReservePosition: reservePosition, SelectedAt: selectedAt, ReadyAt: readyAt,
		NoShowAt: noShowAt, ExcludedAt: excludedAt, ExclusionReason: exclusionReason,
	})
	require.NoError(tb, err)
	return id
}
