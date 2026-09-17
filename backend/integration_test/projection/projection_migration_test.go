//go:build integration

package projection_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/projectionseed"
)

func TestProjectionMigrationRejectsChampionBeforeTerminalFinal(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, resetProjectionTables(ctx, sharedPool))
	t.Cleanup(func() { require.NoError(t, resetProjectionTables(ctx, sharedPool)) })

	fixture, err := createProjectionFixture(ctx, sharedPool, 4)
	require.NoError(t, err)
	goldenPositionCommitID, err := projectionseed.CreateGoldenSource(ctx, sharedPool, projectionseed.GoldenSourceInput{
		TournamentID:   fixture.tournamentID,
		RosterID:       fixture.rosterID,
		ParticipantIDs: fixture.participantIDs,
		CreatedAt:      fixture.createdAt,
	})
	require.NoError(t, err)

	createdAt := fixture.createdAt.Add(4 * time.Minute)
	cutoffID, err := projectionseed.CreateCutoff(ctx, sharedPool, projectionseed.CutoffInput{
		TournamentID:             fixture.tournamentID,
		RosterID:                 fixture.rosterID,
		SequenceNumber:           1,
		PreviousCutoffID:         nil,
		SourceKind:               "golden_position",
		OfficialResultRevisionID: nil,
		GoldenPositionCommitID:   goldenPositionCommitID,
		Reason:                   "planned playoff projection",
		CreatedAt:                createdAt,
	})
	require.NoError(t, err)
	revisionID, err := projectionseed.CreateRevision(ctx, sharedPool, projectionseed.RevisionInput{
		TournamentID:       fixture.tournamentID,
		RosterID:           fixture.rosterID,
		RevisionNumber:     1,
		PreviousRevisionID: nil,
		CutoffID:           cutoffID,
		CreatedAt:          createdAt.Add(time.Second),
	})
	require.NoError(t, err)
	artifacts, err := createProjectionArtifactSet(
		ctx, sharedPool, fixture, revisionID, "planned", createdAt.Add(2*time.Second),
	)
	require.NoError(t, err)
	for kind, artifactID := range artifacts {
		require.NoError(t, linkProjectionArtifact(
			ctx, sharedPool, fixture, revisionID, kind, artifactID, "produced",
		))
	}
	require.NoError(t, createProjectionGoldenDependency(
		ctx, sharedPool, fixture, artifacts["standings"], goldenPositionCommitID,
	))
	require.NoError(t, createProjectionArtifactDependency(
		ctx, sharedPool, fixture, artifacts["bracket"], artifacts["standings"],
	))
	require.NoError(t, createProjectionArtifactDependency(
		ctx, sharedPool, fixture, artifacts["top_four"], artifacts["bracket"],
	))

	championID, err := createProjectionArtifact(
		ctx, sharedPool, fixture, revisionID, "champion", "champion-planned",
		`{"participant_id":"`+fixture.participantIDs[0].String()+`"}`,
		14, createdAt.Add(2*time.Second),
	)
	require.NoError(t, err)
	require.NoError(t, linkProjectionArtifact(
		ctx, sharedPool, fixture, revisionID, "champion", championID, "produced",
	))
	require.NoError(t, createProjectionArtifactDependency(
		ctx, sharedPool, fixture, championID, artifacts["bracket"],
	))

	_, err = sharedPool.Exec(ctx, `
		UPDATE projection_revisions
		SET state = 'published', published_at = $2
		WHERE id = $1`, revisionID, createdAt.Add(3*time.Second))
	require.ErrorContains(t, err, "terminal champion")
}
