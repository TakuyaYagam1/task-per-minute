package admin

import (
	"github.com/google/uuid"

	snapshotusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
)

type OperatorCursor = snapshotusecase.OperatorCursor
type SnapshotQuery = snapshotusecase.SnapshotQuery
type PauseGraphView = snapshotusecase.PauseGraphView
type OperatorSnapshotView = snapshotusecase.OperatorSnapshotView
type SnapshotPort = snapshotusecase.SnapshotPort

func validSnapshotQuery(query SnapshotQuery) bool {
	return snapshotusecase.ValidSnapshotQuery(query)
}

func validOperatorSnapshot(view OperatorSnapshotView, tournamentID uuid.UUID) bool {
	return snapshotusecase.ValidOperatorSnapshot(view, tournamentID)
}
