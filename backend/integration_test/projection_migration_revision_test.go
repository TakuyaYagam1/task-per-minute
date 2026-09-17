//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/projectionseed"
)

func createProjectionGoldenSource(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
) uuid.UUID {
	tb.Helper()

	id, err := projectionseed.CreateGoldenSource(ctx, sharedPool, projectionseed.GoldenSourceInput{
		TournamentID:   fixture.tournamentID,
		RosterID:       fixture.rosterID,
		ParticipantIDs: fixture.participantIDs,
		CreatedAt:      fixture.createdAt,
	})
	require.NoError(tb, err)
	return id
}

func createProjectionCutoff(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	sequenceNumber int,
	previousCutoffID any,
	sourceKind string,
	officialResultRevisionID any,
	goldenPositionCommitID any,
	reason string,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	id, err := projectionseed.CreateCutoff(ctx, sharedPool, projectionseed.CutoffInput{
		TournamentID:             fixture.tournamentID,
		RosterID:                 fixture.rosterID,
		SequenceNumber:           sequenceNumber,
		PreviousCutoffID:         previousCutoffID,
		SourceKind:               sourceKind,
		OfficialResultRevisionID: officialResultRevisionID,
		GoldenPositionCommitID:   goldenPositionCommitID,
		Reason:                   reason,
		CreatedAt:                createdAt,
	})
	require.NoError(tb, err)
	return id
}

func createProjectionRevision(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	revisionNumber int,
	previousRevisionID any,
	cutoffID uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	id, err := projectionseed.CreateRevision(ctx, sharedPool, projectionseed.RevisionInput{
		TournamentID:       fixture.tournamentID,
		RosterID:           fixture.rosterID,
		RevisionNumber:     revisionNumber,
		PreviousRevisionID: previousRevisionID,
		CutoffID:           cutoffID,
		CreatedAt:          createdAt,
	})
	require.NoError(tb, err)
	return id
}
