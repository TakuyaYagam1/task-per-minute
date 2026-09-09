//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
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
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO projection_revision_artifacts (
			revision_id, tournament_id, roster_id,
			artifact_kind, artifact_id, change_kind
		)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		revisionID,
		fixture.tournamentID,
		fixture.rosterID,
		kind,
		artifactID,
		changeKind,
	)
	require.NoError(tb, err)
}

func createProjectionArtifactDependency(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	artifactID uuid.UUID,
	dependsOnArtifactID uuid.UUID,
) {
	tb.Helper()
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO projection_dependencies (
			artifact_id, tournament_id, roster_id,
			dependency_kind, depends_on_artifact_id
		)
		VALUES ($1, $2, $3, 'artifact', $4)`,
		artifactID,
		fixture.tournamentID,
		fixture.rosterID,
		dependsOnArtifactID,
	)
	require.NoError(tb, err)
}

func createProjectionGoldenDependency(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	artifactID uuid.UUID,
	goldenPositionCommitID uuid.UUID,
) {
	tb.Helper()
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO projection_dependencies (
			artifact_id, tournament_id, roster_id,
			dependency_kind, golden_position_commit_id
		)
		VALUES ($1, $2, $3, 'golden_position', $4)`,
		artifactID,
		fixture.tournamentID,
		fixture.rosterID,
		goldenPositionCommitID,
	)
	require.NoError(tb, err)
}

func publishProjectionRevision(
	ctx context.Context, tb testing.TB,
	revisionID uuid.UUID,
	publishedAt time.Time,
) {
	tb.Helper()
	_, err := sharedPool.Exec(ctx, `
		UPDATE projection_revisions
		SET state = 'published', published_at = $2
		WHERE id = $1`, revisionID, publishedAt)
	require.NoError(tb, err)
}

func supersedeProjectionRevision(
	ctx context.Context, tb testing.TB,
	previousRevisionID uuid.UUID,
	replacementRevisionID uuid.UUID,
	transitionAt time.Time,
) {
	tb.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(tb, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		UPDATE projection_revisions
		SET state = 'superseded',
			superseded_by_revision_id = $2,
			superseded_at = $3,
			supersession_reason = 'affected descendants rebuilt'
		WHERE id = $1`, previousRevisionID, replacementRevisionID, transitionAt)
	require.NoError(tb, err)
	_, err = tx.Exec(ctx, `
		UPDATE projection_revisions
		SET state = 'published', published_at = $2
		WHERE id = $1`, replacementRevisionID, transitionAt)
	require.NoError(tb, err)
	require.NoError(tb, tx.Commit(ctx))
}

func assertProjectionCutoffScopeIsolation(
	ctx context.Context, tb testing.TB,
	goldenPositionCommitID uuid.UUID,
	createdAt time.Time,
) {
	tb.Helper()

	otherTournamentID := createMigrationTournament(ctx, tb)
	otherRosterID := createMigrationRoster(ctx, tb, otherTournamentID)
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO projection_cutoffs (
			tournament_id, roster_id, sequence_number,
			source_kind, golden_position_commit_id,
			reason, cutoff_at, created_at
		)
		VALUES ($1, $2, 1, 'golden_position', $3, 'cross-roster probe', $4, $4)`,
		otherTournamentID,
		otherRosterID,
		goldenPositionCommitID,
		createdAt,
	)
	require.ErrorContains(tb, err, "outside its roster")
}
