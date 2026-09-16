package postgres

import (
	resultpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
)

var ErrResultNotFound = resultpostgres.ErrResultNotFound

type ResultPostgres = resultpostgres.ResultPostgres
type ResultScope = resultpostgres.ResultScope
type SubmissionInput = resultpostgres.SubmissionInput
type SubmissionRecord = resultpostgres.SubmissionRecord
type ResultSettlementIDs = resultpostgres.ResultSettlementIDs
type ResultSettlementInput = resultpostgres.ResultSettlementInput
type ResultProjectionPublication = resultpostgres.ResultProjectionPublication
type ResultCommitRecord = resultpostgres.ResultCommitRecord
type ResultHistoryRecord = resultpostgres.ResultHistoryRecord

func NewResultPostgres(tx *TxManager) *ResultPostgres {
	return resultauthority.NewResultPostgres(tx)
}
