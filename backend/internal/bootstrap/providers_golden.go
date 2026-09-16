package bootstrap

import (
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	runtimerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/golden/runtime"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
)

func provideGoldenRuntimeRepository(tx *postgres.TxManager) *runtimerepo.GoldenRuntimePostgres {
	return runtimerepo.NewGoldenRuntimePostgres(tx)
}

func provideGoldenRuntimeApplication(
	repository *runtimerepo.GoldenRuntimePostgres,
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
