//go:build integration

package resultaudit

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/assignmentseed"
	"github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit/gameseed"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// AssignmentInput identifies the already selected task snapshot that backs
// the active assignment row. Plan and reservation state are prepared by the
// assignment fixture lane and are intentionally not reconstructed here.
type AssignmentInput struct {
	ID                     uuid.UUID
	PlanID                 uuid.UUID
	BranchID               uuid.UUID
	ReservationID          uuid.UUID
	SnapshotID             uuid.UUID
	TaskID                 uuid.UUID
	TaskVersion            int
	SupersedesAssignmentID *uuid.UUID
}

// PrepareInput supplies the typed identities owned by the draft and
// assignment fixture lanes. The builder creates only the game and result
// audit boundary that correction tests need.
type PrepareInput struct {
	TournamentID        uuid.UUID
	RosterID            uuid.UUID
	SeriesID            uuid.UUID
	ParticipantIDs      [2]uuid.UUID
	Assignment          AssignmentInput
	GameCategory        domain.Category
	GameCreatedAt       time.Time
	AssignmentCreatedAt time.Time
	LockedAt            time.Time
}

// PreparedScope contains the locked result scope and the slot identity used
// to create its active attempt.
type PreparedScope struct {
	Fixture Fixture
	SlotID  uuid.UUID
}

// PrepareScope creates the game assignment boundary and locks the planned
// series. It preserves the legacy independent slot, attempt, assignment and
// lock operations while keeping draft/content construction outside this
// package.
func PrepareScope(
	ctx context.Context,
	pool *pgxpool.Pool,
	input PrepareInput,
) (PreparedScope, error) {
	if pool == nil {
		return PreparedScope{}, fmt.Errorf("result audit seed: nil pool")
	}
	if input.TournamentID == uuid.Nil || input.RosterID == uuid.Nil || input.SeriesID == uuid.Nil ||
		input.ParticipantIDs[0] == uuid.Nil || input.ParticipantIDs[1] == uuid.Nil ||
		input.Assignment.PlanID == uuid.Nil || input.Assignment.BranchID == uuid.Nil ||
		input.Assignment.ReservationID == uuid.Nil || input.Assignment.SnapshotID == uuid.Nil ||
		input.Assignment.TaskID == uuid.Nil || input.LockedAt.IsZero() {
		return PreparedScope{}, fmt.Errorf("result audit seed: invalid scope input")
	}
	category := input.GameCategory
	if category == "" {
		category = domain.CategoryWeb
	}
	createdAt := input.GameCreatedAt
	if createdAt.IsZero() {
		createdAt = input.LockedAt.Add(-2 * time.Second)
	}
	assignmentCreatedAt := input.AssignmentCreatedAt
	if assignmentCreatedAt.IsZero() {
		assignmentCreatedAt = createdAt.Add(time.Second)
	}

	slotID, err := gameseed.CreateSlot(ctx, pool, gameseed.SlotInput{
		SeriesID:   input.SeriesID,
		RosterID:   input.RosterID,
		SlotNumber: 1,
		Category:   category,
		CreatedAt:  createdAt,
	})
	if err != nil {
		return PreparedScope{}, fmt.Errorf("result audit seed: create slot: %w", err)
	}
	attemptID, err := gameseed.CreateAttempt(
		ctx, pool, slotID, input.SeriesID, input.RosterID, createdAt,
	)
	if err != nil {
		return PreparedScope{}, fmt.Errorf("result audit seed: create attempt: %w", err)
	}

	assignmentID, err := assignmentseed.CreateActive(ctx, pool, assignmentseed.Input{
		ID:                     input.Assignment.ID,
		AttemptID:              attemptID,
		SeriesID:               input.SeriesID,
		RosterID:               input.RosterID,
		PlanID:                 input.Assignment.PlanID,
		BranchID:               input.Assignment.BranchID,
		ReservationID:          input.Assignment.ReservationID,
		SnapshotID:             input.Assignment.SnapshotID,
		TaskID:                 input.Assignment.TaskID,
		TaskVersion:            input.Assignment.TaskVersion,
		SupersedesAssignmentID: input.Assignment.SupersedesAssignmentID,
		CreatedAt:              assignmentCreatedAt,
	})
	if err != nil {
		return PreparedScope{}, fmt.Errorf("result audit seed: create assignment: %w", err)
	}

	fixture, err := LockSeries(ctx, pool, LockInput{
		Scope: Scope{
			TournamentID:   input.TournamentID,
			RosterID:       input.RosterID,
			SeriesID:       input.SeriesID,
			AttemptID:      attemptID,
			AssignmentID:   assignmentID,
			ParticipantIDs: input.ParticipantIDs,
		},
		LockedAt: input.LockedAt,
	})
	if err != nil {
		return PreparedScope{}, fmt.Errorf("result audit seed: lock series: %w", err)
	}
	return PreparedScope{Fixture: fixture, SlotID: slotID}, nil
}
