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

func createProjectionArtifactSet(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	revisionID uuid.UUID,
	keySuffix string,
	createdAt time.Time,
) map[string]uuid.UUID {
	tb.Helper()

	artifacts := createProjectionArtifactSetWithoutStandings(
		ctx, tb, fixture, revisionID, keySuffix, createdAt,
	)
	standingsPayload := fmt.Sprintf(
		`{"entries":[{"participant_id":"%s","rank":1}]}`,
		fixture.participantIDs[0],
	)
	artifacts["standings"] = createProjectionArtifact(
		ctx, tb,
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

func createProjectionArtifactSetWithoutStandings(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	revisionID uuid.UUID,
	keySuffix string,
	createdAt time.Time,
) map[string]uuid.UUID {
	tb.Helper()

	topFourPayload := fmt.Sprintf(
		`{"participants":["%s","%s","%s","%s"]}`,
		fixture.participantIDs[0],
		fixture.participantIDs[1],
		fixture.participantIDs[2],
		fixture.participantIDs[3],
	)

	return map[string]uuid.UUID{
		"bracket": createProjectionArtifact(
			ctx, tb,
			fixture,
			revisionID,
			"bracket",
			"bracket-"+keySuffix,
			`{"rounds":[{"round":1}]}`,
			12,
			createdAt,
		),
		"top_four": createProjectionArtifact(
			ctx, tb,
			fixture,
			revisionID,
			"top_four",
			"top4-"+keySuffix,
			topFourPayload,
			13,
			createdAt,
		),
	}
}

func createProjectionArtifact(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	revisionID uuid.UUID,
	kind string,
	key string,
	payload string,
	digestByte byte,
	createdAt time.Time,
) uuid.UUID {
	tb.Helper()

	id := uuid.New()
	_, err := sharedPool.Exec(
		ctx, `
		INSERT INTO projection_artifacts (
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
	require.NoError(tb, err)
	createProjectionArtifactMembers(ctx, tb, fixture, id, kind)
	return id
}

func createProjectionArtifactMembers(
	ctx context.Context, tb testing.TB,
	fixture goldenMigrationFixture,
	artifactID uuid.UUID,
	kind string,
) {
	tb.Helper()

	memberCount := len(fixture.participantIDs)
	if kind == "champion" {
		memberCount = 1
	}
	for index, participantID := range fixture.participantIDs[:memberCount] {
		var score any
		if kind == "standings" {
			score = memberCount - index
		}
		_, err := sharedPool.Exec(
			ctx, `
			INSERT INTO projection_artifact_members (
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
		require.NoError(tb, err)
	}
}
