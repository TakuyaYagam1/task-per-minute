//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestProjectionRepositoryPublishesScopedStandingsAndBracketHistory(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createGoldenMigrationFixture(ctx, t, 4)
	goldenPositionCommitID := createProjectionGoldenSource(ctx, t, fixture)
	repository := postgres.NewProjectionPostgres(postgres.NewTxManager(sharedPool))
	scope := postgres.ProjectionScope{TournamentID: fixture.tournamentID, RosterID: fixture.rosterID}
	createdAt := time.Now().UTC().Truncate(time.Microsecond)

	first, err := repository.Publish(ctx, postgres.ProjectionPublishInput{
		IDs:   postgres.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: scope,
		Source: postgres.ProjectionSource{
			Kind: "golden_position", GoldenPositionCommitID: &goldenPositionCommitID,
			Reason: "Golden positions changed the tournament projection",
		},
		Artifacts:          projectionRepositoryArtifacts(t, fixture.participantIDs, goldenPositionCommitID, "v1"),
		SupersessionReason: "replaced by a later tournament projection",
		CutoffAt:           createdAt, CreatedAt: createdAt, PublishedAt: createdAt.Add(time.Second),
	})
	require.NoError(t, err)
	require.Equal(t, "published", first.Revision.State)
	require.Len(t, first.Artifacts, 3)

	secondCreatedAt := createdAt.Add(time.Minute)
	second, err := repository.Publish(ctx, postgres.ProjectionPublishInput{
		IDs:   postgres.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: scope,
		Source: postgres.ProjectionSource{
			Kind: "operator_rebuild", Reason: "rebuild tournament scoped descendants",
		},
		Artifacts:          projectionRepositoryArtifacts(t, fixture.participantIDs, goldenPositionCommitID, "v2"),
		SupersessionReason: "replaced after deterministic rebuild",
		CutoffAt:           secondCreatedAt, CreatedAt: secondCreatedAt, PublishedAt: secondCreatedAt.Add(time.Second),
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, second.Revision.RevisionNumber)

	current, err := repository.Current(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, second.Revision.ID, current.Revision.ID)
	standings, err := repository.CurrentStandings(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, "standings", standings.Artifact.ArtifactKind)
	require.Contains(t, string(standings.Artifact.Payload), "v2")
	require.Len(t, standings.Members, 4)
	bracket, err := repository.CurrentBracket(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, "bracket", bracket.Artifact.ArtifactKind)
	require.Contains(t, string(bracket.Artifact.Payload), "v2")

	snapshot, err := postgres.NewTournamentSnapshotPostgres(postgres.NewTxManager(sharedPool)).PublicSnapshot(
		ctx,
		usecase.PublicSnapshotQuery{TournamentID: fixture.tournamentID},
	)
	require.NoError(t, err)
	require.Equal(t, second.Revision.RevisionNumber, snapshot.Cursor.ProjectionRevision)
	require.Len(t, snapshot.Scoreboard, 4)
	require.Equal(t, 14, snapshot.Scoreboard[0].Points,
		"snapshot must use the latest published payload, not superseded history")

	history, err := repository.History(ctx, scope)
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.Equal(t, "superseded", history[0].State)
	require.Equal(t, "published", history[1].State)

	_, err = repository.Current(ctx, postgres.ProjectionScope{
		TournamentID: fixture.tournamentID, RosterID: uuid.New(),
	})
	require.ErrorIs(t, err, postgres.ErrProjectionNotFound)
}

func TestProjectionRepositoryRejectsChampionBeforeTerminalFinal(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	fixture := createGoldenMigrationFixture(ctx, t, 4)
	goldenPositionCommitID := createProjectionGoldenSource(ctx, t, fixture)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	artifacts := projectionRepositoryArtifacts(t, fixture.participantIDs, goldenPositionCommitID, "planned")
	artifacts = append(artifacts, projectionRepositoryChampionArtifact(
		fixture.participantIDs[0],
		artifacts[1].ID,
		"planned",
	))

	_, err := postgres.NewProjectionPostgres(postgres.NewTxManager(sharedPool)).Publish(
		ctx,
		postgres.ProjectionPublishInput{
			IDs: postgres.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
			Scope: postgres.ProjectionScope{
				TournamentID: fixture.tournamentID,
				RosterID:     fixture.rosterID,
			},
			Source: postgres.ProjectionSource{
				Kind: "golden_position", GoldenPositionCommitID: &goldenPositionCommitID,
				Reason: "planned playoff bracket must not choose a champion",
			},
			Artifacts:          artifacts,
			SupersessionReason: "not applicable",
			CutoffAt:           createdAt,
			CreatedAt:          createdAt,
			PublishedAt:        createdAt.Add(time.Second),
		},
	)
	require.ErrorIs(t, err, domain.ErrValidation)
}

func projectionRepositoryArtifacts(
	t testing.TB,
	participantIDs []uuid.UUID,
	goldenPositionCommitID uuid.UUID,
	version string,
) []postgres.ProjectionArtifactInput {
	t.Helper()
	standingsID := uuid.New()
	bracketID := uuid.New()
	topFourID := uuid.New()
	standingsMembers := make([]postgres.ProjectionMemberInput, 0, len(participantIDs))
	positionMembers := make([]postgres.ProjectionMemberInput, 0, len(participantIDs))
	entries := make([]map[string]any, 0, len(participantIDs))
	for index, participantID := range participantIDs {
		points := len(participantIDs) - index
		if version == "v2" {
			points += 10
		}
		score := int64(points * 1000)
		position := int32(index + 1)
		standingsMembers = append(standingsMembers, postgres.ProjectionMemberInput{
			ParticipantID: participantID, Position: position, ScoreMilli: &score,
		})
		positionMembers = append(positionMembers, postgres.ProjectionMemberInput{
			ParticipantID: participantID, Position: position,
		})
		entries = append(entries, map[string]any{
			"participant_id": participantID,
			"position":       position,
			"points":         points,
			"buchholz":       len(participantIDs) - index - 1,
			"effective_time": int64(index+1) * int64(time.Second),
		})
	}
	standingsPayload, err := json.Marshal(map[string]any{
		"version": version,
		"entries": entries,
	})
	require.NoError(t, err)
	bracketPayload, err := json.Marshal(map[string]any{
		"version": version,
		"rounds": []map[string]any{
			{
				"position":              1,
				"series_id":             uuid.New(),
				"first_participant_id":  participantIDs[0],
				"second_participant_id": participantIDs[3],
				"format":                "bo1",
				"state":                 "planned",
				"first_wins":            0,
				"second_wins":           0,
			},
			{
				"position":              2,
				"series_id":             uuid.New(),
				"first_participant_id":  participantIDs[1],
				"second_participant_id": participantIDs[2],
				"format":                "bo1",
				"state":                 "planned",
				"first_wins":            0,
				"second_wins":           0,
			},
		},
	})
	require.NoError(t, err)
	return []postgres.ProjectionArtifactInput{
		projectionRepositoryArtifact(
			standingsID,
			domain.ArtifactKindStandings,
			version,
			standingsPayload,
			standingsMembers,
			postgres.ProjectionDependencyInput{
				ID: uuid.New(), Kind: "golden_position", GoldenPositionCommitID: &goldenPositionCommitID,
			},
		),
		projectionRepositoryArtifact(
			bracketID,
			domain.ArtifactKindBracket,
			version,
			bracketPayload,
			positionMembers,
			postgres.ProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &standingsID,
			},
		),
		projectionRepositoryArtifact(
			topFourID,
			domain.ArtifactKindTopFour,
			version,
			json.RawMessage(fmt.Sprintf(
				`{"participants":[%q,%q,%q,%q]}`,
				participantIDs[0], participantIDs[1], participantIDs[2], participantIDs[3],
			)),
			positionMembers,
			postgres.ProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &bracketID,
			},
		),
	}
}

func projectionRepositoryChampionArtifact(
	winnerID uuid.UUID,
	bracketID uuid.UUID,
	version string,
) postgres.ProjectionArtifactInput {
	payload := json.RawMessage(fmt.Sprintf(`{"participant_id":%q}`, winnerID))
	digest := sha256.Sum256(payload)
	return postgres.ProjectionArtifactInput{
		ID: uuid.New(), Kind: domain.ArtifactKindChampion, Key: "champion-" + version,
		Payload: payload, PayloadDigest: digest,
		Members: []postgres.ProjectionMemberInput{{ParticipantID: winnerID, Position: 1}},
		Dependencies: []postgres.ProjectionDependencyInput{{
			ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &bracketID,
		}},
	}
}

func projectionRepositoryArtifact(
	id uuid.UUID,
	kind domain.ArtifactKind,
	version string,
	payload json.RawMessage,
	members []postgres.ProjectionMemberInput,
	dependency postgres.ProjectionDependencyInput,
) postgres.ProjectionArtifactInput {
	digest := sha256.Sum256(payload)
	return postgres.ProjectionArtifactInput{
		ID: id, Kind: kind, Key: string(kind) + "-" + version, Payload: payload,
		PayloadDigest: digest, Members: members,
		Dependencies: []postgres.ProjectionDependencyInput{dependency},
	}
}
