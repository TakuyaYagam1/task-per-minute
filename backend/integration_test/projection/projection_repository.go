//go:build integration

package projection

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/projectionseed"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	snapshotrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/snapshot"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func runProjectionRepositoryPublishesScopedStandingsAndBracketHistory(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, resetProjectionTables(ctx, sharedPool))
	t.Cleanup(func() { require.NoError(t, resetProjectionTables(ctx, sharedPool)) })

	fixture, err := createProjectionFixture(ctx, sharedPool, 4)
	require.NoError(t, err)
	goldenPositionCommitID := createProjectionGoldenSource(ctx, t, fixture)
	repository := projectionrepo.NewProjectionPostgres(postgres.NewTxManager(sharedPool))
	scope := projectionrepo.ProjectionScope{TournamentID: fixture.tournamentID, RosterID: fixture.rosterID}
	createdAt := time.Now().UTC().Truncate(time.Microsecond)

	first, err := repository.Publish(ctx, projectionrepo.ProjectionPublishInput{
		IDs:   projectionrepo.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: scope,
		Source: projectionrepo.ProjectionSource{
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
	second, err := repository.Publish(ctx, projectionrepo.ProjectionPublishInput{
		IDs:   projectionrepo.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: scope,
		Source: projectionrepo.ProjectionSource{
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

	snapshot, err := snapshotrepo.NewTournamentSnapshotPostgres(postgres.NewTxManager(sharedPool)).PublicSnapshot(
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

	_, err = repository.Current(ctx, projectionrepo.ProjectionScope{
		TournamentID: fixture.tournamentID, RosterID: uuid.New(),
	})
	require.ErrorIs(t, err, projectionrepo.ErrProjectionNotFound)
}

func runProjectionRepositoryRejectsChampionBeforeTerminalFinal(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, resetProjectionTables(ctx, sharedPool))
	t.Cleanup(func() { require.NoError(t, resetProjectionTables(ctx, sharedPool)) })

	fixture, err := createProjectionFixture(ctx, sharedPool, 4)
	require.NoError(t, err)
	goldenPositionCommitID := createProjectionGoldenSource(ctx, t, fixture)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	artifacts := projectionRepositoryArtifacts(t, fixture.participantIDs, goldenPositionCommitID, "planned")
	artifacts = append(artifacts, projectionRepositoryChampionArtifact(
		fixture.participantIDs[0],
		artifacts[1].ID,
		"planned",
	))

	_, err = projectionrepo.NewProjectionPostgres(postgres.NewTxManager(sharedPool)).Publish(
		ctx,
		projectionrepo.ProjectionPublishInput{
			IDs: projectionrepo.ProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
			Scope: projectionrepo.ProjectionScope{
				TournamentID: fixture.tournamentID,
				RosterID:     fixture.rosterID,
			},
			Source: projectionrepo.ProjectionSource{
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

func createProjectionGoldenSource(
	ctx context.Context,
	tb testing.TB,
	fixture projectionFixture,
) uuid.UUID {
	tb.Helper()

	id, err := projectionseed.CreateGoldenSource(ctx, sharedPool, projectionseed.GoldenSourceInput{
		TournamentID:   fixture.tournamentID,
		RosterID:       fixture.rosterID,
		ParticipantIDs: fixture.participantIDs,
		CreatedAt:      fixture.createdAt,
	})
	require.NoError(tb, err)
	return id
}

func projectionRepositoryArtifacts(
	t testing.TB,
	participantIDs []uuid.UUID,
	goldenPositionCommitID uuid.UUID,
	version string,
) []projectionrepo.ProjectionArtifactInput {
	t.Helper()
	standingsID := uuid.New()
	bracketID := uuid.New()
	topFourID := uuid.New()
	standingsMembers := make([]projectionrepo.ProjectionMemberInput, 0, len(participantIDs))
	positionMembers := make([]projectionrepo.ProjectionMemberInput, 0, len(participantIDs))
	entries := make([]map[string]any, 0, len(participantIDs))
	for index, participantID := range participantIDs {
		points := len(participantIDs) - index
		if version == "v2" {
			points += 10
		}
		score := int64(points * 1000)
		position := int32(index + 1)
		standingsMembers = append(standingsMembers, projectionrepo.ProjectionMemberInput{
			ParticipantID: participantID, Position: position, ScoreMilli: &score,
		})
		positionMembers = append(positionMembers, projectionrepo.ProjectionMemberInput{
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
	return []projectionrepo.ProjectionArtifactInput{
		projectionRepositoryArtifact(
			standingsID,
			domain.ArtifactKindStandings,
			version,
			standingsPayload,
			standingsMembers,
			projectionrepo.ProjectionDependencyInput{
				ID: uuid.New(), Kind: "golden_position", GoldenPositionCommitID: &goldenPositionCommitID,
			},
		),
		projectionRepositoryArtifact(
			bracketID,
			domain.ArtifactKindBracket,
			version,
			bracketPayload,
			positionMembers,
			projectionrepo.ProjectionDependencyInput{
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
			projectionrepo.ProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &bracketID,
			},
		),
	}
}

func projectionRepositoryChampionArtifact(
	winnerID uuid.UUID,
	bracketID uuid.UUID,
	version string,
) projectionrepo.ProjectionArtifactInput {
	payload := json.RawMessage(fmt.Sprintf(`{"participant_id":%q}`, winnerID))
	digest := sha256.Sum256(payload)
	return projectionrepo.ProjectionArtifactInput{
		ID: uuid.New(), Kind: domain.ArtifactKindChampion, Key: "champion-" + version,
		Payload: payload, PayloadDigest: digest,
		Members: []projectionrepo.ProjectionMemberInput{{ParticipantID: winnerID, Position: 1}},
		Dependencies: []projectionrepo.ProjectionDependencyInput{{
			ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &bracketID,
		}},
	}
}

func projectionRepositoryArtifact(
	id uuid.UUID,
	kind domain.ArtifactKind,
	version string,
	payload json.RawMessage,
	members []projectionrepo.ProjectionMemberInput,
	dependency projectionrepo.ProjectionDependencyInput,
) projectionrepo.ProjectionArtifactInput {
	digest := sha256.Sum256(payload)
	return projectionrepo.ProjectionArtifactInput{
		ID: id, Kind: kind, Key: string(kind) + "-" + version, Payload: payload,
		PayloadDigest: digest, Members: members,
		Dependencies: []projectionrepo.ProjectionDependencyInput{dependency},
	}
}
