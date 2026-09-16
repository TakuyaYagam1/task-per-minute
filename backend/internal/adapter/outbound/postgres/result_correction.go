package postgres

import (
	"context"

	"github.com/google/uuid"

	correctionpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrCorrectionNotFound = correctionpostgres.ErrCorrectionNotFound
	ErrCorrectionCutoff   = correctionpostgres.ErrCorrectionCutoff
)

type CorrectionCutoffError = correctionpostgres.CorrectionCutoffError
type CorrectionDescendant = correctionpostgres.CorrectionDescendant
type CorrectionTraversal = correctionpostgres.CorrectionTraversal
type CorrectionInput = correctionpostgres.CorrectionInput
type CorrectionRecord = correctionpostgres.CorrectionRecord

// CorrectionPostgres is a root-package facade for the result correction
// implementation. The wrapper keeps the transaction-owned private bridge
// used by the administrative correction workflow source-compatible.
type CorrectionPostgres struct {
	implementation *correctionpostgres.CorrectionPostgres
}

func NewCorrectionPostgres(tx *TxManager) *CorrectionPostgres {
	return &CorrectionPostgres{implementation: correctionpostgres.NewCorrectionPostgres(tx)}
}

func (r *CorrectionPostgres) Traverse(
	ctx context.Context,
	scope ResultScope,
	sourceRevisionID uuid.UUID,
) (*CorrectionTraversal, error) {
	if r == nil || r.implementation == nil {
		return nil, domain.ErrValidation
	}
	return r.implementation.Traverse(ctx, scope, sourceRevisionID)
}

func (r *CorrectionPostgres) Rebuild(
	ctx context.Context,
	in CorrectionInput,
) (*CorrectionRecord, error) {
	if r == nil || r.implementation == nil {
		return nil, domain.ErrValidation
	}
	return r.implementation.Rebuild(ctx, in)
}

// rebuildLocked is the compatibility bridge for the administrative
// correction workflow, which owns the surrounding transaction.
func (r *CorrectionPostgres) rebuildLocked(
	ctx context.Context,
	in CorrectionInput,
) (*CorrectionRecord, error) {
	if r == nil || r.implementation == nil {
		return nil, domain.ErrValidation
	}
	return r.implementation.RebuildLocked(ctx, in)
}

// correctionCutoffParams remains available to the root administrative
// workflow while the correction implementation lives in its child package.
func correctionCutoffParams(
	scope ResultScope,
	sourceRevisionID uuid.UUID,
) sqlc.GetCorrectionCutoffParams {
	return sqlc.GetCorrectionCutoffParams{
		RosterID: scope.RosterID, SeriesID: scope.SeriesID,
		TournamentID: scope.TournamentID, SourceRevisionID: sourceRevisionID,
	}
}
