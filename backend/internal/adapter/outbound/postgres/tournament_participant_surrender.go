package postgres

import (
	participantsurrender "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/surrender"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

type ParticipantForfeitRepository = participantsurrender.ParticipantForfeitRepository
type ParticipantSurrenderWorkflow = participantsurrender.ParticipantSurrenderWorkflow

func NewParticipantForfeitRepository(
	tx *TxManager,
	results *ResultPostgres,
) *ParticipantForfeitRepository {
	return participantsurrender.NewParticipantForfeitRepository(tx, results)
}

func NewParticipantSurrenderWorkflow(
	repository *ParticipantForfeitRepository,
	clock gameusecase.ForfeitClock,
) *ParticipantSurrenderWorkflow {
	return participantsurrender.NewParticipantSurrenderWorkflow(repository, clock)
}
