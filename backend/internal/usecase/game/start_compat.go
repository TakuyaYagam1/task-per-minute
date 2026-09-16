package game

import gamestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"

var (
	ErrInvalidWaveStart           = gamestart.ErrInvalidWaveStart
	ErrWaveStartAuthorityConflict = gamestart.ErrWaveStartAuthorityConflict
	ErrWaveStartConflict          = gamestart.ErrWaveStartConflict
)

type StartScope = gamestart.StartScope
type GameAuthority = gamestart.GameAuthority
type StartAuthority = gamestart.StartAuthority
type StartCommand = gamestart.StartCommand
type StartRecord = gamestart.StartRecord
type StartRepository = gamestart.StartRepository
type StartUseCase = gamestart.StartUseCase

func NewStartUseCase(repository StartRepository, clock WaveClock) *StartUseCase {
	return gamestart.NewStartUseCase(repository, clock)
}
