package postgres

import correctionpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/correction"

var (
	ErrCorrectionNotFound = correctionpostgres.ErrCorrectionNotFound
	ErrCorrectionCutoff   = correctionpostgres.ErrCorrectionCutoff
)

type CorrectionCutoffError = correctionpostgres.CorrectionCutoffError
type CorrectionDescendant = correctionpostgres.CorrectionDescendant
type CorrectionTraversal = correctionpostgres.CorrectionTraversal
type CorrectionInput = correctionpostgres.CorrectionInput
type CorrectionRecord = correctionpostgres.CorrectionRecord

// CorrectionPostgres keeps the historical root-package name while the result
// correction implementation lives in the child capability package.
type CorrectionPostgres = correctionpostgres.CorrectionPostgres

func NewCorrectionPostgres(tx *TxManager) *CorrectionPostgres {
	return correctionpostgres.NewCorrectionPostgres(tx)
}
