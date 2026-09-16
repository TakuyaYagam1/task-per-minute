//go:build integration

package integration_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func correctionProjectionArtifacts(
	participantIDs []uuid.UUID,
	sourceRevisionID uuid.UUID,
	seriesID uuid.UUID,
	version string,
) []projectionrepo.ProjectionArtifactInput {
	standingsID := uuid.New()
	bracketID := uuid.New()
	topFourID := uuid.New()
	standingsMembers := make([]projectionrepo.ProjectionMemberInput, 0, len(participantIDs))
	positionMembers := make([]projectionrepo.ProjectionMemberInput, 0, len(participantIDs))
	for index, participantID := range participantIDs {
		score := int64((len(participantIDs) - index) * 1000)
		position := int32(index + 1)
		standingsMembers = append(standingsMembers, projectionrepo.ProjectionMemberInput{
			ParticipantID: participantID, Position: position, ScoreMilli: &score,
		})
		positionMembers = append(positionMembers, projectionrepo.ProjectionMemberInput{
			ParticipantID: participantID, Position: position,
		})
	}
	return []projectionrepo.ProjectionArtifactInput{
		correctionProjectionArtifact(
			standingsID,
			domain.ArtifactKindStandings,
			version,
			json.RawMessage(fmt.Sprintf(`{"entries":[{"version":%q}]}`, version)),
			standingsMembers,
			projectionrepo.ProjectionDependencyInput{
				ID: uuid.New(), Kind: "official_result", OfficialResultRevisionID: &sourceRevisionID,
				OfficialResultSeriesID: &seriesID,
			},
		),
		correctionProjectionArtifact(
			bracketID,
			domain.ArtifactKindBracket,
			version,
			json.RawMessage(fmt.Sprintf(`{"rounds":[{"version":%q}]}`, version)),
			positionMembers,
			projectionrepo.ProjectionDependencyInput{
				ID: uuid.New(), Kind: "artifact", DependsOnArtifactID: &standingsID,
			},
		),
		correctionProjectionArtifact(
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

func correctionProjectionArtifact(
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
