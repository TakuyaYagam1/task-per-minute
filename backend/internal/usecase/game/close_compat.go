package game

import closeusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/close"

var (
	ErrInvalidClosure      = closeusecase.ErrInvalidClosure
	ErrClosureBlocked      = closeusecase.ErrClosureBlocked
	ErrClosureConflict     = closeusecase.ErrClosureConflict
	ErrClosureCommandReuse = closeusecase.ErrClosureCommandReuse
)

type CloseScope = closeusecase.CloseScope
type CloseChild = closeusecase.CloseChild
type CloseCommand = closeusecase.CloseCommand
type CloseAuthority = closeusecase.CloseAuthority
type Closure = closeusecase.Closure
type CloseRepository = closeusecase.CloseRepository
type CloseUseCase = closeusecase.CloseUseCase

func NewCloseUseCase(repository CloseRepository, clock WaveClock) *CloseUseCase {
	return closeusecase.NewCloseUseCase(repository, clock)
}

// ReconcileClosure compares a replay command with a retained closure.
func ReconcileClosure(closure Closure, command CloseCommand) (*Closure, error) {
	return closeusecase.ReconcileClosure(closure, command)
}

// CloneClosure clones a retained closure at the execution boundary.
func CloneClosure(closure Closure) Closure {
	return closeusecase.CloneClosure(closure)
}

// ValidateCloseCommand validates a retained close command before replay.
func ValidateCloseCommand(command CloseCommand) error {
	return closeusecase.ValidateCloseCommand(command)
}
