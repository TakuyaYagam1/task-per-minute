//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/projectionseed"
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

	id, err := projectionseed.CreateArtifact(ctx, sharedPool, projectionseed.ArtifactInput{
		TournamentID: fixture.tournamentID,
		RosterID:     fixture.rosterID,
		RevisionID:   revisionID,
		Kind:         kind,
		Key:          key,
		Payload:      payload,
		DigestByte:   digestByte,
		CreatedAt:    createdAt,
	})
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

	err := projectionseed.CreateArtifactMembers(ctx, sharedPool, projectionseed.ArtifactMembersInput{
		ArtifactID:     artifactID,
		TournamentID:   fixture.tournamentID,
		RosterID:       fixture.rosterID,
		Kind:           kind,
		ParticipantIDs: fixture.participantIDs,
	})
	require.NoError(tb, err)
}
