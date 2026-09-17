package stage

import (
	"crypto/sha256"
	"errors"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/canonical"
)

var ErrInvalidMaterializedProjection = errors.New("invalid correction materialized projection")

// MaterializedProjectionState contains only locked normalized authority.
type MaterializedProjectionState struct {
	TournamentID          uuid.UUID
	CanonicalParticipants []resultprojection.CanonicalSwissParticipant
	SwissLedger           []resultprojection.CanonicalSwissPointLedgerEntry
	SwissComplete         bool
	TopFour               []resultprojection.CanonicalTopFourPosition
	Bracket               []resultprojection.CanonicalBracketMatch
	ArtifactKinds         []domain.ArtifactKind
}

type MaterializedProjectionMember struct {
	ParticipantID uuid.UUID
	Position      int
	ScoreMilli    *int64
}

type MaterializedProjectionArtifact struct {
	Kind                    domain.ArtifactKind
	Payload                 []byte
	PayloadDigest           [sha256.Size]byte
	Members                 []MaterializedProjectionMember
	GoldenPositionCommitIDs []uuid.UUID
}

type MaterializedProjections struct {
	Artifacts []MaterializedProjectionArtifact
}

// BuildMaterializedProjections delegates the canonical ordering and stage
// gates to result projection. Corrections never derive standings from wins or
// UUID order and never accept artifact payloads from an operator request.
func BuildMaterializedProjections(state MaterializedProjectionState) (MaterializedProjections, error) {
	if state.TournamentID == uuid.Nil || len(state.CanonicalParticipants) == 0 || len(state.SwissLedger) == 0 {
		return MaterializedProjections{}, ErrInvalidMaterializedProjection
	}
	materialized, err := resultprojection.BuildCanonicalMaterialization(resultprojection.CanonicalMaterializationInput{
		TournamentID: state.TournamentID, Participants: state.CanonicalParticipants, SwissLedger: state.SwissLedger,
		SwissComplete: state.SwissComplete, TopFour: state.TopFour, Bracket: state.Bracket,
		ArtifactKinds: state.ArtifactKinds,
	})
	if err != nil {
		return MaterializedProjections{}, ErrInvalidMaterializedProjection
	}
	artifacts := make([]MaterializedProjectionArtifact, len(materialized.Artifacts))
	for index, artifact := range materialized.Artifacts {
		members := make([]MaterializedProjectionMember, len(artifact.Members))
		for memberIndex, member := range artifact.Members {
			members[memberIndex] = MaterializedProjectionMember{
				ParticipantID: member.ParticipantID, Position: member.Position, ScoreMilli: cloneMaterializedScore(member.ScoreMilli),
			}
		}
		artifacts[index] = MaterializedProjectionArtifact{
			Kind: artifact.Kind, Payload: append([]byte(nil), artifact.Payload...), PayloadDigest: artifact.PayloadDigest, Members: members,
			GoldenPositionCommitIDs: append([]uuid.UUID(nil), artifact.GoldenPositionCommitIDs...),
		}
	}
	return MaterializedProjections{Artifacts: artifacts}, nil
}

func cloneMaterializedScore(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
