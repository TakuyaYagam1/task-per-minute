//go:build integration

// Package assignmentseed provides reusable assignment fixtures for integration tests.
package assignmentseed

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

// Input identifies an assignment row and its existing immutable task snapshot.
// SupersedesAssignmentID is optional because the initial assignment has no
// predecessor.
type Input struct {
	ID                     uuid.UUID
	AttemptID              uuid.UUID
	SeriesID               uuid.UUID
	RosterID               uuid.UUID
	PlanID                 uuid.UUID
	BranchID               uuid.UUID
	ReservationID          uuid.UUID
	SnapshotID             uuid.UUID
	TaskID                 uuid.UUID
	TaskVersion            int
	SupersedesAssignmentID *uuid.UUID
	CreatedAt              time.Time
}

// CreateActive writes one assignment row through generated SQLC. It keeps
// the legacy fixture's single statement boundary and deliberately leaves
// plan and reservation validation to PostgreSQL.
func CreateActive(ctx context.Context, pool *pgxpool.Pool, input Input) (uuid.UUID, error) {
	if pool == nil {
		return uuid.Nil, fmt.Errorf("assignment seed: nil pool")
	}

	taskVersion := int32(input.TaskVersion)
	if int(taskVersion) != input.TaskVersion {
		return uuid.Nil, fmt.Errorf("assignment seed: task version out of range")
	}

	assignmentID := input.ID
	if assignmentID == uuid.Nil {
		assignmentID = uuid.New()
	}

	row, err := sqlc.New(pool).CreateAssignment(ctx, sqlc.CreateAssignmentParams{
		ID:                     assignmentID,
		AttemptID:              input.AttemptID,
		SeriesID:               input.SeriesID,
		RosterID:               input.RosterID,
		PlanID:                 input.PlanID,
		BranchID:               input.BranchID,
		ReservationID:          input.ReservationID,
		SnapshotID:             input.SnapshotID,
		TaskID:                 input.TaskID,
		TaskVersion:            taskVersion,
		SupersedesAssignmentID: nullableUUID(input.SupersedesAssignmentID),
		CreatedAt:              pgtype.Timestamptz{Time: input.CreatedAt, Valid: true},
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("assignment seed: create active assignment: %w", err)
	}
	return row.ID, nil
}

func nullableUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}
