//go:build integration

package projection_test

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/projectionseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/swissseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/tournamentseed"
)

type projectionFixture struct {
	tournamentID   uuid.UUID
	rosterID       uuid.UUID
	participantIDs []uuid.UUID
	createdAt      time.Time
}

func resetProjectionTables(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("projection fixture: nil pool")
	}
	_, err := pool.Exec(ctx, `
		TRUNCATE TABLE participant_reservations, tournaments CASCADE`)
	return err
}

func createProjectionFixture(
	ctx context.Context,
	pool *pgxpool.Pool,
	participantCount int,
) (projectionFixture, error) {
	if participantCount < 4 {
		return projectionFixture{}, fmt.Errorf(
			"projection fixture: expected at least four participants, got %d",
			participantCount,
		)
	}

	tournamentID, err := tournamentseed.CreateTournament(ctx, pool)
	if err != nil {
		return projectionFixture{}, fmt.Errorf("projection fixture: create tournament: %w", err)
	}
	roster, err := tournamentseed.CreateRoster(ctx, pool, tournamentID)
	if err != nil {
		return projectionFixture{}, fmt.Errorf("projection fixture: create roster: %w", err)
	}
	playerIDs, err := tournamentseed.CreatePlayers(ctx, pool, "tournament_migration", participantCount)
	if err != nil {
		return projectionFixture{}, fmt.Errorf("projection fixture: create players: %w", err)
	}
	participantIDs, err := swissseed.CreateParticipants(ctx, pool, roster.ID, playerIDs)
	if err != nil {
		return projectionFixture{}, fmt.Errorf("projection fixture: create participants: %w", err)
	}

	return projectionFixture{
		tournamentID:   tournamentID,
		rosterID:       roster.ID,
		participantIDs: participantIDs,
		createdAt:      time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Microsecond),
	}, nil
}

func createProjectionArtifactSet(
	ctx context.Context,
	pool *pgxpool.Pool,
	fixture projectionFixture,
	revisionID uuid.UUID,
	keySuffix string,
	createdAt time.Time,
) (map[string]uuid.UUID, error) {
	artifacts, err := createProjectionArtifactSetWithoutStandings(
		ctx, pool, fixture, revisionID, keySuffix, createdAt,
	)
	if err != nil {
		return nil, err
	}
	standingsPayload := fmt.Sprintf(
		`{"entries":[{"participant_id":"%s","rank":1}]}`,
		fixture.participantIDs[0],
	)
	standingsID, err := createProjectionArtifact(
		ctx, pool, fixture, revisionID, "standings", "standings-"+keySuffix,
		standingsPayload, 11, createdAt,
	)
	if err != nil {
		return nil, err
	}
	artifacts["standings"] = standingsID
	return artifacts, nil
}

func createProjectionArtifactSetWithoutStandings(
	ctx context.Context,
	pool *pgxpool.Pool,
	fixture projectionFixture,
	revisionID uuid.UUID,
	keySuffix string,
	createdAt time.Time,
) (map[string]uuid.UUID, error) {
	topFourPayload := fmt.Sprintf(
		`{"participants":["%s","%s","%s","%s"]}`,
		fixture.participantIDs[0],
		fixture.participantIDs[1],
		fixture.participantIDs[2],
		fixture.participantIDs[3],
	)

	bracketID, err := createProjectionArtifact(
		ctx, pool, fixture, revisionID, "bracket", "bracket-"+keySuffix,
		`{"rounds":[{"round":1}]}`, 12, createdAt,
	)
	if err != nil {
		return nil, err
	}
	topFourID, err := createProjectionArtifact(
		ctx, pool, fixture, revisionID, "top_four", "top4-"+keySuffix,
		topFourPayload, 13, createdAt,
	)
	if err != nil {
		return nil, err
	}
	return map[string]uuid.UUID{
		"bracket":  bracketID,
		"top_four": topFourID,
	}, nil
}

func createProjectionArtifact(
	ctx context.Context,
	pool *pgxpool.Pool,
	fixture projectionFixture,
	revisionID uuid.UUID,
	kind string,
	key string,
	payload string,
	digestByte byte,
	createdAt time.Time,
) (uuid.UUID, error) {
	id, err := projectionseed.CreateArtifact(ctx, pool, projectionseed.ArtifactInput{
		TournamentID: fixture.tournamentID,
		RosterID:     fixture.rosterID,
		RevisionID:   revisionID,
		Kind:         kind,
		Key:          key,
		Payload:      payload,
		DigestByte:   digestByte,
		CreatedAt:    createdAt,
	})
	if err != nil {
		return uuid.Nil, err
	}
	if err := projectionseed.CreateArtifactMembers(ctx, pool, projectionseed.ArtifactMembersInput{
		ArtifactID:     id,
		TournamentID:   fixture.tournamentID,
		RosterID:       fixture.rosterID,
		Kind:           kind,
		ParticipantIDs: fixture.participantIDs,
	}); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func linkProjectionArtifact(
	ctx context.Context,
	pool *pgxpool.Pool,
	fixture projectionFixture,
	revisionID uuid.UUID,
	kind string,
	artifactID uuid.UUID,
	changeKind string,
) error {
	return projectionseed.LinkArtifact(ctx, pool, projectionseed.ArtifactLinkInput{
		RevisionID:   revisionID,
		TournamentID: fixture.tournamentID,
		RosterID:     fixture.rosterID,
		Kind:         kind,
		ArtifactID:   artifactID,
		ChangeKind:   changeKind,
	})
}

func createProjectionGoldenDependency(
	ctx context.Context,
	pool *pgxpool.Pool,
	fixture projectionFixture,
	artifactID uuid.UUID,
	goldenPositionCommitID uuid.UUID,
) error {
	return projectionseed.CreateGoldenDependency(ctx, pool, projectionseed.GoldenDependencyInput{
		ArtifactID:             artifactID,
		TournamentID:           fixture.tournamentID,
		RosterID:               fixture.rosterID,
		GoldenPositionCommitID: goldenPositionCommitID,
	})
}

func createProjectionArtifactDependency(
	ctx context.Context,
	pool *pgxpool.Pool,
	fixture projectionFixture,
	artifactID uuid.UUID,
	dependsOnArtifactID uuid.UUID,
) error {
	return projectionseed.CreateArtifactDependency(ctx, pool, projectionseed.ArtifactDependencyInput{
		ArtifactID:          artifactID,
		TournamentID:        fixture.tournamentID,
		RosterID:            fixture.rosterID,
		DependsOnArtifactID: dependsOnArtifactID,
	})
}
