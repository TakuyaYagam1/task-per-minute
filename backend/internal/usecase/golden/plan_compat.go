package golden

import (
	"crypto/sha256"

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
type Scope = planusecase.Scope
type Revisions = planusecase.Revisions
type Expectation = planusecase.Expectation
type GroupAuthority = planusecase.GroupAuthority
type ParticipantReservation = planusecase.ParticipantReservation
type TaskVersion = planusecase.TaskVersion
type TaskReservation = planusecase.TaskReservation
type Authority = planusecase.Authority
type GroupCommand = planusecase.GroupCommand
type Command = planusecase.Command
type Edge = planusecase.Edge
type Group = planusecase.Group
type ExactPlan = planusecase.ExactPlan
type PlanRepository = planusecase.PlanRepository
type UseCase = planusecase.UseCase

const (
	GroupRevisionPurposeSeedOnly       = planusecase.GroupRevisionPurposeSeedOnly
	GroupRevisionEffectNoSwissMutation = planusecase.GroupRevisionEffectNoSwissMutation
)

var (
	ErrInvalidStandingsProjection = planusecase.ErrInvalidStandingsProjection
	ErrInvalidGroupRevision       = planusecase.ErrInvalidGroupRevision
	ErrGroupSourceStale           = planusecase.ErrGroupSourceStale
	ErrInvalidExactPlan           = planusecase.ErrInvalidExactPlan
	ErrExactPlanStale             = planusecase.ErrExactPlanStale
	ErrExactPlanConflict          = planusecase.ErrExactPlanConflict
	ErrExactPlanInsufficient      = planusecase.ErrExactPlanInsufficient
)

func NewUseCase(repository PlanRepository) *UseCase {
	return planusecase.NewUseCase(repository)
}

func BuildAuthority(input Authority) (Authority, error) {
	return planusecase.BuildAuthority(input)
}

func BuildExactPlan(command Command, authority Authority) (ExactPlan, error) {
	return planusecase.BuildExactPlan(command, authority)
}

func AuthorityIdentityIDs(authority Authority) []uuid.UUID {
	return planusecase.AuthorityIdentityIDs(authority)
}

func TaskArtifactDigest(task domain.Task, version int) [sha256.Size]byte {
	return planusecase.TaskArtifactDigest(task, version)
}

func CloneTaskSnapshot(snapshot domain.AssignmentTaskSnapshot) domain.AssignmentTaskSnapshot {
	return planusecase.CloneTaskSnapshot(snapshot)
}

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
