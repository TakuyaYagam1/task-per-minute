package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	resultpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
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
	return resultauthority.FinalizeProjection(ctx, tx, tournamentID, rosterID, revisionID, at)
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
