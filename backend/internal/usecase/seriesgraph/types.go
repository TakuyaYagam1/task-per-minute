package seriesgraph

import (
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

var (
	ErrInvalidSeriesGraph      = errors.New("invalid series graph")
	ErrSeriesGraphConflict     = errors.New("series graph conflict")
	ErrSeriesGraphCommandReuse = errors.New("series graph command reuse")
)

const seriesGraphVersion = "series-graph-v1"

// MaterializeInput is the complete, already-authorized source set for one
// executable series graph. The materializer does not load or persist any of
// these values and never calls an infrastructure adapter.
type MaterializeInput struct {
	CommandID   uuid.UUID
	DeliveredAt time.Time

	Series             domain.Series
	CategoryRevision   draftusecase.CategoryRevision
	SelectedCategories []domain.Category
	AssignmentPlans    []assignmentusecase.ExactNormalAssignmentPlan
}

// TaskReservation is one immutable primary or reserve edge. It deliberately
// carries identifiers and digests only; private task content lives in the
// matching Snapshot field of SnapshotRecord.
type TaskReservation struct {
	ReservationID uuid.UUID
	EdgeID        uuid.UUID
	AssignmentID  uuid.UUID
	AttemptID     uuid.UUID
	SlotID        uuid.UUID
	Position      int
	SnapshotID    uuid.UUID
	TaskID        uuid.UUID
	Version       int
	Kind          domain.AssignmentTaskKind
	ContentDigest [sha256.Size]byte
}

// SnapshotRecord is the sole graph location for private task content.
type SnapshotRecord struct {
	AssignmentID  uuid.UUID
	AttemptID     uuid.UUID
	SlotID        uuid.UUID
	Position      int
	Snapshot      domain.AssignmentTaskSnapshot
	ContentDigest [sha256.Size]byte
}

// AssignmentAggregate combines the validated exact plan, the domain
// assignment aggregate, and delivery receipts for one initial game attempt.
type AssignmentAggregate struct {
	ID         uuid.UUID
	AttemptID  uuid.UUID
	SlotID     uuid.UUID
	Position   int
	Plan       assignmentusecase.ExactNormalAssignmentPlan
	Assignment domain.Assignment

	DeliveryReceipts []domain.TaskDeliveryReceipt
}

// SeriesGraphProof is replay evidence for the complete materialized graph.
// CommandID is repeated intentionally so a proof cannot be detached from the
// idempotency identity that produced it.
type SeriesGraphProof struct {
	Version          string
	CommandID        uuid.UUID
	CommandDigest    [sha256.Size]byte
	GraphDigest      [sha256.Size]byte
	ContentDigest    [sha256.Size]byte
	ProofDigest      [sha256.Size]byte
	ProofHash        string
	AssignmentProofs []string
}

// SeriesGraph is a detached, transport-neutral executable series graph. The
// Series field contains the current initial planned attempt; future positions
// remain ordered assignment bindings until score progression appends them.
type SeriesGraph struct {
	CommandID uuid.UUID

	Series             domain.Series
	CategoryRevision   draftusecase.CategoryRevision
	SelectedCategories []domain.Category

	Assignments             []AssignmentAggregate
	ParticipantReservations []assignmentusecase.ExactNormalParticipantReservation
	TaskReservations        []TaskReservation
	Snapshots               []SnapshotRecord
	DeliveryReceipts        []domain.TaskDeliveryReceipt

	Proof SeriesGraphProof
}
