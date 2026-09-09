package postgres

import (
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

var errReplayWorkflowAuthority = errors.New("invalid replay workflow authority")

// replayWorkflowReserveChain maps the immutable, locked assignment-plan
// evidence. The fourth position may exist only when it is bound to the
// operator command that authorized it. The active snapshot is supplied by the
// failed-attempt evidence rather than inferred from a mutable assignment head.
func replayWorkflowReserveChain(
	assignmentID uuid.UUID,
	activeSnapshotID uuid.UUID,
	rows []sqlc.LockReplayWorkflowReserveChainRow,
	operatorCommandID *uuid.UUID,
) (gameusecase.ReplayReserveChain, []sqlc.TaskVersionReservation, error) {
	if assignmentID == uuid.Nil || activeSnapshotID == uuid.Nil ||
		(len(rows) != domain.AssignmentReserveCount+1 && len(rows) != domain.AssignmentReserveCount+2) {
		return gameusecase.ReplayReserveChain{}, nil, errReplayWorkflowAuthority
	}

	snapshots := make([]domain.AssignmentTaskSnapshot, 0, len(rows))
	reservations := make([]sqlc.TaskVersionReservation, 0, len(rows))
	seenTasks := make(map[uuid.UUID]struct{}, len(rows))
	seenSnapshots := make(map[uuid.UUID]struct{}, len(rows))
	activeIndex := -1
	for index, row := range rows {
		position := index + 1
		edge := row.AssignmentPlanEdge
		reservation := row.TaskVersionReservation
		snapshotRow := row.TaskSnapshot
		if edge.ID == uuid.Nil || edge.Position != int16(position) || edge.PlanID == uuid.Nil ||
			edge.BranchID == uuid.Nil || edge.TaskID == uuid.Nil || edge.TaskVersion < 1 ||
			reservation.ID == uuid.Nil || reservation.EdgeID != edge.ID ||
			reservation.PlanID != edge.PlanID || reservation.BranchID != edge.BranchID ||
			reservation.TaskID != edge.TaskID || reservation.TaskVersion != edge.TaskVersion ||
			reservation.Revision < 1 || reservation.State != "committed" ||
			!reservation.CommittedAt.Valid || snapshotRow.ReservationID != reservation.ID ||
			snapshotRow.TaskID != edge.TaskID || snapshotRow.TaskVersion != edge.TaskVersion {
			return gameusecase.ReplayReserveChain{}, nil, errReplayWorkflowAuthority
		}
		if position <= domain.AssignmentReserveCount+1 {
			if edge.OperatorReserveCommandID.Valid {
				return gameusecase.ReplayReserveChain{}, nil, errReplayWorkflowAuthority
			}
		} else if operatorCommandID == nil || *operatorCommandID == uuid.Nil ||
			!edge.OperatorReserveCommandID.Valid || edge.OperatorReserveCommandID.UUID != *operatorCommandID {
			return gameusecase.ReplayReserveChain{}, nil, errReplayWorkflowAuthority
		}
		snapshot, err := replayWorkflowSnapshot(snapshotRow)
		if err != nil {
			return gameusecase.ReplayReserveChain{}, nil, err
		}
		if _, duplicate := seenTasks[snapshot.TaskID]; duplicate {
			return gameusecase.ReplayReserveChain{}, nil, errReplayWorkflowAuthority
		}
		if _, duplicate := seenSnapshots[snapshot.SnapshotID]; duplicate {
			return gameusecase.ReplayReserveChain{}, nil, errReplayWorkflowAuthority
		}
		seenTasks[snapshot.TaskID] = struct{}{}
		seenSnapshots[snapshot.SnapshotID] = struct{}{}
		if snapshot.SnapshotID == activeSnapshotID {
			activeIndex = index
		}
		snapshots = append(snapshots, snapshot)
		reservations = append(reservations, reservation)
	}
	if activeIndex < 0 {
		return gameusecase.ReplayReserveChain{}, nil, errReplayWorkflowAuthority
	}
	chain := gameusecase.ReplayReserveChain{
		AssignmentID: assignmentID,
		ActiveIndex:  activeIndex,
		Snapshots:    snapshots,
	}
	if err := chain.Validate(snapshots[0].Category); err != nil {
		return gameusecase.ReplayReserveChain{}, nil, fmt.Errorf("%w: reserve chain: %w", errReplayWorkflowAuthority, err)
	}
	return chain, reservations, nil
}

func replayWorkflowSnapshot(row sqlc.TaskSnapshot) (domain.AssignmentTaskSnapshot, error) {
	record, err := taskSnapshotRecord(row)
	if err != nil {
		return domain.AssignmentTaskSnapshot{}, fmt.Errorf("%w: snapshot: %w", errReplayWorkflowAuthority, err)
	}
	return record.Snapshot, nil
}
