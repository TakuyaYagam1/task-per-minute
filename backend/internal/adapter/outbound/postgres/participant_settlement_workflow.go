package postgres

import settlementpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/settlement"

type ParticipantSettlementWorkflow = settlementpostgres.ParticipantSettlementWorkflow

func NewParticipantSettlementWorkflow(
	repository *ParticipantSettlementRepository,
) *ParticipantSettlementWorkflow {
	return settlementpostgres.NewParticipantSettlementWorkflow(repository)
}
