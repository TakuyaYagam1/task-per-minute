package resultprojection

import (
	canonicalusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/canonical"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

var ErrInvalidCanonicalMaterialization = canonicalusecase.ErrInvalidCanonicalMaterialization

type CanonicalSwissPointLedgerEntry = canonicalusecase.CanonicalSwissPointLedgerEntry
type CanonicalSwissParticipant = canonicalusecase.CanonicalSwissParticipant
type CanonicalBracketMatch = canonicalusecase.CanonicalBracketMatch
type CanonicalTopFourPosition = canonicalusecase.CanonicalTopFourPosition
type CanonicalMaterializationInput = canonicalusecase.CanonicalMaterializationInput
type CanonicalMaterializedMember = canonicalusecase.CanonicalMaterializedMember
type CanonicalMaterializedArtifact = canonicalusecase.CanonicalMaterializedArtifact
type CanonicalMaterialization = canonicalusecase.CanonicalMaterialization

func BuildCanonicalSwissRounds(entries []CanonicalSwissPointLedgerEntry) ([]swissusecase.Round, error) {
	return canonicalusecase.BuildCanonicalSwissRounds(entries)
}

func BuildCanonicalMaterialization(input CanonicalMaterializationInput) (CanonicalMaterialization, error) {
	return canonicalusecase.BuildCanonicalMaterialization(input)
}
