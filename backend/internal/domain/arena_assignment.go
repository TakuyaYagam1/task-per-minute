package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const ArenaAssignmentReserveCount = 2

type ArenaTaskKind string

const (
	ArenaTaskKindNormal ArenaTaskKind = "normal"
	ArenaTaskKindGolden ArenaTaskKind = "golden"
)

var (
	ErrInvalidArenaTaskSnapshot       = errors.New("invalid arena task snapshot")
	ErrInvalidArenaAssignment         = errors.New("invalid arena assignment")
	ErrArenaAssignmentParticipant     = errors.New("participant does not belong to arena assignment")
	ErrArenaAssignmentRepeatReceipt   = errors.New("participant already received arena task")
	ErrArenaAssignmentReservesExhaust = errors.New("arena assignment reserves exhausted")
)

type ArenaTaskSnapshot struct {
	SnapshotID    uuid.UUID
	TaskID        uuid.UUID
	Version       int
	Kind          ArenaTaskKind
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

type ArenaDeliveryReceipt struct {
	ID            uuid.UUID
	AssignmentID  uuid.UUID
	AttemptID     uuid.UUID
	ParticipantID uuid.UUID
	SnapshotID    uuid.UUID
	TaskID        uuid.UUID
	DeliveredAt   time.Time
}

type ArenaParticipantTaskHistory struct {
	receipts []ArenaDeliveryReceipt
}

type ArenaAssignment struct {
	id             uuid.UUID
	attemptID      uuid.UUID
	participantIDs [2]uuid.UUID
	snapshots      [ArenaAssignmentReserveCount + 1]ArenaTaskSnapshot
	activeIndex    int
	receipts       []ArenaDeliveryReceipt
}

func (k ArenaTaskKind) IsValid() bool {
	return k == ArenaTaskKindNormal || k == ArenaTaskKindGolden
}

func NewArenaTaskSnapshot(snapshotID uuid.UUID, version int, kind ArenaTaskKind, task Task) (ArenaTaskSnapshot, error) {
	snapshot := ArenaTaskSnapshot{
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
		return ArenaTaskSnapshot{}, err
	}
	return snapshot, nil
}

func (s ArenaTaskSnapshot) Validate() error {
	if s.SnapshotID == uuid.Nil || s.TaskID == uuid.Nil || s.Version < 1 {
		return fmt.Errorf("%w: missing version identity", ErrInvalidArenaTaskSnapshot)
	}
	if !s.Kind.IsValid() || !s.Category.IsValid() || !s.Difficulty.IsValid() {
		return fmt.Errorf("%w: invalid kind, category, or difficulty", ErrInvalidArenaTaskSnapshot)
	}
	if !IsValidTaskTitle(s.Title) || !IsValidTaskDescription(s.Description) || !IsValidTaskTimeLimit(s.TimeLimit) || !IsValidTaskFlag(s.Flag) {
		return fmt.Errorf("%w: invalid task content", ErrInvalidArenaTaskSnapshot)
	}
	if !IsValidTaskURLShape(s.Category, s.TaskURL, s.SourceFileURL) {
		return fmt.Errorf("%w: invalid task URL shape", ErrInvalidArenaTaskSnapshot)
	}
	return nil
}

func NewArenaParticipantTaskHistory(receipts []ArenaDeliveryReceipt) (ArenaParticipantTaskHistory, error) {
	history := ArenaParticipantTaskHistory{receipts: append([]ArenaDeliveryReceipt(nil), receipts...)}
	if err := history.Validate(); err != nil {
		return ArenaParticipantTaskHistory{}, err
	}
	return history, nil
}

func (h ArenaParticipantTaskHistory) Validate() error {
	seen := make(map[[2]uuid.UUID]struct{}, len(h.receipts))
	for _, receipt := range h.receipts {
		if err := receipt.Validate(); err != nil {
			return err
		}
		key := [2]uuid.UUID{receipt.ParticipantID, receipt.TaskID}
		if _, exists := seen[key]; exists {
			return ErrArenaAssignmentRepeatReceipt
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (h ArenaParticipantTaskHistory) HasReceived(participantID, taskID uuid.UUID) bool {
	for _, receipt := range h.receipts {
		if receipt.ParticipantID == participantID && receipt.TaskID == taskID {
			return true
		}
	}
	return false
}

func (h ArenaParticipantTaskHistory) Receipts() []ArenaDeliveryReceipt {
	return append([]ArenaDeliveryReceipt(nil), h.receipts...)
}

func (r ArenaDeliveryReceipt) Validate() error {
	if r.ID == uuid.Nil || r.AssignmentID == uuid.Nil || r.AttemptID == uuid.Nil || r.ParticipantID == uuid.Nil || r.SnapshotID == uuid.Nil || r.TaskID == uuid.Nil {
		return fmt.Errorf("%w: invalid delivery receipt identity", ErrInvalidArenaAssignment)
	}
	if r.DeliveredAt.IsZero() || r.DeliveredAt.Location() != time.UTC {
		return fmt.Errorf("%w: delivery timestamp must be server UTC", ErrInvalidArenaAssignment)
	}
	return nil
}

func NewArenaAssignment(
	id uuid.UUID,
	attemptID uuid.UUID,
	firstParticipantID uuid.UUID,
	secondParticipantID uuid.UUID,
	primary ArenaTaskSnapshot,
	reserves []ArenaTaskSnapshot,
) (ArenaAssignment, error) {
	if len(reserves) != ArenaAssignmentReserveCount {
		return ArenaAssignment{}, fmt.Errorf("%w: exactly two reserves required", ErrInvalidArenaAssignment)
	}
	assignment := ArenaAssignment{
		id:             id,
		attemptID:      attemptID,
		participantIDs: [2]uuid.UUID{firstParticipantID, secondParticipantID},
	}
	assignment.snapshots[0] = cloneArenaTaskSnapshot(primary)
	for i := range reserves {
		assignment.snapshots[i+1] = cloneArenaTaskSnapshot(reserves[i])
	}
	if err := assignment.Validate(); err != nil {
		return ArenaAssignment{}, err
	}
	return assignment, nil
}

func (a ArenaAssignment) Validate() error {
	if a.id == uuid.Nil || a.attemptID == uuid.Nil {
		return fmt.Errorf("%w: missing identity", ErrInvalidArenaAssignment)
	}
	if a.participantIDs[0] == uuid.Nil || a.participantIDs[1] == uuid.Nil || a.participantIDs[0] == a.participantIDs[1] {
		return fmt.Errorf("%w: invalid participants", ErrInvalidArenaAssignment)
	}
	if a.activeIndex < 0 || a.activeIndex >= len(a.snapshots) {
		return fmt.Errorf("%w: invalid active snapshot index", ErrInvalidArenaAssignment)
	}
	if err := a.validateSnapshots(); err != nil {
		return err
	}
	return a.validateReceipts()
}

func (a ArenaAssignment) ID() uuid.UUID {
	return a.id
}

func (a ArenaAssignment) AttemptID() uuid.UUID {
	return a.attemptID
}

func (a ArenaAssignment) ActiveSnapshot() ArenaTaskSnapshot {
	return cloneArenaTaskSnapshot(a.snapshots[a.activeIndex])
}

func (a ArenaAssignment) UndisclosedReserveCount() int {
	return len(a.snapshots) - a.activeIndex - 1
}

func (a ArenaAssignment) Receipts() []ArenaDeliveryReceipt {
	return append([]ArenaDeliveryReceipt(nil), a.receipts...)
}

func (a *ArenaAssignment) PromoteNextReserve() (ArenaTaskSnapshot, error) {
	if a == nil {
		return ArenaTaskSnapshot{}, fmt.Errorf("%w: nil assignment", ErrInvalidArenaAssignment)
	}
	if a.activeIndex+1 >= len(a.snapshots) {
		return ArenaTaskSnapshot{}, ErrArenaAssignmentReservesExhaust
	}
	a.activeIndex++
	return a.ActiveSnapshot(), nil
}

func (a *ArenaAssignment) DeliverTo(
	receiptID uuid.UUID,
	participantID uuid.UUID,
	deliveredAt time.Time,
	history *ArenaParticipantTaskHistory,
) (ArenaDeliveryReceipt, error) {
	if a == nil || history == nil {
		return ArenaDeliveryReceipt{}, fmt.Errorf("%w: nil assignment or history", ErrInvalidArenaAssignment)
	}
	if err := a.Validate(); err != nil {
		return ArenaDeliveryReceipt{}, err
	}
	if err := history.Validate(); err != nil {
		return ArenaDeliveryReceipt{}, err
	}
	if !a.hasParticipant(participantID) {
		return ArenaDeliveryReceipt{}, ErrArenaAssignmentParticipant
	}
	active := a.snapshots[a.activeIndex]
	if history.HasReceived(participantID, active.TaskID) || a.hasReceipt(participantID, active.TaskID) {
		return ArenaDeliveryReceipt{}, ErrArenaAssignmentRepeatReceipt
	}
	receipt := ArenaDeliveryReceipt{
		ID:            receiptID,
		AssignmentID:  a.id,
		AttemptID:     a.attemptID,
		ParticipantID: participantID,
		SnapshotID:    active.SnapshotID,
		TaskID:        active.TaskID,
		DeliveredAt:   deliveredAt.Round(0).UTC(),
	}
	if err := receipt.Validate(); err != nil {
		return ArenaDeliveryReceipt{}, err
	}
	a.receipts = append(a.receipts, receipt)
	history.receipts = append(history.receipts, receipt)
	return receipt, nil
}

func (a ArenaAssignment) validateSnapshots() error {
	seenTaskIDs := make(map[uuid.UUID]struct{}, len(a.snapshots))
	seenSnapshotIDs := make(map[uuid.UUID]struct{}, len(a.snapshots))
	for _, snapshot := range a.snapshots {
		if err := snapshot.Validate(); err != nil {
			return err
		}
		if _, exists := seenTaskIDs[snapshot.TaskID]; exists {
			return fmt.Errorf("%w: duplicate task in reserve chain", ErrInvalidArenaAssignment)
		}
		if _, exists := seenSnapshotIDs[snapshot.SnapshotID]; exists {
			return fmt.Errorf("%w: duplicate snapshot in reserve chain", ErrInvalidArenaAssignment)
		}
		seenTaskIDs[snapshot.TaskID] = struct{}{}
		seenSnapshotIDs[snapshot.SnapshotID] = struct{}{}
	}
	return nil
}

func (a ArenaAssignment) validateReceipts() error {
	seen := make(map[[2]uuid.UUID]struct{}, len(a.receipts))
	for _, receipt := range a.receipts {
		if err := receipt.Validate(); err != nil {
			return err
		}
		if receipt.AssignmentID != a.id || receipt.AttemptID != a.attemptID || !a.hasParticipant(receipt.ParticipantID) {
			return fmt.Errorf("%w: receipt belongs to another assignment", ErrInvalidArenaAssignment)
		}
		if !a.hasSnapshot(receipt.SnapshotID, receipt.TaskID) {
			return fmt.Errorf("%w: receipt references unknown snapshot", ErrInvalidArenaAssignment)
		}
		key := [2]uuid.UUID{receipt.ParticipantID, receipt.TaskID}
		if _, exists := seen[key]; exists {
			return ErrArenaAssignmentRepeatReceipt
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (a ArenaAssignment) hasParticipant(participantID uuid.UUID) bool {
	return a.participantIDs[0] == participantID || a.participantIDs[1] == participantID
}

func (a ArenaAssignment) hasReceipt(participantID, taskID uuid.UUID) bool {
	for _, receipt := range a.receipts {
		if receipt.ParticipantID == participantID && receipt.TaskID == taskID {
			return true
		}
	}
	return false
}

func (a ArenaAssignment) hasSnapshot(snapshotID, taskID uuid.UUID) bool {
	for _, snapshot := range a.snapshots {
		if snapshot.SnapshotID == snapshotID && snapshot.TaskID == taskID {
			return true
		}
	}
	return false
}

func cloneArenaTaskSnapshot(snapshot ArenaTaskSnapshot) ArenaTaskSnapshot {
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
