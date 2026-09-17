package replay

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func TestReplayWorkflowReserveChainAcceptsBoundedOperatorReserve(t *testing.T) {
	t.Parallel()

	assignmentID := uuid.New()
	operatorCommandID := uuid.New()
	rows := replayWorkflowReserveChainRows(4, &operatorCommandID)
	activeSnapshotID := rows[2].TaskSnapshot.ID

	chain, reservations, err := replayWorkflowReserveChain(
		assignmentID,
		activeSnapshotID,
		rows,
		&operatorCommandID,
	)

	require.NoError(t, err)
	require.Equal(t, assignmentID, chain.AssignmentID)
	require.Equal(t, 2, chain.ActiveIndex)
	require.Len(t, chain.Snapshots, 4)
	require.Len(t, reservations, 4)
}

func TestReplayWorkflowReserveChainRejectsUnboundFourthReserve(t *testing.T) {
	t.Parallel()

	rows := replayWorkflowReserveChainRows(4, nil)

	_, _, err := replayWorkflowReserveChain(uuid.New(), rows[2].TaskSnapshot.ID, rows, nil)

	require.ErrorIs(t, err, errReplayWorkflowAuthority)
}

func replayWorkflowReserveChainRows(
	count int,
	operatorCommandID *uuid.UUID,
) []sqlc.LockReplayWorkflowReserveChainRow {
	result := make([]sqlc.LockReplayWorkflowReserveChainRow, count)
	planID := uuid.New()
	branchID := uuid.New()
	createdAt := pgtype.Timestamptz{Time: time.Date(2026, 9, 6, 10, 20, 30, 0, time.UTC), Valid: true}
	for index := range result {
		position := index + 1
		edgeID := uuid.New()
		reservationID := uuid.New()
		result[index] = sqlc.LockReplayWorkflowReserveChainRow{
			AssignmentPlanEdge: sqlc.AssignmentPlanEdge{
				ID: edgeID, PlanID: planID, BranchID: branchID, Position: int16(position),
				TaskID: uuid.New(), TaskVersion: 1, CreatedAt: createdAt,
			},
			TaskVersionReservation: sqlc.TaskVersionReservation{
				ID: reservationID, EdgeID: edgeID, PlanID: planID, BranchID: branchID,
				TaskID: uuid.Nil, TaskVersion: 1, Revision: 1, State: "committed",
				CommittedAt: createdAt, CreatedAt: createdAt,
			},
			TaskSnapshot: sqlc.TaskSnapshot{
				ID: uuid.New(), ReservationID: reservationID, TaskID: uuid.Nil, TaskVersion: 1,
				Kind: "normal", Title: "task", Description: "synthetic task", Category: "web",
				Difficulty: "easy", TimeLimit: 60, Flag: "synthetic", Hints: []byte("[]"),
				ContentDigest: make([]byte, 32), CreatedAt: createdAt,
			},
		}
		result[index].TaskVersionReservation.TaskID = result[index].AssignmentPlanEdge.TaskID
		result[index].TaskSnapshot.TaskID = result[index].AssignmentPlanEdge.TaskID
		if position == 4 && operatorCommandID != nil {
			result[index].AssignmentPlanEdge.OperatorReserveCommandID = uuid.NullUUID{UUID: *operatorCommandID, Valid: true}
		}
	}
	return result
}
