package postgres

import (
	"time"

	"github.com/google/uuid"

	assignmentadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
)

func taskSnapshotParams(
	edge AssignmentEdgeInput,
	createdAt time.Time,
) (sqlc.CreateAssignmentTaskSnapshotParams, error) {
	return assignmentadapter.TaskSnapshotParams(edge, createdAt)
}

func taskSnapshotRecord(row sqlc.TaskSnapshot) (TaskSnapshotRecord, error) {
	return assignmentadapter.MapTaskSnapshotRecord(row)
}

func exactNormalHistory(
	participantIDs []uuid.UUID,
	rows []sqlc.LockExactNormalAssignmentHistoryRow,
) ([]capacity.TaskUse, error) {
	return assignmentadapter.ExactNormalHistory(participantIDs, rows)
}
