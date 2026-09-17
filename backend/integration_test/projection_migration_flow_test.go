//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestProjectionMigration(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createGoldenMigrationFixture(ctx, t, 4)
	goldenPositionCommitID := createProjectionGoldenSource(ctx, t, fixture)

	firstCutoffAt := fixture.createdAt.Add(4 * time.Minute)
	firstCutoffID := createProjectionCutoff(
		ctx, t,
		fixture,
		1,
		nil,
		"golden_position",
		nil,
		goldenPositionCommitID,
		"initial Golden terminal position",
		firstCutoffAt,
	)
	firstRevisionID := createProjectionRevision(
		ctx, t, fixture, 1, nil, firstCutoffID, firstCutoffAt.Add(time.Second),
	)

	firstArtifacts := createProjectionArtifactSet(
		ctx, t,
		fixture,
		firstRevisionID,
		"v1",
		firstCutoffAt.Add(2*time.Second),
	)
	for kind, artifactID := range firstArtifacts {
		linkProjectionArtifact(
			ctx, t, fixture, firstRevisionID, kind, artifactID, "produced",
		)
	}
	createProjectionGoldenDependency(
		ctx, t, fixture, firstArtifacts["standings"], goldenPositionCommitID,
	)
	createProjectionArtifactDependency(
		ctx, t, fixture, firstArtifacts["bracket"], firstArtifacts["standings"],
	)
	createProjectionArtifactDependency(
		ctx, t, fixture, firstArtifacts["top_four"], firstArtifacts["bracket"],
	)

	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO projection_dependencies (
			artifact_id, tournament_id, roster_id,
			dependency_kind, depends_on_artifact_id
		)
		VALUES ($1, $2, $3, 'artifact', $4)`,
		firstArtifacts["standings"],
		fixture.tournamentID,
		fixture.rosterID,
		firstArtifacts["bracket"],
	)
	require.ErrorContains(t, err, "must remain acyclic")

	_, err = sharedPool.Exec(
		ctx, `
		INSERT INTO projection_artifacts (
			tournament_id, roster_id, produced_by_revision_id,
			artifact_kind, artifact_key, payload, payload_digest, created_at
		)
		VALUES (
			$1, $2, $3,
			'top4', 'invalid-top4', '{"participants":["one","two","three"]}'::JSONB,
			$4, $5
		)`,
		fixture.tournamentID,
		fixture.rosterID,
		firstRevisionID,
		bytes.Repeat([]byte{21}, 32),
		firstCutoffAt.Add(3*time.Second),
	)
	require.Error(t, err)

	invalidCanonicalPayloads := []struct {
		kind    string
		key     string
		payload string
	}{
		{kind: "standings", key: "missing-standings-entries", payload: `{}`},
		{kind: "standings", key: "legacy-standings", payload: `{"entries":[{"rank":1}],"standings":[{"rank":1}]}`},
		{kind: "bracket", key: "missing-bracket-rounds", payload: `{}`},
		{kind: "bracket", key: "legacy-bracket", payload: `{"rounds":[{"round":1}],"semifinals":[{"round":1}]}`},
	}
	for index, test := range invalidCanonicalPayloads {
		_, err = sharedPool.Exec(
			ctx, `
			INSERT INTO projection_artifacts (
				tournament_id, roster_id, produced_by_revision_id,
				artifact_kind, artifact_key, payload, payload_digest, created_at
			)
			VALUES ($1, $2, $3, $4, $5, $6::JSONB, $7, $8)`,
			fixture.tournamentID,
			fixture.rosterID,
			firstRevisionID,
			test.kind,
			test.key,
			test.payload,
			bytes.Repeat([]byte{byte(22 + index)}, 32),
			firstCutoffAt.Add(3*time.Second),
		)
		require.Error(t, err)
	}

	firstPublishedAt := firstCutoffAt.Add(4 * time.Second)
	publishProjectionRevision(ctx, t, firstRevisionID, firstPublishedAt)

	_, err = sharedPool.Exec(ctx, `
		UPDATE projection_artifacts
		SET payload = '{"entries":[]}'::JSONB
		WHERE id = $1`, firstArtifacts["standings"])
	require.ErrorContains(t, err, "immutable evidence")

	_, err = sharedPool.Exec(ctx, `
		UPDATE projection_artifact_members
		SET score = score + 1
		WHERE artifact_id = $1 AND participant_id = $2`,
		firstArtifacts["standings"], fixture.participantIDs[0])
	require.ErrorContains(t, err, "immutable evidence")

	_, err = sharedPool.Exec(ctx, `
		UPDATE projection_cutoffs
		SET reason = 'rewritten cutoff'
		WHERE id = $1`, firstCutoffID)
	require.ErrorContains(t, err, "immutable evidence")

	secondCutoffAt := firstCutoffAt.Add(time.Minute)
	secondCutoffID := createProjectionCutoff(
		ctx, t,
		fixture,
		2,
		firstCutoffID,
		"operator_rebuild",
		nil,
		nil,
		"correct affected bracket descendants",
		secondCutoffAt,
	)
	secondRevisionID := createProjectionRevision(
		ctx, t,
		fixture,
		2,
		firstRevisionID,
		secondCutoffID,
		secondCutoffAt.Add(time.Second),
	)

	linkProjectionArtifact(
		ctx, t,
		fixture,
		secondRevisionID,
		"standings",
		firstArtifacts["standings"],
		"reused",
	)
	secondArtifacts := createProjectionArtifactSetWithoutStandings(
		ctx, t,
		fixture,
		secondRevisionID,
		"v2",
		secondCutoffAt.Add(2*time.Second),
	)
	for kind, artifactID := range secondArtifacts {
		linkProjectionArtifact(
			ctx, t, fixture, secondRevisionID, kind, artifactID, "produced",
		)
	}
	createProjectionArtifactDependency(
		ctx, t, fixture, secondArtifacts["bracket"], firstArtifacts["standings"],
	)
	createProjectionArtifactDependency(
		ctx, t, fixture, secondArtifacts["top_four"], secondArtifacts["bracket"],
	)

	_, err = sharedPool.Exec(ctx, `
		UPDATE projection_revisions
		SET state = 'published', published_at = $2
		WHERE id = $1`, secondRevisionID, secondCutoffAt.Add(3*time.Second))
	require.ErrorContains(t, err, "must atomically supersede")

	supersedeProjectionRevision(
		ctx, t,
		firstRevisionID,
		secondRevisionID,
		secondCutoffAt.Add(3*time.Second),
	)

	var (
		firstState         string
		secondState        string
		firstArtifactCount int
		reusedStandingsID  uuid.UUID
	)
	err = sharedPool.QueryRow(ctx, `
		SELECT state
		FROM projection_revisions
		WHERE id = $1`, firstRevisionID).Scan(&firstState)
	require.NoError(t, err)
	err = sharedPool.QueryRow(ctx, `
		SELECT state
		FROM projection_revisions
		WHERE id = $1`, secondRevisionID).Scan(&secondState)
	require.NoError(t, err)
	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM projection_revision_artifacts
		WHERE revision_id = $1`, firstRevisionID).Scan(&firstArtifactCount)
	require.NoError(t, err)
	err = sharedPool.QueryRow(ctx, `
		SELECT artifact_id
		FROM projection_revision_artifacts
		WHERE revision_id = $1 AND artifact_kind = 'standings'`, secondRevisionID).
		Scan(&reusedStandingsID)
	require.NoError(t, err)

	require.Equal(t, "superseded", firstState)
	require.Equal(t, "published", secondState)
	require.Equal(t, 3, firstArtifactCount)
	require.Equal(t, firstArtifacts["standings"], reusedStandingsID)

	_, err = sharedPool.Exec(ctx, `
		DELETE FROM projection_artifacts
		WHERE id = $1`, firstArtifacts["standings"])
	require.ErrorContains(t, err, "immutable evidence")

	assertProjectionCutoffScopeIsolation(
		ctx, t, goldenPositionCommitID, secondCutoffAt.Add(time.Minute),
	)
}
