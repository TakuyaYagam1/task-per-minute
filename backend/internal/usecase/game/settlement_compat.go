package game

import gamesettlement "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/settlement"

var (
	ErrInvalidConcurrentWinnerSettlement = gamesettlement.ErrInvalidConcurrentWinnerSettlement
	ErrConcurrentWinnerUnavailable       = gamesettlement.ErrConcurrentWinnerUnavailable
	ErrConcurrentWinnerConflict          = gamesettlement.ErrConcurrentWinnerConflict
)

type SettlementAuthority = gamesettlement.SettlementAuthority
type SettlementCommand = gamesettlement.SettlementCommand
type SettlementGameResultRevision = gamesettlement.SettlementGameResultRevision
type SettlementRecord = gamesettlement.SettlementRecord
type SettlementRepository = gamesettlement.SettlementRepository
type SettlementUseCase = gamesettlement.SettlementUseCase

func SettlementNewUseCase(repository SettlementRepository) *SettlementUseCase {
	return gamesettlement.SettlementNewUseCase(repository)
}
