package game

import noshowusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/noshow"

var (
	ErrInvalidNormalNoShow           = noshowusecase.ErrInvalidNormalNoShow
	ErrNormalNoShowCutoff            = noshowusecase.ErrNormalNoShowCutoff
	ErrNormalNoShowNotRequired       = noshowusecase.ErrNormalNoShowNotRequired
	ErrNormalNoShowAuthorityConflict = noshowusecase.ErrNormalNoShowAuthorityConflict
	ErrNormalNoShowConflict          = noshowusecase.ErrNormalNoShowConflict
)

type NoShowAuthority = noshowusecase.NoShowAuthority
type NoShowCommand = noshowusecase.NoShowCommand
type NoShowResolution = noshowusecase.NoShowResolution
type NoShowUseCase = noshowusecase.NoShowUseCase
type NoShowClock = noshowusecase.NoShowClock
type NoShowRepository = noshowusecase.NoShowRepository

func NoShowNewUseCase(repository NoShowRepository, clock NoShowClock) *NoShowUseCase {
	return noshowusecase.NoShowNewUseCase(repository, clock)
}
