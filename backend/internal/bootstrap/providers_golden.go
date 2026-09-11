package bootstrap

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
)

func provideGoldenRuntimeRepository(tx *postgres.TxManager) *postgres.GoldenRuntimePostgres {
	return postgres.NewGoldenRuntimePostgres(tx)
}

func provideGoldenRuntimeApplication(
	repository *postgres.GoldenRuntimePostgres,
	clock clockFunc,
) *goldenusecase.RuntimeApplication {
	return goldenusecase.NewRuntimeApplication(repository, clock)
}

func provideTournamentProductionSnapshotSource(
	snapshots inbound.TournamentSnapshotUseCase,
	golden inbound.GoldenUseCase,
) (*websocket.TournamentProductionSnapshotSource, error) {
	return websocket.NewTournamentProductionSnapshotSource(snapshots, golden)
}
