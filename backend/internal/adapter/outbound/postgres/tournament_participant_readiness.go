package postgres

import (
	readinesspostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/participant/readiness"
	readinessusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness"
)

type ParticipantReadinessRepository = readinesspostgres.ParticipantReadinessRepository

func NewParticipantReadinessRepository(
	tx *TxManager,
	waves *WavePostgres,
) *ParticipantReadinessRepository {
	return readinesspostgres.NewParticipantReadinessRepository(tx, waves)
}

var _ readinessusecase.ReadinessRepository = (*ParticipantReadinessRepository)(nil)
