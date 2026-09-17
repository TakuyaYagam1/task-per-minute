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

func linkProjectionArtifact(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	revisionID uuid.UUID,
	kind string,
	artifactID uuid.UUID,
	changeKind string,
) {
	tb.Helper()
	err := projectionseed.LinkArtifact(ctx, sharedPool, projectionseed.ArtifactLinkInput{
		RevisionID:   revisionID,
		TournamentID: fixture.tournamentID,
		RosterID:     fixture.rosterID,
		Kind:         kind,
		ArtifactID:   artifactID,
		ChangeKind:   changeKind,
	})
	require.NoError(tb, err)
}

func createProjectionArtifactDependency(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	artifactID uuid.UUID,
	dependsOnArtifactID uuid.UUID,
) {
	tb.Helper()
	err := projectionseed.CreateArtifactDependency(ctx, sharedPool, projectionseed.ArtifactDependencyInput{
		ArtifactID:          artifactID,
		TournamentID:        fixture.tournamentID,
		RosterID:            fixture.rosterID,
		DependsOnArtifactID: dependsOnArtifactID,
	})
	require.NoError(tb, err)
}

func createProjectionGoldenDependency(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	artifactID uuid.UUID,
	goldenPositionCommitID uuid.UUID,
) {
	tb.Helper()
	err := projectionseed.CreateGoldenDependency(ctx, sharedPool, projectionseed.GoldenDependencyInput{
		ArtifactID:             artifactID,
		TournamentID:           fixture.tournamentID,
		RosterID:               fixture.rosterID,
		GoldenPositionCommitID: goldenPositionCommitID,
	})
	require.NoError(tb, err)
}

func publishProjectionRevision(
	ctx context.Context, tb testing.TB,
	revisionID uuid.UUID,
	publishedAt time.Time,
) {
	tb.Helper()
	err := projectionseed.PublishRevision(ctx, sharedPool, revisionID, publishedAt)
	require.NoError(tb, err)
}

func supersedeProjectionRevision(
	ctx context.Context, tb testing.TB,
	previousRevisionID uuid.UUID,
	replacementRevisionID uuid.UUID,
	transitionAt time.Time,
) {
	tb.Helper()
	err := projectionseed.SupersedeRevision(ctx, sharedPool, projectionseed.SupersedeInput{
		PreviousRevisionID:    previousRevisionID,
		ReplacementRevisionID: replacementRevisionID,
		TransitionAt:          transitionAt,
	})
	require.NoError(tb, err)
}

func assertProjectionCutoffScopeIsolation(
	ctx context.Context, tb testing.TB,
	goldenPositionCommitID uuid.UUID,
	createdAt time.Time,
) {
	tb.Helper()

	otherTournamentID := createMigrationTournament(ctx, tb)
	otherRosterID := createMigrationRoster(ctx, tb, otherTournamentID)
	err := projectionseed.CreateCrossRosterCutoff(ctx, sharedPool, projectionseed.CrossRosterCutoffInput{
		TournamentID:           otherTournamentID,
		RosterID:               otherRosterID,
		GoldenPositionCommitID: goldenPositionCommitID,
		CreatedAt:              createdAt,
	})
	require.ErrorContains(tb, err, "outside its roster")
}
