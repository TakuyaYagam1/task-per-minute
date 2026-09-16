package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	resultpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

// resultProjectionTargetBinding is retained for root execution and operator
// workflows that still use the private migration-era helper.
type resultProjectionTargetBinding struct {
	ID       uuid.UUID
	Revision int64
}

func resultProjectionTarget(
	source sqlc.LockResultSourceProjectionRow,
	targetID uuid.UUID,
) (resultProjectionTargetBinding, bool) {
	target, ok := resultpostgres.ResultProjectionTarget(source, targetID)
	if !ok {
		return resultProjectionTargetBinding{}, false
	}
	return resultProjectionTargetBinding{ID: target.ID, Revision: target.Revision}, true
}

func resultProjectionFinalizer(
	ctx context.Context,
	tx *TxManager,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	revisionID uuid.UUID,
	at time.Time,
) error {
	if err := NewTournamentProgressionPostgres(tx).persistFinalSwissReceipt(
		ctx,
		ProjectionScope{TournamentID: tournamentID, RosterID: rosterID},
		revisionID,
		at,
	); err != nil {
		return fmt.Errorf("result publication - final Swiss receipt: %w", err)
	}
	return nil
}

func publishResultProjection(
	ctx context.Context,
	tx *TxManager,
	in ResultSettlementInput,
	source sqlc.LockResultSourceProjectionRow,
) error {
	return resultpostgres.PublishResultProjectionWithFinalizer(ctx, tx, in, source, resultProjectionFinalizer)
}

// All settlement paths reach this boundary after their exact terminal heads
// and evidence exist. Participant settlement still calls this private root
// bridge while its workflow remains in the parent package.
func publishCommittedResultProjection(
	ctx context.Context,
	tx *TxManager,
	scope ProjectionScope,
	revisionID uuid.UUID,
	at time.Time,
) error {
	return resultpostgres.PublishCommittedResultProjection(
		ctx, tx, scope.TournamentID, scope.RosterID, revisionID, at, resultProjectionFinalizer,
	)
}
