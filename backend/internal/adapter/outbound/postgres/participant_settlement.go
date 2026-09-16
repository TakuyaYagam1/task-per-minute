package postgres

import (
	resultauthority "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result/authority"
	settlementpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/settlement"
)

type ParticipantSettlementRepository = settlementpostgres.ParticipantSettlementRepository

func NewParticipantSettlementRepository(
	tx *TxManager,
	results *ResultPostgres,
) *ParticipantSettlementRepository {
	return settlementpostgres.NewParticipantSettlementRepositoryWithFinalizer(
		tx,
		results,
		resultauthority.FinalizeProjection,
	)
}
