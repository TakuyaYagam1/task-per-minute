package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func TestFinalSwissReceiptSuccessorRetainsExactPredecessor(t *testing.T) {
	input := progressionTiedSwissInput(t)
	previous, err := playoff.PlanFinalSwissReceipt(input)
	require.NoError(t, err)
	root := sqlc.LockTournamentProgressionFinalSwissReceiptChainRow{ProjectionRevisionID: uuid.New(), TournamentID: input.TournamentID,
		RosterID: uuid.New(), PhysicalProjectionRevision: int64(input.PhysicalProjectionRevision) + 3, CreatedAt: tstz(input.CreatedAt.Add(time.Second))}
	got, err := finalSwissReceiptLineage(root, &previous)
	require.NoError(t, err)
	require.EqualValues(t, 2, got.ReceiptRevision)
	require.Equal(t, input.ProjectionID, got.CanonicalProjectionID)
	require.Equal(t, nullableUUIDValue(input.RevisionID.UUID()), got.PreviousReceiptProjectionRevisionID)
	for _, mutate := range []func(*sqlc.LockTournamentProgressionFinalSwissReceiptChainRow){
		func(row *sqlc.LockTournamentProgressionFinalSwissReceiptChainRow) { row.TournamentID = uuid.New() },
		func(row *sqlc.LockTournamentProgressionFinalSwissReceiptChainRow) {
			row.PhysicalProjectionRevision = int64(input.PhysicalProjectionRevision)
		},
		func(row *sqlc.LockTournamentProgressionFinalSwissReceiptChainRow) {
			row.ProjectionRevisionID = input.RevisionID.UUID()
		},
		func(row *sqlc.LockTournamentProgressionFinalSwissReceiptChainRow) {
			row.CreatedAt = tstz(input.CreatedAt.Add(-time.Second))
		},
	} {
		changed := root
		mutate(&changed)
		_, err := finalSwissReceiptLineage(changed, &previous)
		require.ErrorIs(t, err, domain.ErrConflict)
	}
}
