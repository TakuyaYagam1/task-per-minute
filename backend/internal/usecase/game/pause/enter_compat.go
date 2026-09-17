package pause

import enterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/enter"

type NormalPauseGraphUseCase = enterusecase.NormalPauseGraphUseCase

func NewNormalPauseGraphUseCase(transactions TransactionManager, repository NormalPauseRepository, clock PauseClock) *NormalPauseGraphUseCase {
	return enterusecase.NewNormalPauseGraphUseCase(transactions, repository, clock)
}
