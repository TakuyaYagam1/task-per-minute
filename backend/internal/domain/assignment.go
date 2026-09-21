package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	// MaxAssignmentReserveCount is the largest reserve chain supported by the
	// current tournament contract.
	MaxAssignmentReserveCount = 2
	// AssignmentReserveCount is retained as the legacy/default chain size for
	// callers that predate versioned reserve configuration. New workflows must
	// carry the configured count explicitly.
	AssignmentReserveCount = MaxAssignmentReserveCount
)

type AssignmentTaskKind string

const (
	AssignmentTaskKindNormal AssignmentTaskKind = "normal"
	AssignmentTaskKindGolden AssignmentTaskKind = "golden"
)

var (
	ErrInvalidAssignmentTaskSnapshot = errors.New("invalid task snapshot")
	ErrInvalidAssignment             = errors.New("invalid assignment")
	ErrAssignmentParticipant         = errors.New("participant does not belong to assignment")
	ErrAssignmentRepeatReceipt       = errors.New("participant already received task")
	ErrAssignmentReservesExhaust     = errors.New("assignment reserves exhausted")
)

type AssignmentTaskSnapshot struct {
	SnapshotID    uuid.UUID
	TaskID        uuid.UUID
	Version       int
	Kind          AssignmentTaskKind
	Title         string
	Description   string
	Category      Category
	Difficulty    Difficulty
	TimeLimit     int
	Flag          string
	Hints         []string
	TaskURL       *string
	SourceFileURL *string
}

type TaskDeliveryReceipt struct {
	ID            uuid.UUID
	AssignmentID  uuid.UUID
	AttemptID     uuid.UUID
	ParticipantID uuid.UUID
	InstanceID    uuid.UUID
	SnapshotID    uuid.UUID
	TaskID        uuid.UUID
	DeliveredAt   time.Time
}

type ParticipantTaskHistory struct {
	receipts []TaskDeliveryReceipt
}

type Assignment struct {
	id             uuid.UUID
	attemptID      uuid.UUID
	participantIDs [2]uuid.UUID
	snapshots      []AssignmentTaskSnapshot
	activeIndex    int
	receipts       []TaskDeliveryReceipt
}

func IsValidAssignmentReserveCount(count int) bool {
	return count >= 0 && count <= MaxAssignmentReserveCount
}

func (k AssignmentTaskKind) IsValid() bool {
	return k == AssignmentTaskKindNormal || k == AssignmentTaskKindGolden
}

func NewAssignmentTaskSnapshot(snapshotID uuid.UUID, version int, kind AssignmentTaskKind, task Task) (AssignmentTaskSnapshot, error) {
	snapshot := AssignmentTaskSnapshot{
		SnapshotID:    snapshotID,
		TaskID:        task.ID,
		Version:       version,
		Kind:          kind,
		Title:         task.Title,
		Description:   task.Description,
		Category:      task.Category,
		Difficulty:    task.Difficulty,
		TimeLimit:     task.TimeLimit,
		Flag:          task.Flag,
		Hints:         append([]string(nil), task.Hints...),
		TaskURL:       cloneStringPointer(task.TaskURL),
		SourceFileURL: cloneStringPointer(task.SourceFileURL),
	}
	if err := snapshot.Validate(); err != nil {
		return AssignmentTaskSnapshot{}, err
	}
	return snapshot, nil
}

func (s AssignmentTaskSnapshot) Validate() error {
	if s.SnapshotID == uuid.Nil || s.TaskID == uuid.Nil || s.Version < 1 {
		return fmt.Errorf("%w: missing version identity", ErrInvalidAssignmentTaskSnapshot)
	}
	if !s.Kind.IsValid() || !s.Category.IsValid() || !s.Difficulty.IsValid() {
		return fmt.Errorf("%w: invalid kind, category, or difficulty", ErrInvalidAssignmentTaskSnapshot)
	}
	if !IsValidTaskTitle(s.Title) || !IsValidTaskDescription(s.Description) || !IsValidTaskTimeLimit(s.TimeLimit) || !IsValidTaskFlag(s.Flag) {
		return fmt.Errorf("%w: invalid task content", ErrInvalidAssignmentTaskSnapshot)
	}
	if !IsValidTaskURLShape(s.Category, s.TaskURL, s.SourceFileURL) {
		return fmt.Errorf("%w: invalid task URL shape", ErrInvalidAssignmentTaskSnapshot)
	}
	return nil
}

func NewParticipantTaskHistory(receipts []TaskDeliveryReceipt) (ParticipantTaskHistory, error) {
	history := ParticipantTaskHistory{receipts: append([]TaskDeliveryReceipt(nil), receipts...)}
	if err := history.Validate(); err != nil {
		return ParticipantTaskHistory{}, err
	}
	return history, nil
}

func (h ParticipantTaskHistory) Validate() error {
	seen := make(map[[2]uuid.UUID]struct{}, len(h.receipts))
	for _, receipt := range h.receipts {
		if err := receipt.Validate(); err != nil {
			return err
		}
		key := [2]uuid.UUID{receipt.ParticipantID, receipt.TaskID}
		if _, exists := seen[key]; exists {
			return ErrAssignmentRepeatReceipt
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (h ParticipantTaskHistory) HasReceived(participantID, taskID uuid.UUID) bool {
	for _, receipt := range h.receipts {
		if receipt.ParticipantID == participantID && receipt.TaskID == taskID {
			return true
		}
	}
	return false
}

func (h ParticipantTaskHistory) Receipts() []TaskDeliveryReceipt {
	return append([]TaskDeliveryReceipt(nil), h.receipts...)
}

func (r TaskDeliveryReceipt) Validate() error {
	if r.ID == uuid.Nil || r.AssignmentID == uuid.Nil || r.AttemptID == uuid.Nil ||
		r.ParticipantID == uuid.Nil || r.InstanceID == uuid.Nil || r.SnapshotID == uuid.Nil || r.TaskID == uuid.Nil {
		return fmt.Errorf("%w: invalid delivery receipt identity", ErrInvalidAssignment)
	}
	if r.InstanceID != ParticipantTaskInstanceID(r.AssignmentID, r.ParticipantID) {
		return fmt.Errorf("%w: delivery receipt instance does not match participant", ErrInvalidAssignment)
	}
	if r.DeliveredAt.IsZero() || r.DeliveredAt.Location() != time.UTC {
		return fmt.Errorf("%w: delivery timestamp must be server UTC", ErrInvalidAssignment)
	}
	return nil
}

func NewAssignment(
	id uuid.UUID,
	attemptID uuid.UUID,
	firstParticipantID uuid.UUID,
	secondParticipantID uuid.UUID,
	primary AssignmentTaskSnapshot,
	reserves []AssignmentTaskSnapshot,
) (Assignment, error) {
	return NewAssignmentWithReserveCount(
		id, attemptID, firstParticipantID, secondParticipantID,
		primary, reserves, AssignmentReserveCount,
	)
}

// NewAssignmentWithReserveCount builds a primary task plus the configured
// number of reserves. The variadic legacy constructor above intentionally
// keeps the old two-reserve contract for callers that do not yet carry the
// tournament content configuration.
func NewAssignmentWithReserveCount(
	id uuid.UUID,
	attemptID uuid.UUID,
	firstParticipantID uuid.UUID,
	secondParticipantID uuid.UUID,
	primary AssignmentTaskSnapshot,
	reserves []AssignmentTaskSnapshot,
	reserveCount int,
) (Assignment, error) {
	if !IsValidAssignmentReserveCount(reserveCount) {
		return Assignment{}, fmt.Errorf("%w: invalid reserve count %d", ErrInvalidAssignment, reserveCount)
	}
	if len(reserves) != reserveCount {
		return Assignment{}, fmt.Errorf("%w: reserve count %d does not match %d snapshots", ErrInvalidAssignment, reserveCount, len(reserves))
	}
	assignment := Assignment{
		id:             id,
		attemptID:      attemptID,
		participantIDs: [2]uuid.UUID{firstParticipantID, secondParticipantID},
		snapshots:      make([]AssignmentTaskSnapshot, reserveCount+1),
	}
	assignment.snapshots[0] = cloneTaskSnapshot(primary)
	for i := range reserves {
		assignment.snapshots[i+1] = cloneTaskSnapshot(reserves[i])
	}
	if err := assignment.Validate(); err != nil {
		return Assignment{}, err
	}
	return assignment, nil
}

func (a Assignment) Validate() error {
	if a.id == uuid.Nil || a.attemptID == uuid.Nil {
		return fmt.Errorf("%w: missing identity", ErrInvalidAssignment)
	}
	if a.participantIDs[0] == uuid.Nil || a.participantIDs[1] == uuid.Nil || a.participantIDs[0] == a.participantIDs[1] {
		return fmt.Errorf("%w: invalid participants", ErrInvalidAssignment)
	}
	if a.activeIndex < 0 || a.activeIndex >= len(a.snapshots) {
		return fmt.Errorf("%w: invalid active snapshot index", ErrInvalidAssignment)
	}
	if err := a.validateSnapshots(); err != nil {
		return err
	}
	return a.validateReceipts()
}

func (a Assignment) ID() uuid.UUID {
	return a.id
}

func (a Assignment) AttemptID() uuid.UUID {
	return a.attemptID
}

func (a Assignment) ActiveSnapshot() AssignmentTaskSnapshot {
	return cloneTaskSnapshot(a.snapshots[a.activeIndex])
}

func (a Assignment) UndisclosedReserveCount() int {
	return len(a.snapshots) - a.activeIndex - 1
}

func (a Assignment) ReserveCount() int {
	if len(a.snapshots) == 0 {
		return 0
	}
	return len(a.snapshots) - 1
}

func (a Assignment) Receipts() []TaskDeliveryReceipt {
	return append([]TaskDeliveryReceipt(nil), a.receipts...)
}

func (a *Assignment) PromoteNextReserve() (AssignmentTaskSnapshot, error) {
	if a == nil {
		return AssignmentTaskSnapshot{}, fmt.Errorf("%w: nil assignment", ErrInvalidAssignment)
	}
	if a.activeIndex+1 >= len(a.snapshots) {
		return AssignmentTaskSnapshot{}, ErrAssignmentReservesExhaust
	}
	a.activeIndex++
	return a.ActiveSnapshot(), nil
}

func (a *Assignment) DeliverTo(
	receiptID uuid.UUID,
	participantID uuid.UUID,
	deliveredAt time.Time,
	history *ParticipantTaskHistory,
) (TaskDeliveryReceipt, error) {
	if a == nil || history == nil {
		return TaskDeliveryReceipt{}, fmt.Errorf("%w: nil assignment or history", ErrInvalidAssignment)
	}
	if err := a.Validate(); err != nil {
		return TaskDeliveryReceipt{}, err
	}
	if err := history.Validate(); err != nil {
		return TaskDeliveryReceipt{}, err
	}
	if !a.hasParticipant(participantID) {
		return TaskDeliveryReceipt{}, ErrAssignmentParticipant
	}
	active := a.snapshots[a.activeIndex]
	if history.HasReceived(participantID, active.TaskID) || a.hasReceipt(participantID, active.TaskID) {
		return TaskDeliveryReceipt{}, ErrAssignmentRepeatReceipt
	}
	receipt := TaskDeliveryReceipt{
		ID:            receiptID,
		AssignmentID:  a.id,
		AttemptID:     a.attemptID,
		ParticipantID: participantID,
		InstanceID:    ParticipantTaskInstanceID(a.id, participantID),
		SnapshotID:    active.SnapshotID,
		TaskID:        active.TaskID,
		DeliveredAt:   deliveredAt.Round(0).UTC(),
	}
	if err := receipt.Validate(); err != nil {
		return TaskDeliveryReceipt{}, err
	}
	a.receipts = append(a.receipts, receipt)
	history.receipts = append(history.receipts, receipt)
	return receipt, nil
}

func ParticipantTaskInstanceID(assignmentID, participantID uuid.UUID) uuid.UUID {
	if assignmentID == uuid.Nil || participantID == uuid.Nil {
		return uuid.Nil
	}
	return uuid.NewSHA1(assignmentID, participantID[:])
}

func (a Assignment) validateSnapshots() error {
	if len(a.snapshots) < 1 || len(a.snapshots) > MaxAssignmentReserveCount+1 {
		return fmt.Errorf("%w: invalid reserve chain length", ErrInvalidAssignment)
	}
	seenTaskIDs := make(map[uuid.UUID]struct{}, len(a.snapshots))
	seenSnapshotIDs := make(map[uuid.UUID]struct{}, len(a.snapshots))
	for _, snapshot := range a.snapshots {
		if err := snapshot.Validate(); err != nil {
			return err
		}
		if _, exists := seenTaskIDs[snapshot.TaskID]; exists {
			return fmt.Errorf("%w: duplicate task in reserve chain", ErrInvalidAssignment)
		}
		if _, exists := seenSnapshotIDs[snapshot.SnapshotID]; exists {
			return fmt.Errorf("%w: duplicate snapshot in reserve chain", ErrInvalidAssignment)
		}
		seenTaskIDs[snapshot.TaskID] = struct{}{}
		seenSnapshotIDs[snapshot.SnapshotID] = struct{}{}
	}
	return nil
}

func (a Assignment) validateReceipts() error {
	seen := make(map[[2]uuid.UUID]struct{}, len(a.receipts))
	for _, receipt := range a.receipts {
		if err := receipt.Validate(); err != nil {
			return err
		}
		if receipt.AssignmentID != a.id || receipt.AttemptID != a.attemptID || !a.hasParticipant(receipt.ParticipantID) {
			return fmt.Errorf("%w: receipt belongs to another assignment", ErrInvalidAssignment)
		}
		if !a.hasSnapshot(receipt.SnapshotID, receipt.TaskID) {
			return fmt.Errorf("%w: receipt references unknown snapshot", ErrInvalidAssignment)
		}
		key := [2]uuid.UUID{receipt.ParticipantID, receipt.TaskID}
		if _, exists := seen[key]; exists {
			return ErrAssignmentRepeatReceipt
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (a Assignment) hasParticipant(participantID uuid.UUID) bool {
	return a.participantIDs[0] == participantID || a.participantIDs[1] == participantID
}

func (a Assignment) hasReceipt(participantID, taskID uuid.UUID) bool {
	for _, receipt := range a.receipts {
		if receipt.ParticipantID == participantID && receipt.TaskID == taskID {
			return true
		}
	}
	return false
}

func (a Assignment) hasSnapshot(snapshotID, taskID uuid.UUID) bool {
	for _, snapshot := range a.snapshots {
		if snapshot.SnapshotID == snapshotID && snapshot.TaskID == taskID {
			return true
		}
	}
	return false
}

func cloneTaskSnapshot(snapshot AssignmentTaskSnapshot) AssignmentTaskSnapshot {
	clone := snapshot
	clone.Hints = append([]string(nil), snapshot.Hints...)
	clone.TaskURL = cloneStringPointer(snapshot.TaskURL)
	clone.SourceFileURL = cloneStringPointer(snapshot.SourceFileURL)
	return clone
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
