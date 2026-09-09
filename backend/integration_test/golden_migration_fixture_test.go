//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
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

	id := uuid.New()
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO golden_attempts (
			id, tournament_id, roster_id, attempt_number,
			previous_attempt_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		id,
		fixture.tournamentID,
		fixture.rosterID,
		attemptNumber,
		previousAttemptID,
		createdAt,
	)
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

	id := uuid.New()
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO golden_memberships (
			id, attempt_id, tournament_id, roster_id, participant_id,
			selection_kind, reserve_position, selected_at,
			ready_at, no_show_at, excluded_at, exclusion_reason
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8,
			$9, $10, $11, $12
		)`,
		id,
		attemptID,
		fixture.tournamentID,
		fixture.rosterID,
		participantID,
		selectionKind,
		reservePosition,
		selectedAt,
		readyAt,
		noShowAt,
		excludedAt,
		exclusionReason,
	)
	require.NoError(tb, err)
	return id
}
