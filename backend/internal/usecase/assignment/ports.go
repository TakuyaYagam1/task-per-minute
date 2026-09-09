package assignment

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/google/uuid"
)

const (
	exactDraftBranchPlanAttempts = 2
	exactDraftBranchPlanProofV1  = "exact-draft-branch-plan-v1"
)

type ExactDraftBranchPlanState string

const (
	ExactDraftBranchPlanStatePlanned   ExactDraftBranchPlanState = "planned"
	ExactDraftBranchPlanStateCommitted ExactDraftBranchPlanState = "committed"
)

type ExactDraftBranchState string

const (
	ExactDraftBranchStateReserved ExactDraftBranchState = "reserved"
	ExactDraftBranchStateActive   ExactDraftBranchState = "active"
	ExactDraftBranchStateReleased ExactDraftBranchState = "released"
)

type ExactDraftReservationState string

const (
	ExactDraftReservationStateReserved  ExactDraftReservationState = "reserved"
	ExactDraftReservationStateCommitted ExactDraftReservationState = "committed"
	ExactDraftReservationStateReleased  ExactDraftReservationState = "released"
)

var (
	ErrInvalidExactDraftBranchPlan  = errors.New("invalid exact draft branch plan")
	ErrExactDraftBranchPlanConflict = errors.New("exact draft branch plan conflict")
	ErrExactDraftBranchPlanNotFound = errors.New("exact draft branch plan not found")
)

type ExactDraftBranchAction struct {
	Turn     int
	ActorID  uuid.UUID
	Action   domain.DraftActionType
	Category domain.Category
}

type ExactDraftBranchPath struct {
	Key        string
	Actions    []ExactDraftBranchAction
	Categories []domain.Category
}

type ExactDraftBranchCommand struct {
	// BranchID identifies the reachable draft outcome. ChildBranchIDs identify
	// its three persisted normal-assignment branches, one per final Game.
	BranchID       uuid.UUID
	ChildBranchIDs [3]uuid.UUID
	Key            string
	Assignments    []ExactNormalAssignmentCommand
}

type ExactDraftBranchPlanCommand struct {
	PlanID                  uuid.UUID
	PlanRevisionID          uuid.UUID
	DraftID                 uuid.UUID
	ExpectedDraftRevisionID uuid.UUID
	ExpectedDraftRevision   int64
	Branches                []ExactDraftBranchCommand
	CreatedAt               time.Time
}

type ExactDraftBranchAuthority struct {
	Key         string
	Assignments []ExactNormalAssignmentAuthority
}

type ExactDraftBranchPlanAuthority struct {
	Draft                   draftusecase.Execution
	Branches                []ExactDraftBranchAuthority
	UnavailableTaskVersions []domain.TaskVersionRef
}

type ExactDraftBranchAssignment struct {
	Position       int
	Plan           ExactNormalAssignmentPlan
	State          ExactDraftReservationState
	TransitionedAt time.Time
}

type ExactDraftBranch struct {
	ID             uuid.UUID
	ChildBranchIDs [3]uuid.UUID
	Path           ExactDraftBranchPath
	State          ExactDraftBranchState
	Assignments    []ExactDraftBranchAssignment
	ActivatedAt    time.Time
	ReleasedAt     time.Time
	ReleaseReason  string
}

type ExactDraftBranchPlan struct {
	ID                        uuid.UUID
	RevisionID                uuid.UUID
	SourceDraft               draftusecase.Execution
	State                     ExactDraftBranchPlanState
	Branches                  []ExactDraftBranch
	ActiveBranchID            uuid.UUID
	CompletionDraftRevisionID uuid.UUID
	CompletionDraftRevision   int64
	ActivationCommandID       uuid.UUID
	CompletedCategories       []domain.Category
	CreatedAt                 time.Time
	CommittedAt               time.Time
	ProofHash                 string
}

type ExactDraftBranchActivationCommand struct {
	PlanID                  uuid.UUID
	DraftID                 uuid.UUID
	ExpectedPlanRevisionID  uuid.UUID
	ExpectedDraftRevisionID uuid.UUID
	ExpectedDraftRevision   int64
	CommandID               uuid.UUID
	CommittedAt             time.Time
	ReleaseReason           string
}

// ExactDraftBranchPlanRepository owns the atomic source-revision checks and
// reservation writes for the full branch set. Activation commits one branch
// and releases every still-undisclosed alternative in the same transaction.
type ExactDraftBranchPlanRepository interface {
	LoadExactDraftBranchPlanAuthority(
		ctx context.Context,
		draftID uuid.UUID,
	) (ExactDraftBranchPlanAuthority, error)
	CommitExactDraftBranchPlan(
		ctx context.Context,
		plan ExactDraftBranchPlan,
	) (*ExactDraftBranchPlan, bool, error)
	LoadExactDraftBranchActivation(
		ctx context.Context,
		planID uuid.UUID,
	) (*ExactDraftBranchPlan, *draftusecase.Execution, error)
	CommitExactDraftBranchActivation(
		ctx context.Context,
		plan ExactDraftBranchPlan,
	) (*ExactDraftBranchPlan, bool, error)
}

type ExactDraftBranchPlanUseCase struct {
	repository ExactDraftBranchPlanRepository
}

func NewExactDraftBranchPlanUseCase(
	repository ExactDraftBranchPlanRepository,
) *ExactDraftBranchPlanUseCase {
	return &ExactDraftBranchPlanUseCase{repository: repository}
}

const exactNormalAssignmentAttempts = 2

var (
	ErrInvalidExactNormalAssignment  = errors.New("invalid exact normal assignment")
	ErrExactNormalAssignmentConflict = errors.New("exact normal assignment conflict")
)

type ExactNormalAssignmentScope struct {
	TournamentID   uuid.UUID
	RosterID       uuid.UUID
	SeriesID       uuid.UUID
	SlotID         uuid.UUID
	CategoryLockID uuid.UUID
}

type ExactNormalAssignmentSourceRevisions struct {
	SeriesRevision     int64
	PoolRevisionID     uuid.UUID
	PoolRevision       int64
	HistoryRevisionID  uuid.UUID
	HistoryRevision    int64
	RosterRevision     int64
	ArtifactRevisionID uuid.UUID
	ArtifactRevision   int64
	CategoryRevisionID uuid.UUID
	CategoryRevision   int64
}

type ExactNormalParticipantReservation struct {
	ParticipantID uuid.UUID
	PlayerID      uuid.UUID
	Reservation   domain.ParticipantReservation
}

type ExactNormalTaskVersion struct {
	PoolRevisionID uuid.UUID
	Version        int
	Task           domain.Task
}

type ExactNormalAssignmentAuthority struct {
	Scope                   ExactNormalAssignmentScope
	Category                domain.Category
	Revisions               ExactNormalAssignmentSourceRevisions
	Pool                    domain.TaskPoolRevision
	ParticipantIDs          []uuid.UUID
	ParticipantReservations []ExactNormalParticipantReservation
	History                 []capacity.TaskUse
	Candidates              []ExactNormalTaskVersion
	GraphDigest             [sha256.Size]byte
	ArtifactDigest          [sha256.Size]byte
}

type ExactNormalAssignmentCommand struct {
	Scope              ExactNormalAssignmentScope
	PlanID             uuid.UUID
	PlanRevisionID     uuid.UUID
	BranchID           uuid.UUID
	DecisionEvidenceID uuid.UUID
	EdgeIDs            [domain.AssignmentReserveCount + 1]uuid.UUID
	ReservationIDs     [domain.AssignmentReserveCount + 1]uuid.UUID
	SnapshotIDs        [domain.AssignmentReserveCount + 1]uuid.UUID
	CreatedAt          time.Time
}

type ExactNormalAssignmentEdge struct {
	ID            uuid.UUID
	ReservationID uuid.UUID
	Position      int
	Snapshot      domain.AssignmentTaskSnapshot
	ContentDigest [sha256.Size]byte
}

type ExactNormalAssignmentPlan struct {
	Scope                   ExactNormalAssignmentScope
	PlanID                  uuid.UUID
	PlanRevisionID          uuid.UUID
	BranchID                uuid.UUID
	Category                domain.Category
	Revisions               ExactNormalAssignmentSourceRevisions
	Pool                    domain.TaskPoolRevision
	ParticipantIDs          []uuid.UUID
	ParticipantReservations []ExactNormalParticipantReservation
	History                 []capacity.TaskUse
	CandidateTaskVersions   []domain.TaskVersionRef
	GraphDigest             [sha256.Size]byte
	ArtifactDigest          [sha256.Size]byte
	DecisionEvidence        domain.DecisionEvidence
	SelectedEdges           []ExactNormalAssignmentEdge
	CreatedAt               time.Time
	ProofHash               string
}

// ExactNormalAssignmentRepository owns one transaction that compares every
// source revision and digest before writing the plan, branch, selected edges,
// snapshots, and all three task reservations.
type ExactNormalAssignmentRepository interface {
	LoadExactNormalAssignmentAuthority(
		ctx context.Context,
		scope ExactNormalAssignmentScope,
	) (ExactNormalAssignmentAuthority, error)
	CommitExactNormalAssignment(
		ctx context.Context,
		plan ExactNormalAssignmentPlan,
	) (*ExactNormalAssignmentPlan, bool, error)
}

type ExactNormalAssignmentUseCase struct {
	repository ExactNormalAssignmentRepository
}

func NewExactNormalAssignmentUseCase(
	repository ExactNormalAssignmentRepository,
) *ExactNormalAssignmentUseCase {
	return &ExactNormalAssignmentUseCase{repository: repository}
}

var (
	ErrInvalidReserveAssignment  = errors.New("invalid reserve assignment")
	ErrReserveAssignmentConflict = errors.New("reserve assignment conflict")
)

type ReserveAssignmentMode string

const (
	ReserveAssignmentModeAutomatic ReserveAssignmentMode = "automatic"
	ReserveAssignmentModeOperator  ReserveAssignmentMode = "operator"
)

type ReserveAssignmentScope struct {
	TournamentID uuid.UUID
	AssignmentID uuid.UUID
	AttemptID    uuid.UUID
	SlotID       uuid.UUID
}

type ReserveAssignmentSourceRevisions struct {
	AssignmentRevision    int64
	PoolRevisionID        uuid.UUID
	PoolRevision          int64
	HistoryRevisionID     uuid.UUID
	HistoryRevision       int64
	ArtifactRevisionID    uuid.UUID
	ArtifactRevision      int64
	ReservationRevisionID uuid.UUID
	ReservationRevision   int64
	CategoryRevisionID    uuid.UUID
	CategoryRevision      int64
}

type ReserveCategoryExhaustionEvidence struct {
	RequiredCategory     domain.Category
	EligibleSameCategory []domain.TaskVersionRef
	Reason               string
	ProofDigest          [sha256.Size]byte
}

type ReserveAssignmentAuthority struct {
	Scope                   ReserveAssignmentScope
	Revisions               ReserveAssignmentSourceRevisions
	CurrentSnapshotID       uuid.UUID
	RequiredCategory        domain.Category
	ParticipantIDs          []uuid.UUID
	ParticipantReservations []ExactNormalParticipantReservation
	Pool                    domain.TaskPoolRevision
	ReceiptHistory          []TaskReceiptRef
	CandidateHealth         domain.TaskVersionHealth
	CandidateSnapshot       domain.AssignmentTaskSnapshot
	CandidateContentDigest  [sha256.Size]byte
	CategoryExhaustion      *ReserveCategoryExhaustionEvidence
}

type ReserveAssignmentCommand struct {
	Scope              ReserveAssignmentScope
	ExpectedSnapshotID uuid.UUID
	EvidenceID         uuid.UUID
	Mode               ReserveAssignmentMode
	OperatorID         uuid.UUID
	Reason             string
	PromotedAt         time.Time
}

type ReserveAssignmentEvidence struct {
	ID         uuid.UUID
	Mode       ReserveAssignmentMode
	OperatorID uuid.UUID
	Reason     string
	TaskID     uuid.UUID
	Version    int
	SnapshotID uuid.UUID
	DecidedAt  time.Time
	Digest     [sha256.Size]byte
}

type ReserveAssignmentRecord struct {
	Scope                   ReserveAssignmentScope
	Revisions               ReserveAssignmentSourceRevisions
	FromSnapshotID          uuid.UUID
	RequiredCategory        domain.Category
	ParticipantIDs          []uuid.UUID
	ParticipantReservations []ExactNormalParticipantReservation
	Pool                    domain.TaskPoolRevision
	ReceiptHistory          []TaskReceiptRef
	CandidateHealth         domain.TaskVersionHealth
	Snapshot                domain.AssignmentTaskSnapshot
	ContentDigest           [sha256.Size]byte
	CategoryExhaustion      *ReserveCategoryExhaustionEvidence
	Evidence                ReserveAssignmentEvidence
	PromotedAt              time.Time
	ProofDigest             [sha256.Size]byte
}

// ReserveAssignmentRepository owns the transaction that locks the assignment
// and revalidates every source before promoting one reserve.
type ReserveAssignmentRepository interface {
	LoadReserveAssignmentAuthority(
		ctx context.Context,
		scope ReserveAssignmentScope,
	) (ReserveAssignmentAuthority, error)
	CommitReserveAssignment(
		ctx context.Context,
		record ReserveAssignmentRecord,
	) (*ReserveAssignmentRecord, bool, error)
}
