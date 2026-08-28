//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestArenaProjectionMigration(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	fixture := createArenaGoldenMigrationFixture(t, ctx, 4)
	goldenPositionCommitID := createArenaProjectionGoldenSource(t, ctx, fixture)

	firstCutoffAt := fixture.createdAt.Add(4 * time.Minute)
	firstCutoffID := createArenaProjectionCutoff(
		t,
		ctx,
		fixture,
		1,
		nil,
		"golden_position",
		nil,
		goldenPositionCommitID,
		"initial Golden terminal position",
		firstCutoffAt,
	)
	firstRevisionID := createArenaProjectionRevision(
		t, ctx, fixture, 1, nil, firstCutoffID, firstCutoffAt.Add(time.Second),
	)

	firstArtifacts := createArenaProjectionArtifactSet(
		t,
		ctx,
		fixture,
		firstRevisionID,
		"v1",
		firstCutoffAt.Add(2*time.Second),
	)
	for kind, artifactID := range firstArtifacts {
		linkArenaProjectionArtifact(
			t, ctx, fixture, firstRevisionID, kind, artifactID, "produced",
		)
	}
	createArenaProjectionGoldenDependency(
		t, ctx, fixture, firstArtifacts["standings"], goldenPositionCommitID,
	)
	createArenaProjectionArtifactDependency(
		t, ctx, fixture, firstArtifacts["bracket"], firstArtifacts["standings"],
	)
	createArenaProjectionArtifactDependency(
		t, ctx, fixture, firstArtifacts["top4"], firstArtifacts["bracket"],
	)
	createArenaProjectionArtifactDependency(
		t, ctx, fixture, firstArtifacts["champion"], firstArtifacts["top4"],
	)

	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_projection_dependencies (
			artifact_id, tournament_id, roster_id,
			dependency_kind, depends_on_artifact_id
		)
		VALUES ($1, $2, $3, 'artifact', $4)`,
		firstArtifacts["standings"],
		fixture.tournamentID,
		fixture.rosterID,
		firstArtifacts["champion"],
	)
	require.ErrorContains(t, err, "must remain acyclic")

	_, err = sharedPool.Exec(ctx, `
		INSERT INTO arena_projection_artifacts (
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

	firstPublishedAt := firstCutoffAt.Add(4 * time.Second)
	publishArenaProjectionRevision(t, ctx, firstRevisionID, firstPublishedAt)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_projection_artifacts
		SET payload = '{"entries":[]}'::JSONB
		WHERE id = $1`, firstArtifacts["standings"])
	require.ErrorContains(t, err, "immutable evidence")

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_projection_artifact_members
		SET score = score + 1
		WHERE artifact_id = $1 AND participant_id = $2`,
		firstArtifacts["standings"], fixture.participantIDs[0])
	require.ErrorContains(t, err, "immutable evidence")

	_, err = sharedPool.Exec(ctx, `
		DELETE FROM arena_projection_dependencies
		WHERE artifact_id = $1`, firstArtifacts["champion"])
	require.ErrorContains(t, err, "immutable")

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_projection_cutoffs
		SET reason = 'rewritten cutoff'
		WHERE id = $1`, firstCutoffID)
	require.ErrorContains(t, err, "immutable evidence")

	secondCutoffAt := firstCutoffAt.Add(time.Minute)
	secondCutoffID := createArenaProjectionCutoff(
		t,
		ctx,
		fixture,
		2,
		firstCutoffID,
		"operator_rebuild",
		nil,
		nil,
		"correct affected bracket descendants",
		secondCutoffAt,
	)
	secondRevisionID := createArenaProjectionRevision(
		t,
		ctx,
		fixture,
		2,
		firstRevisionID,
		secondCutoffID,
		secondCutoffAt.Add(time.Second),
	)

	linkArenaProjectionArtifact(
		t,
		ctx,
		fixture,
		secondRevisionID,
		"standings",
		firstArtifacts["standings"],
		"reused",
	)
	secondArtifacts := createArenaProjectionArtifactSetWithoutStandings(
		t,
		ctx,
		fixture,
		secondRevisionID,
		"v2",
		secondCutoffAt.Add(2*time.Second),
	)
	for kind, artifactID := range secondArtifacts {
		linkArenaProjectionArtifact(
			t, ctx, fixture, secondRevisionID, kind, artifactID, "produced",
		)
	}
	createArenaProjectionArtifactDependency(
		t, ctx, fixture, secondArtifacts["bracket"], firstArtifacts["standings"],
	)
	createArenaProjectionArtifactDependency(
		t, ctx, fixture, secondArtifacts["top4"], secondArtifacts["bracket"],
	)
	createArenaProjectionArtifactDependency(
		t, ctx, fixture, secondArtifacts["champion"], secondArtifacts["top4"],
	)

	_, err = sharedPool.Exec(ctx, `
		UPDATE arena_projection_revisions
		SET state = 'published', published_at = $2
		WHERE id = $1`, secondRevisionID, secondCutoffAt.Add(3*time.Second))
	require.ErrorContains(t, err, "must atomically supersede")

	supersedeArenaProjectionRevision(
		t,
		ctx,
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
		FROM arena_projection_revisions
		WHERE id = $1`, firstRevisionID).Scan(&firstState)
	require.NoError(t, err)
	err = sharedPool.QueryRow(ctx, `
		SELECT state
		FROM arena_projection_revisions
		WHERE id = $1`, secondRevisionID).Scan(&secondState)
	require.NoError(t, err)
	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM arena_projection_revision_artifacts
		WHERE revision_id = $1`, firstRevisionID).Scan(&firstArtifactCount)
	require.NoError(t, err)
	err = sharedPool.QueryRow(ctx, `
		SELECT artifact_id
		FROM arena_projection_revision_artifacts
		WHERE revision_id = $1 AND artifact_kind = 'standings'`, secondRevisionID).
		Scan(&reusedStandingsID)
	require.NoError(t, err)

	require.Equal(t, "superseded", firstState)
	require.Equal(t, "published", secondState)
	require.Equal(t, 4, firstArtifactCount)
	require.Equal(t, firstArtifacts["standings"], reusedStandingsID)

	_, err = sharedPool.Exec(ctx, `
		DELETE FROM arena_projection_artifacts
		WHERE id = $1`, firstArtifacts["standings"])
	require.ErrorContains(t, err, "immutable evidence")

	assertArenaProjectionCutoffScopeIsolation(
		t, ctx, goldenPositionCommitID, secondCutoffAt.Add(time.Minute),
	)
}

func createArenaProjectionGoldenSource(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
) uuid.UUID {
	t.Helper()

	attemptID := createArenaGoldenAttempt(t, ctx, fixture, 1, nil, fixture.createdAt)
	readyAt := fixture.createdAt.Add(time.Second)
	membershipIDs := make([]uuid.UUID, 2)
	for index := range membershipIDs {
		membershipIDs[index] = createArenaGoldenMembership(
			t,
			ctx,
			fixture,
			attemptID,
			fixture.participantIDs[index],
			"direct",
			nil,
			fixture.createdAt,
			readyAt,
			nil,
			nil,
			nil,
		)
	}
	advanceArenaGoldenAttemptToReady(
		t,
		ctx,
		attemptID,
		fixture.createdAt.Add(2*time.Second),
		fixture.createdAt.Add(3*time.Second),
	)
	for _, membershipID := range membershipIDs {
		establishArenaGoldenParticipation(
			t, ctx, membershipID, fixture.createdAt.Add(4*time.Second),
		)
	}
	advanceArenaGoldenAttemptToActive(
		t, ctx, attemptID, fixture.createdAt.Add(5*time.Second),
	)
	submissionID := createArenaGoldenSubmission(
		t,
		ctx,
		fixture,
		attemptID,
		membershipIDs[0],
		fixture.participantIDs[0],
		1,
		1,
		"accepted",
		nil,
		fixture.createdAt.Add(6*time.Second),
	)
	return createArenaGoldenPositionCommit(
		t,
		ctx,
		fixture,
		attemptID,
		membershipIDs[0],
		fixture.participantIDs[0],
		submissionID,
		nil,
		1,
		fixture.createdAt.Add(7*time.Second),
	)
}

func createArenaProjectionCutoff(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	sequenceNumber int,
	previousCutoffID any,
	sourceKind string,
	officialResultRevisionID any,
	goldenPositionCommitID any,
	reason string,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_projection_cutoffs (
			id, tournament_id, roster_id, sequence_number, previous_cutoff_id,
			source_kind, official_result_revision_id, golden_position_commit_id,
			reason, cutoff_at, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
		id,
		fixture.tournamentID,
		fixture.rosterID,
		sequenceNumber,
		previousCutoffID,
		sourceKind,
		officialResultRevisionID,
		goldenPositionCommitID,
		reason,
		createdAt,
	)
	require.NoError(t, err)
	return id
}

func createArenaProjectionRevision(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	revisionNumber int,
	previousRevisionID any,
	cutoffID uuid.UUID,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_projection_revisions (
			id, tournament_id, roster_id, revision_number,
			previous_revision_id, cutoff_id, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id,
		fixture.tournamentID,
		fixture.rosterID,
		revisionNumber,
		previousRevisionID,
		cutoffID,
		createdAt,
	)
	require.NoError(t, err)
	return id
}

func createArenaProjectionArtifactSet(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	revisionID uuid.UUID,
	keySuffix string,
	createdAt time.Time,
) map[string]uuid.UUID {
	t.Helper()

	artifacts := createArenaProjectionArtifactSetWithoutStandings(
		t, ctx, fixture, revisionID, keySuffix, createdAt,
	)
	standingsPayload := fmt.Sprintf(
		`{"entries":[{"participant_id":"%s","rank":1}]}`,
		fixture.participantIDs[0],
	)
	artifacts["standings"] = createArenaProjectionArtifact(
		t,
		ctx,
		fixture,
		revisionID,
		"standings",
		"standings-"+keySuffix,
		standingsPayload,
		11,
		createdAt,
	)
	return artifacts
}

func createArenaProjectionArtifactSetWithoutStandings(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	revisionID uuid.UUID,
	keySuffix string,
	createdAt time.Time,
) map[string]uuid.UUID {
	t.Helper()

	topFourPayload := fmt.Sprintf(
		`{"participants":["%s","%s","%s","%s"]}`,
		fixture.participantIDs[0],
		fixture.participantIDs[1],
		fixture.participantIDs[2],
		fixture.participantIDs[3],
	)
	championPayload := fmt.Sprintf(
		`{"participant_id":"%s"}`,
		fixture.participantIDs[0],
	)

	return map[string]uuid.UUID{
		"bracket": createArenaProjectionArtifact(
			t,
			ctx,
			fixture,
			revisionID,
			"bracket",
			"bracket-"+keySuffix,
			`{"rounds":[{"round":1}]}`,
			12,
			createdAt,
		),
		"top4": createArenaProjectionArtifact(
			t,
			ctx,
			fixture,
			revisionID,
			"top4",
			"top4-"+keySuffix,
			topFourPayload,
			13,
			createdAt,
		),
		"champion": createArenaProjectionArtifact(
			t,
			ctx,
			fixture,
			revisionID,
			"champion",
			"champion-"+keySuffix,
			championPayload,
			14,
			createdAt,
		),
	}
}

func createArenaProjectionArtifact(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	revisionID uuid.UUID,
	kind string,
	key string,
	payload string,
	digestByte byte,
	createdAt time.Time,
) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_projection_artifacts (
			id, tournament_id, roster_id, produced_by_revision_id,
			artifact_kind, artifact_key, payload, payload_digest, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7::JSONB, $8, $9)`,
		id,
		fixture.tournamentID,
		fixture.rosterID,
		revisionID,
		kind,
		key,
		payload,
		bytes.Repeat([]byte{digestByte}, 32),
		createdAt,
	)
	require.NoError(t, err)
	createArenaProjectionArtifactMembers(t, ctx, fixture, id, kind)
	return id
}

func createArenaProjectionArtifactMembers(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	artifactID uuid.UUID,
	kind string,
) {
	t.Helper()

	memberCount := len(fixture.participantIDs)
	if kind == "champion" {
		memberCount = 1
	}
	for index, participantID := range fixture.participantIDs[:memberCount] {
		var score any
		if kind == "standings" {
			score = memberCount - index
		}
		_, err := sharedPool.Exec(ctx, `
			INSERT INTO arena_projection_artifact_members (
				artifact_id, tournament_id, roster_id, artifact_kind,
				participant_id, position, score
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			artifactID,
			fixture.tournamentID,
			fixture.rosterID,
			kind,
			participantID,
			index+1,
			score,
		)
		require.NoError(t, err)
	}
}

func linkArenaProjectionArtifact(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	revisionID uuid.UUID,
	kind string,
	artifactID uuid.UUID,
	changeKind string,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_projection_revision_artifacts (
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
	require.NoError(t, err)
}

func createArenaProjectionArtifactDependency(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	artifactID uuid.UUID,
	dependsOnArtifactID uuid.UUID,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_projection_dependencies (
			artifact_id, tournament_id, roster_id,
			dependency_kind, depends_on_artifact_id
		)
		VALUES ($1, $2, $3, 'artifact', $4)`,
		artifactID,
		fixture.tournamentID,
		fixture.rosterID,
		dependsOnArtifactID,
	)
	require.NoError(t, err)
}

func createArenaProjectionGoldenDependency(
	t testing.TB,
	ctx context.Context,
	fixture arenaGoldenMigrationFixture,
	artifactID uuid.UUID,
	goldenPositionCommitID uuid.UUID,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_projection_dependencies (
			artifact_id, tournament_id, roster_id,
			dependency_kind, golden_position_commit_id
		)
		VALUES ($1, $2, $3, 'golden_position', $4)`,
		artifactID,
		fixture.tournamentID,
		fixture.rosterID,
		goldenPositionCommitID,
	)
	require.NoError(t, err)
}

func publishArenaProjectionRevision(
	t testing.TB,
	ctx context.Context,
	revisionID uuid.UUID,
	publishedAt time.Time,
) {
	t.Helper()
	_, err := sharedPool.Exec(ctx, `
		UPDATE arena_projection_revisions
		SET state = 'published', published_at = $2
		WHERE id = $1`, revisionID, publishedAt)
	require.NoError(t, err)
}

func supersedeArenaProjectionRevision(
	t testing.TB,
	ctx context.Context,
	previousRevisionID uuid.UUID,
	replacementRevisionID uuid.UUID,
	transitionAt time.Time,
) {
	t.Helper()

	tx, err := sharedPool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		UPDATE arena_projection_revisions
		SET state = 'superseded',
			superseded_by_revision_id = $2,
			superseded_at = $3,
			supersession_reason = 'affected descendants rebuilt'
		WHERE id = $1`, previousRevisionID, replacementRevisionID, transitionAt)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE arena_projection_revisions
		SET state = 'published', published_at = $2
		WHERE id = $1`, replacementRevisionID, transitionAt)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func assertArenaProjectionCutoffScopeIsolation(
	t testing.TB,
	ctx context.Context,
	goldenPositionCommitID uuid.UUID,
	createdAt time.Time,
) {
	t.Helper()

	otherTournamentID := createArenaMigrationTournament(t, ctx)
	otherRosterID := createArenaMigrationRoster(t, ctx, otherTournamentID)
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO arena_projection_cutoffs (
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
	require.ErrorContains(t, err, "outside its roster")
}
