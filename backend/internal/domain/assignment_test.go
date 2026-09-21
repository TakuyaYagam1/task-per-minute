package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func TestAssignmentLocksImmutableTaskVersions(t *testing.T) {
	primary := assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "primary")
	firstReserve := assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "reserve-one")
	secondReserve := assignmentTaskSnapshot(t, domain.AssignmentTaskKindGolden, "reserve-two")
	inputHints := primary.Hints
	assignment, _, _ := assignmentFixture(t, primary, []domain.AssignmentTaskSnapshot{firstReserve, secondReserve})

	primary.Title = "mutated source"
	inputHints[0] = "mutated hint"
	active := assignment.ActiveSnapshot()
	if active.Title == primary.Title || active.Hints[0] == inputHints[0] {
		t.Fatalf("assignment retained mutable input aliases: %+v", active)
	}
	active.Title = "mutated getter"
	active.Hints[0] = "mutated getter hint"
	locked := assignment.ActiveSnapshot()
	if locked.Title == active.Title || locked.Hints[0] == active.Hints[0] {
		t.Fatalf("active snapshot getter leaked mutable aliases: %+v", locked)
	}
	if locked.SnapshotID != primary.SnapshotID || locked.Version != primary.Version {
		t.Fatalf("locked version changed: %+v", locked)
	}
}

func TestAssignmentRecordsDeliveryPerParticipantAndForbidsRepeat(t *testing.T) {
	primary := assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "primary")
	assignment, firstID, secondID := assignmentFixture(t, primary, []domain.AssignmentTaskSnapshot{
		assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "reserve-one"),
		assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "reserve-two"),
	})
	history, err := domain.NewParticipantTaskHistory(nil)
	if err != nil {
		t.Fatalf("new history: %v", err)
	}
	deliveredAt := time.Date(2026, time.August, 27, 13, 0, 0, 0, time.UTC)

	firstReceipt, err := assignment.DeliverTo(uuid.New(), firstID, deliveredAt, &history)
	if err != nil {
		t.Fatalf("deliver first participant: %v", err)
	}
	secondReceipt, err := assignment.DeliverTo(uuid.New(), secondID, deliveredAt, &history)
	if err != nil {
		t.Fatalf("deliver second participant: %v", err)
	}
	if firstReceipt.TaskID != primary.TaskID || secondReceipt.SnapshotID != primary.SnapshotID {
		t.Fatalf("receipts reference wrong snapshot: %+v %+v", firstReceipt, secondReceipt)
	}
	if firstReceipt.InstanceID != domain.ParticipantTaskInstanceID(firstReceipt.AssignmentID, firstID) ||
		secondReceipt.InstanceID != domain.ParticipantTaskInstanceID(secondReceipt.AssignmentID, secondID) {
		t.Fatalf("receipts have unstable participant instances: %+v %+v", firstReceipt, secondReceipt)
	}
	if len(assignment.Receipts()) != 2 || len(history.Receipts()) != 2 {
		t.Fatalf("receipt counts assignment=%d history=%d", len(assignment.Receipts()), len(history.Receipts()))
	}
	if _, err := assignment.DeliverTo(uuid.New(), firstID, deliveredAt, &history); !errors.Is(err, domain.ErrAssignmentRepeatReceipt) {
		t.Fatalf("repeat receipt error = %v", err)
	}
}

func TestAssignmentRejectsTaskAlreadyReceivedInEarlierAttempt(t *testing.T) {
	primary := assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "primary")
	assignment, firstID, _ := assignmentFixture(t, primary, []domain.AssignmentTaskSnapshot{
		assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "reserve-one"),
		assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "reserve-two"),
	})
	deliveredAt := time.Date(2026, time.August, 27, 13, 0, 0, 0, time.UTC)
	priorAssignmentID := uuid.New()
	prior := domain.TaskDeliveryReceipt{
		ID:            uuid.New(),
		AssignmentID:  priorAssignmentID,
		AttemptID:     uuid.New(),
		ParticipantID: firstID,
		InstanceID:    domain.ParticipantTaskInstanceID(priorAssignmentID, firstID),
		SnapshotID:    uuid.New(),
		TaskID:        primary.TaskID,
		DeliveredAt:   deliveredAt,
	}
	history, err := domain.NewParticipantTaskHistory([]domain.TaskDeliveryReceipt{prior})
	if err != nil {
		t.Fatalf("new history: %v", err)
	}

	if _, err := assignment.DeliverTo(uuid.New(), firstID, deliveredAt, &history); !errors.Is(err, domain.ErrAssignmentRepeatReceipt) {
		t.Fatalf("historical repeat error = %v", err)
	}
}

func TestAssignmentKeepsTwoUndisclosedReservesInOrder(t *testing.T) {
	primary := assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "primary")
	firstReserve := assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "reserve-one")
	secondReserve := assignmentTaskSnapshot(t, domain.AssignmentTaskKindGolden, "reserve-two")
	assignment, _, _ := assignmentFixture(t, primary, []domain.AssignmentTaskSnapshot{firstReserve, secondReserve})

	if assignment.UndisclosedReserveCount() != 2 {
		t.Fatalf("reserve count = %d", assignment.UndisclosedReserveCount())
	}
	first, err := assignment.PromoteNextReserve()
	if err != nil {
		t.Fatalf("promote first reserve: %v", err)
	}
	if first.SnapshotID != firstReserve.SnapshotID || assignment.UndisclosedReserveCount() != 1 {
		t.Fatalf("first reserve order mismatch: %+v", first)
	}
	second, err := assignment.PromoteNextReserve()
	if err != nil {
		t.Fatalf("promote second reserve: %v", err)
	}
	if second.SnapshotID != secondReserve.SnapshotID || assignment.UndisclosedReserveCount() != 0 {
		t.Fatalf("second reserve order mismatch: %+v", second)
	}
	if _, err := assignment.PromoteNextReserve(); !errors.Is(err, domain.ErrAssignmentReservesExhaust) {
		t.Fatalf("reserve exhaustion error = %v", err)
	}
}

func TestAssignmentSupportsConfiguredReserveCountsWithoutEmptySnapshots(t *testing.T) {
	t.Parallel()

	for _, reserveCount := range []int{0, 1, 2} {
		primary := assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "primary")
		reserves := make([]domain.AssignmentTaskSnapshot, reserveCount)
		for index := range reserves {
			reserves[index] = assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "reserve-"+string(rune('1'+index)))
		}
		firstID, secondID := uuid.New(), uuid.New()
		assignment, err := domain.NewAssignmentWithReserveCount(
			uuid.New(), uuid.New(), firstID, secondID, primary, reserves, reserveCount,
		)
		if err != nil {
			t.Fatalf("reserve_count=%d: new assignment error = %v", reserveCount, err)
		}
		if assignment.ReserveCount() != reserveCount || assignment.UndisclosedReserveCount() != reserveCount {
			t.Fatalf("reserve_count=%d: counts = %d/%d", reserveCount, assignment.ReserveCount(), assignment.UndisclosedReserveCount())
		}
		if assignment.ActiveSnapshot().SnapshotID == uuid.Nil {
			t.Fatalf("reserve_count=%d: primary snapshot is empty", reserveCount)
		}
		for index := 0; index < reserveCount; index++ {
			next, promoteErr := assignment.PromoteNextReserve()
			if promoteErr != nil {
				t.Fatalf("reserve_count=%d: promote %d: %v", reserveCount, index, promoteErr)
			}
			if next.SnapshotID == uuid.Nil || assignment.ActiveSnapshot().SnapshotID != next.SnapshotID {
				t.Fatalf("reserve_count=%d: empty or out-of-order snapshot at %d", reserveCount, index)
			}
		}
		if assignment.UndisclosedReserveCount() != 0 {
			t.Fatalf("reserve_count=%d: undisclosed reserves remain", reserveCount)
		}
	}
}

func TestAssignmentRequiresExactlyTwoDistinctReserves(t *testing.T) {
	primary := assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "primary")
	participantA := uuid.New()
	participantB := uuid.New()
	for name, reserves := range map[string][]domain.AssignmentTaskSnapshot{
		"one":       {assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "reserve")},
		"duplicate": {primary, assignmentTaskSnapshot(t, domain.AssignmentTaskKindNormal, "reserve")},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := domain.NewAssignment(uuid.New(), uuid.New(), participantA, participantB, primary, reserves)
			if !errors.Is(err, domain.ErrInvalidAssignment) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func assignmentFixture(
	t *testing.T,
	primary domain.AssignmentTaskSnapshot,
	reserves []domain.AssignmentTaskSnapshot,
) (domain.Assignment, uuid.UUID, uuid.UUID) {
	t.Helper()
	firstID := uuid.New()
	secondID := uuid.New()
	assignment, err := domain.NewAssignment(uuid.New(), uuid.New(), firstID, secondID, primary, reserves)
	if err != nil {
		t.Fatalf("new assignment: %v", err)
	}
	return assignment, firstID, secondID
}

func assignmentTaskSnapshot(t *testing.T, kind domain.AssignmentTaskKind, title string) domain.AssignmentTaskSnapshot {
	t.Helper()
	taskURL := "https://tasks.example.test/" + title
	sourceURL := "https://assets.example.test/" + title
	task := domain.Task{
		ID:            uuid.New(),
		Title:         title,
		Description:   "synthetic task description",
		Category:      domain.CategoryWeb,
		Difficulty:    domain.DifficultyMedium,
		TimeLimit:     180,
		Flag:          "synthetic-flag",
		Hints:         []string{"first hint", "second hint"},
		TaskURL:       &taskURL,
		SourceFileURL: &sourceURL,
	}
	snapshot, err := domain.NewAssignmentTaskSnapshot(uuid.New(), 1, kind, task)
	if err != nil {
		t.Fatalf("new task snapshot: %v", err)
	}
	return snapshot
}
