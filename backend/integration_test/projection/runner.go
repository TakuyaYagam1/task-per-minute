//go:build integration

package projection

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/projectionseed"
)

func RunProjectionMigration(t *testing.T, pool *pgxpool.Pool) {
	ctx := context.Background()
	require.NoError(t, resetProjectionTables(ctx, pool))
	t.Cleanup(func() { require.NoError(t, resetProjectionTables(ctx, pool)) })

	fixture, err := createProjectionFixture(ctx, pool, 4)
	require.NoError(t, err)
	goldenPositionCommitID, err := projectionseed.CreateGoldenSource(ctx, pool, projectionseed.GoldenSourceInput{
		TournamentID:   fixture.tournamentID,
		RosterID:       fixture.rosterID,
		ParticipantIDs: fixture.participantIDs,
		CreatedAt:      fixture.createdAt,
	})
	require.NoError(t, err)

	firstCutoffAt := fixture.createdAt.Add(4 * time.Minute)
	firstCutoffID, err := projectionseed.CreateCutoff(ctx, pool, projectionseed.CutoffInput{
		TournamentID:             fixture.tournamentID,
		RosterID:                 fixture.rosterID,
		SequenceNumber:           1,
		PreviousCutoffID:         nil,
		SourceKind:               "golden_position",
		OfficialResultRevisionID: nil,
		GoldenPositionCommitID:   goldenPositionCommitID,
		Reason:                   "initial Golden terminal position",
		CreatedAt:                firstCutoffAt,
	})
	require.NoError(t, err)
	firstRevisionID, err := projectionseed.CreateRevision(ctx, pool, projectionseed.RevisionInput{
		TournamentID:       fixture.tournamentID,
		RosterID:           fixture.rosterID,
		RevisionNumber:     1,
		PreviousRevisionID: nil,
		CutoffID:           firstCutoffID,
		CreatedAt:          firstCutoffAt.Add(time.Second),
	})
	require.NoError(t, err)

	firstArtifacts, err := createProjectionArtifactSet(
		ctx, pool, fixture, firstRevisionID, "v1", firstCutoffAt.Add(2*time.Second),
	)
	require.NoError(t, err)
	for kind, artifactID := range firstArtifacts {
		require.NoError(t, linkProjectionArtifact(
			ctx, pool, fixture, firstRevisionID, kind, artifactID, "produced",
		))
	}
	require.NoError(t, createProjectionGoldenDependency(
		ctx, pool, fixture, firstArtifacts["standings"], goldenPositionCommitID,
	))
	require.NoError(t, createProjectionArtifactDependency(
		ctx, pool, fixture, firstArtifacts["bracket"], firstArtifacts["standings"],
	))
	require.NoError(t, createProjectionArtifactDependency(
		ctx, pool, fixture, firstArtifacts["top_four"], firstArtifacts["bracket"],
	))

	_, err = pool.Exec(
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

	_, err = pool.Exec(
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
		_, err = pool.Exec(
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
	require.NoError(t, projectionseed.PublishRevision(ctx, pool, firstRevisionID, firstPublishedAt))

	_, err = pool.Exec(ctx, `
		UPDATE projection_artifacts
		SET payload = '{"entries":[]}'::JSONB
		WHERE id = $1`, firstArtifacts["standings"])
	require.ErrorContains(t, err, "immutable evidence")

	_, err = pool.Exec(ctx, `
		UPDATE projection_artifact_members
		SET score = score + 1
		WHERE artifact_id = $1 AND participant_id = $2`,
		firstArtifacts["standings"], fixture.participantIDs[0])
	require.ErrorContains(t, err, "immutable evidence")

	_, err = pool.Exec(ctx, `
		UPDATE projection_cutoffs
		SET reason = 'rewritten cutoff'
		WHERE id = $1`, firstCutoffID)
	require.ErrorContains(t, err, "immutable evidence")

	secondCutoffAt := firstCutoffAt.Add(time.Minute)
	secondCutoffID, err := projectionseed.CreateCutoff(ctx, pool, projectionseed.CutoffInput{
		TournamentID:             fixture.tournamentID,
		RosterID:                 fixture.rosterID,
		SequenceNumber:           2,
		PreviousCutoffID:         firstCutoffID,
		SourceKind:               "operator_rebuild",
		OfficialResultRevisionID: nil,
		GoldenPositionCommitID:   nil,
		Reason:                   "correct affected bracket descendants",
		CreatedAt:                secondCutoffAt,
	})
	require.NoError(t, err)
	secondRevisionID, err := projectionseed.CreateRevision(ctx, pool, projectionseed.RevisionInput{
		TournamentID:       fixture.tournamentID,
		RosterID:           fixture.rosterID,
		RevisionNumber:     2,
		PreviousRevisionID: firstRevisionID,
		CutoffID:           secondCutoffID,
		CreatedAt:          secondCutoffAt.Add(time.Second),
	})
	require.NoError(t, err)

	require.NoError(t, linkProjectionArtifact(
		ctx, pool, fixture, secondRevisionID, "standings", firstArtifacts["standings"], "reused",
	))
	secondArtifacts, err := createProjectionArtifactSetWithoutStandings(
		ctx, pool, fixture, secondRevisionID, "v2", secondCutoffAt.Add(2*time.Second),
	)
	require.NoError(t, err)
	for kind, artifactID := range secondArtifacts {
		require.NoError(t, linkProjectionArtifact(
			ctx, pool, fixture, secondRevisionID, kind, artifactID, "produced",
		))
	}
	require.NoError(t, createProjectionArtifactDependency(
		ctx, pool, fixture, secondArtifacts["bracket"], firstArtifacts["standings"],
	))
	require.NoError(t, createProjectionArtifactDependency(
		ctx, pool, fixture, secondArtifacts["top_four"], secondArtifacts["bracket"],
	))

	_, err = pool.Exec(ctx, `
		UPDATE projection_revisions
		SET state = 'published', published_at = $2
		WHERE id = $1`, secondRevisionID, secondCutoffAt.Add(3*time.Second))
	require.ErrorContains(t, err, "must atomically supersede")

	require.NoError(t, projectionseed.SupersedeRevision(ctx, pool, projectionseed.SupersedeInput{
		PreviousRevisionID:    firstRevisionID,
		ReplacementRevisionID: secondRevisionID,
		TransitionAt:          secondCutoffAt.Add(3 * time.Second),
	}))

	var (
		firstState         string
		secondState        string
		firstArtifactCount int
		reusedStandingsID  uuid.UUID
	)
	err = pool.QueryRow(ctx, `
		SELECT state
		FROM projection_revisions
		WHERE id = $1`, firstRevisionID).Scan(&firstState)
	require.NoError(t, err)
	err = pool.QueryRow(ctx, `
		SELECT state
		FROM projection_revisions
		WHERE id = $1`, secondRevisionID).Scan(&secondState)
	require.NoError(t, err)
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM projection_revision_artifacts
		WHERE revision_id = $1`, firstRevisionID).Scan(&firstArtifactCount)
	require.NoError(t, err)
	err = pool.QueryRow(ctx, `
		SELECT artifact_id
		FROM projection_revision_artifacts
		WHERE revision_id = $1 AND artifact_kind = 'standings'`, secondRevisionID).
		Scan(&reusedStandingsID)
	require.NoError(t, err)

	require.Equal(t, "superseded", firstState)
	require.Equal(t, "published", secondState)
	require.Equal(t, 3, firstArtifactCount)
	require.Equal(t, firstArtifacts["standings"], reusedStandingsID)

	_, err = pool.Exec(ctx, `
		DELETE FROM projection_artifacts
		WHERE id = $1`, firstArtifacts["standings"])
	require.ErrorContains(t, err, "immutable evidence")

	require.ErrorContains(t, createProjectionCutoffScopeIsolationProbe(
		ctx, pool, goldenPositionCommitID, secondCutoffAt.Add(time.Minute),
	), "outside its roster")
}
