package golden

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	planusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

type StandingsProjection = planusecase.StandingsProjection
type GroupMemberSeed = planusecase.GroupMemberSeed
type TieGroupSeed = planusecase.TieGroupSeed
type TieSegment = planusecase.TieSegment
type TiePartition = planusecase.TiePartition

type GroupRevisionPurpose = planusecase.GroupRevisionPurpose
type GroupRevisionEffect = planusecase.GroupRevisionEffect
type GroupRevisionCommand = planusecase.GroupRevisionCommand
type RevisionIdentity = planusecase.RevisionIdentity
type RevisionSnapshot = planusecase.RevisionSnapshot
type GroupRevision = planusecase.GroupRevision

const (
	GroupRevisionPurposeSeedOnly       = planusecase.GroupRevisionPurposeSeedOnly
	GroupRevisionEffectNoSwissMutation = planusecase.GroupRevisionEffectNoSwissMutation
)

var (
	ErrInvalidStandingsProjection = planusecase.ErrInvalidStandingsProjection
	ErrInvalidGroupRevision       = planusecase.ErrInvalidGroupRevision
	ErrGroupSourceStale           = planusecase.ErrGroupSourceStale
)

func NewStandingsProjection(
	tournamentID uuid.UUID,
	projectionID uuid.UUID,
	revisionID domain.DerivedRevisionID,
	revisionNo int,
	previousRevisionID *domain.DerivedRevisionID,
	final bool,
	standings []swissusecase.NormalStanding,
) (StandingsProjection, error) {
	return planusecase.NewStandingsProjection(
		tournamentID, projectionID, revisionID, revisionNo, previousRevisionID, final, standings,
	)
}

func PartitionTies(source StandingsProjection) (TiePartition, error) {
	return planusecase.PartitionTies(source)
}

func CloneGroupMemberSeeds(input []GroupMemberSeed) []GroupMemberSeed {
	return planusecase.CloneGroupMemberSeeds(input)
}

func BuildGroupRevision(
	command GroupRevisionCommand,
	source StandingsProjection,
	seed TieGroupSeed,
	predecessor *GroupRevision,
	used []RevisionIdentity,
) (GroupRevision, error) {
	return planusecase.BuildGroupRevision(command, source, seed, predecessor, used)
}

func RestoreGroupRevision(snapshot RevisionSnapshot) (GroupRevision, error) {
	return planusecase.RestoreGroupRevision(snapshot)
}

func ValidateGroupMember(member GroupMemberSeed) error {
	return planusecase.ValidateGroupMember(member)
}
