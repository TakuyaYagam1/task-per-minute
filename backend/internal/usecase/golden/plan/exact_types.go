package plan

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
)

type Scope struct {
	TournamentID uuid.UUID `json:"tournament_id"`
	PlanSetID    uuid.UUID `json:"plan_set_id"`
}

type Revisions struct {
	SourceProjectionRevisionID domain.DerivedRevisionID `json:"source_projection_revision_id"`
	GroupSetRevisionID         uuid.UUID                `json:"group_set_revision_id"`
	GroupSetRevision           int64                    `json:"group_set_revision"`
	PoolRevisionID             uuid.UUID                `json:"pool_revision_id"`
	PoolRevision               int64                    `json:"pool_revision"`
	HistoryRevisionID          uuid.UUID                `json:"history_revision_id"`
	HistoryRevision            int64                    `json:"history_revision"`
	TaskHealthRevisionID       uuid.UUID                `json:"task_health_revision_id"`
	TaskHealthRevision         int64                    `json:"task_health_revision"`
	ArtifactRevisionID         uuid.UUID                `json:"artifact_revision_id"`
	ArtifactRevision           int64                    `json:"artifact_revision"`
	ReservationRevisionID      uuid.UUID                `json:"reservation_revision_id"`
	ReservationRevision        int64                    `json:"reservation_revision"`
	MembershipRevisionID       uuid.UUID                `json:"membership_revision_id"`
	MembershipRevision         int64                    `json:"membership_revision"`
}

type Expectation struct {
	Revisions           Revisions         `json:"revisions"`
	SourcePayloadDigest [sha256.Size]byte `json:"source_payload_digest"`
	GroupDigest         [sha256.Size]byte `json:"group_digest"`
	PoolDigest          [sha256.Size]byte `json:"pool_digest"`
	HistoryDigest       [sha256.Size]byte `json:"history_digest"`
	TaskHealthDigest    [sha256.Size]byte `json:"task_health_digest"`
	ArtifactDigest      [sha256.Size]byte `json:"artifact_digest"`
	ReservationDigest   [sha256.Size]byte `json:"reservation_digest"`
	MembershipDigest    [sha256.Size]byte `json:"membership_digest"`
}

type GroupAuthority struct {
	Revision             GroupRevision
	ActiveParticipantIDs []uuid.UUID
}

type ParticipantReservation struct {
	ParticipantID uuid.UUID
	PlayerID      uuid.UUID
	Reservation   domain.ParticipantReservation
}

type TaskVersion struct {
	PoolRevisionID uuid.UUID
	Version        int
	Task           domain.Task
	Health         domain.TaskVersionHealth
	ArtifactDigest [sha256.Size]byte
}

type TaskReservation struct {
	TaskVersion    domain.TaskVersionRef
	ReservationID  uuid.UUID
	PlanID         uuid.UUID
	PlanRevisionID uuid.UUID
}

type Authority struct {
	Scope                    Scope
	Revisions                Revisions
	Source                   StandingsProjection
	Groups                   []GroupAuthority
	Pool                     domain.TaskPoolRevision
	History                  []assignmentusecase.TaskReceiptRef
	Candidates               []TaskVersion
	ParticipantReservations  []ParticipantReservation
	ExistingTaskReservations []TaskReservation
	Evidence                 Expectation
}

type GroupCommand struct {
	GroupID         uuid.UUID
	GroupRevisionID domain.DerivedRevisionID
	EdgeIDs         [domain.AssignmentReserveCount + 1]uuid.UUID
	ReservationIDs  [domain.AssignmentReserveCount + 1]uuid.UUID
	SnapshotIDs     [domain.AssignmentReserveCount + 1]uuid.UUID
}

type Command struct {
	Scope          Scope
	PlanID         uuid.UUID
	PlanRevisionID uuid.UUID
	Expected       Expectation
	GroupCommands  []GroupCommand
	CreatedAt      time.Time
}

type Edge struct {
	ID            uuid.UUID
	ReservationID uuid.UUID
	Position      int
	Snapshot      domain.AssignmentTaskSnapshot
	ContentDigest [sha256.Size]byte
}

// Group is opening evidence. Members, exclusions, topology and prior attempts
// stay frozen. Only the current Attempt and ParticipationEstablished may
// advance as derived execution evidence during the atomic start.
type Group struct {
	GroupID                    uuid.UUID
	GroupRevisionID            domain.DerivedRevisionID
	SourceProjectionRevisionID domain.DerivedRevisionID
	PositionFrom               int
	PositionTo                 int
	ParticipantIDs             []uuid.UUID
	Edges                      []Edge
}

type ExactPlan struct {
	Scope          Scope
	PlanID         uuid.UUID
	PlanRevisionID uuid.UUID
	Expected       Expectation
	Authority      Authority
	Groups         []Group
	CreatedAt      time.Time
	ProofHash      string
}

func (a Authority) Expectation() Expectation { return a.Evidence }

func (a Authority) Snapshot() Authority {
	clone := a
	clone.Source = a.Source.Snapshot()
	clone.Groups = make([]GroupAuthority, len(a.Groups))
	for index, group := range a.Groups {
		clone.Groups[index] = GroupAuthority{
			Revision:             group.Revision.Snapshot(),
			ActiveParticipantIDs: append([]uuid.UUID(nil), group.ActiveParticipantIDs...),
		}
	}
	clone.Pool = domain.CloneTaskPool(a.Pool)
	clone.History = append([]assignmentusecase.TaskReceiptRef(nil), a.History...)
	clone.Candidates = cloneGoldenCandidates(a.Candidates)
	clone.ParticipantReservations = append([]ParticipantReservation(nil), a.ParticipantReservations...)
	clone.ExistingTaskReservations = append([]TaskReservation(nil), a.ExistingTaskReservations...)
	return clone
}

func (p ExactPlan) Snapshot() ExactPlan {
	clone := p
	clone.Authority = p.Authority.Snapshot()
	clone.Groups = make([]Group, len(p.Groups))
	for groupIndex, group := range p.Groups {
		clone.Groups[groupIndex] = group
		clone.Groups[groupIndex].ParticipantIDs = append([]uuid.UUID(nil), group.ParticipantIDs...)
		clone.Groups[groupIndex].Edges = append([]Edge(nil), group.Edges...)
		for edgeIndex := range clone.Groups[groupIndex].Edges {
			clone.Groups[groupIndex].Edges[edgeIndex].Snapshot = CloneTaskSnapshot(group.Edges[edgeIndex].Snapshot)
		}
	}
	return clone
}

// PlanRepository owns one transaction. Commit must revalidate the full
// authority, compare every bound revision and digest, and atomically lock the
// plan plus every task reservation.
type PlanRepository interface {
	Get(ctx context.Context, scope Scope, planID uuid.UUID) (*ExactPlan, error)
	LoadAuthority(ctx context.Context, scope Scope) (Authority, error)
	Commit(ctx context.Context, plan ExactPlan) (*ExactPlan, bool, error)
}
