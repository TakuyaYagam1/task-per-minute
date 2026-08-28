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
)

func TestArenaProjectionRepositoryPublishesScopedStandingsAndBracketHistory(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })

	fixture := createArenaGoldenMigrationFixture(t, ctx, 4)
	goldenPositionCommitID := createArenaProjectionGoldenSource(t, ctx, fixture)
	repository := postgres.NewArenaProjectionPostgres(postgres.NewTxManager(sharedPool))
	scope := postgres.ArenaProjectionScope{TournamentID: fixture.tournamentID, RosterID: fixture.rosterID}
	createdAt := time.Now().UTC().Truncate(time.Microsecond)

	first, err := repository.Publish(ctx, postgres.ArenaProjectionPublishInput{
		IDs:   postgres.ArenaProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: scope,
		Source: postgres.ArenaProjectionSource{
			Kind: "golden_position", GoldenPositionCommitID: &goldenPositionCommitID,
			Reason: "Golden positions changed the tournament projection",
		},
		Artifacts:          arenaProjectionRepositoryArtifacts(fixture.participantIDs, goldenPositionCommitID, "v1"),
		SupersessionReason: "replaced by a later tournament projection",
		CutoffAt:           createdAt, CreatedAt: createdAt, PublishedAt: createdAt.Add(time.Second),
	})
	require.NoError(t, err)
	require.Equal(t, "published", first.Revision.State)
	require.Len(t, first.Artifacts, 4)

	secondCreatedAt := createdAt.Add(time.Minute)
	second, err := repository.Publish(ctx, postgres.ArenaProjectionPublishInput{
		IDs:   postgres.ArenaProjectionIDs{RevisionID: uuid.New(), CutoffID: uuid.New()},
		Scope: scope,
		Source: postgres.ArenaProjectionSource{
			Kind: "operator_rebuild", Reason: "rebuild tournament scoped descendants",
		},
		Artifacts:          arenaProjectionRepositoryArtifacts(fixture.participantIDs, goldenPositionCommitID, "v2"),
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

	history, err := repository.History(ctx, scope)
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.Equal(t, "superseded", history[0].State)
	require.Equal(t, "published", history[1].State)

	_, err = repository.Current(ctx, postgres.ArenaProjectionScope{
		TournamentID: fixture.tournamentID, RosterID: uuid.New(),
	})
	require.ErrorIs(t, err, postgres.ErrArenaProjectionNotFound)

	var casualMutations int
	err = sharedPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM player_task_history
		WHERE player_id IN (
			SELECT player_id FROM arena_participants WHERE roster_id = $1
		)`, fixture.rosterID).Scan(&casualMutations)
	require.NoError(t, err)
	require.Zero(t, casualMutations)
}

func arenaProjectionRepositoryArtifacts(
	participantIDs []uuid.UUID,
	goldenPositionCommitID uuid.UUID,
	version string,
) []postgres.ArenaProjectionArtifactInput {
	standingsID := uuid.New()
	bracketID := uuid.New()
	topFourID := uuid.New()
	championID := uuid.New()
	standingsMembers := make([]postgres.ArenaProjectionMemberInput, 0, len(participantIDs))
	positionMembers := make([]postgres.ArenaProjectionMemberInput, 0, len(participantIDs))
	for index, participantID := range participantIDs {
		score := int64((len(participantIDs) - index) * 1000)
		position := int32(index + 1)
		standingsMembers = append(standingsMembers, postgres.ArenaProjectionMemberInput{
			ParticipantID: participantID, Position: position, ScoreMilli: &score,
		})
		positionMembers = append(positionMembers, postgres.ArenaProjectionMemberInput{
			ParticipantID: participantID, Position: position,
		})
	}
	return []postgres.ArenaProjectionArtifactInput{
		arenaProjectionRepositoryArtifact(
			standingsID,
			domain.ArenaArtifactKindStandings,
			version,
			json.RawMessage(fmt.Sprintf(`{"entries":[{"version":%q}]}`, version)),
			standingsMembers,
			postgres.ArenaProjectionDependencyInput{
				ID: uuid.New(), Kind: "golden_position", GoldenPositionCommitID: &goldenPositionCommitID,
			},
		),
		arenaProjectionRepositoryArtifact(
			bracketID,
			domain.ArenaArtifactKindBracket,
			version,
			json.RawMessage(fmt.Sprintf(`{"rounds":[{"version":%q}]}`, version)),
			positionMembers,
			postgres.ArenaProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &standingsID,
			},
		),
		arenaProjectionRepositoryArtifact(
			topFourID,
			domain.ArenaArtifactKindTopFour,
			version,
			json.RawMessage(fmt.Sprintf(
				`{"participants":[%q,%q,%q,%q]}`,
				participantIDs[0], participantIDs[1], participantIDs[2], participantIDs[3],
			)),
			positionMembers,
			postgres.ArenaProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &bracketID,
			},
		),
		arenaProjectionRepositoryArtifact(
			championID,
			domain.ArenaArtifactKindChampion,
			version,
			json.RawMessage(fmt.Sprintf(`{"participant_id":%q}`, participantIDs[0])),
			positionMembers[:1],
			postgres.ArenaProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &topFourID,
			},
		),
	}
}

func arenaProjectionRepositoryArtifact(
	id uuid.UUID,
	kind domain.ArenaArtifactKind,
	version string,
	payload json.RawMessage,
	members []postgres.ArenaProjectionMemberInput,
	dependency postgres.ArenaProjectionDependencyInput,
) postgres.ArenaProjectionArtifactInput {
	digest := sha256.Sum256(payload)
	return postgres.ArenaProjectionArtifactInput{
		ID: id, Kind: kind, Key: string(kind) + "-" + version, Payload: payload,
		PayloadDigest: digest, Members: members,
		Dependencies: []postgres.ArenaProjectionDependencyInput{dependency},
	}
}
