package progression

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// PrepareCorrectionFinalSwissReceiptPredecessor exposes the correction
// receipt preparation boundary to the admin correction capability. The
// implementation and validation remain owned by the progression package.
func (r *TournamentProgressionPostgres) PrepareCorrectionFinalSwissReceiptPredecessor(
	ctx context.Context,
	scope ProjectionScope,
	projectionID uuid.UUID,
	physicalRevision int64,
) (correctionFinalSwissReceiptPredecessor, error) {
	return r.prepareCorrectionFinalSwissReceiptPredecessor(ctx, scope, projectionID, physicalRevision)
}

// PersistCorrectionFinalSwissReceipt exposes the correction receipt write
// boundary without duplicating progression transaction logic in its caller.
func (r *TournamentProgressionPostgres) PersistCorrectionFinalSwissReceipt(
	ctx context.Context,
	scope ProjectionScope,
	projectionID, commandID uuid.UUID,
	predecessor correctionFinalSwissReceiptPredecessor,
	now time.Time,
) error {
	return r.persistCorrectionFinalSwissReceipt(ctx, scope, projectionID, commandID, predecessor, now)
}
