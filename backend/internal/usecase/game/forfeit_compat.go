package game

import forfeitusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/forfeit"

var (
	ErrInvalidForfeit              = forfeitusecase.ErrInvalidForfeit
	ErrForfeitUnavailable          = forfeitusecase.ErrForfeitUnavailable
	ErrSurrenderDisconnected       = forfeitusecase.ErrSurrenderDisconnected
	ErrOperatorForfeitUnauthorized = forfeitusecase.ErrOperatorForfeitUnauthorized
	ErrForfeitAuthorityConflict    = forfeitusecase.ErrForfeitAuthorityConflict
	ErrForfeitCommandReuse         = forfeitusecase.ErrForfeitCommandReuse
)

const (
	SourceSurrender            = forfeitusecase.SourceSurrender
	SourceOperator             = forfeitusecase.SourceOperator
	OperatorBasisRuleViolation = forfeitusecase.OperatorBasisRuleViolation
)

type Source = forfeitusecase.Source
type OperatorBasis = forfeitusecase.OperatorBasis
type Scope = forfeitusecase.Scope
type GameExpectation = forfeitusecase.GameExpectation
type ForfeitRevisionSet = forfeitusecase.ForfeitRevisionSet
type OperatorEvidence = forfeitusecase.OperatorEvidence
type SurrenderCommand = forfeitusecase.SurrenderCommand
type OperatorCommand = forfeitusecase.OperatorCommand
type ForfeitAuthority = forfeitusecase.ForfeitAuthority
type ForfeitResolution = forfeitusecase.ForfeitResolution
type ForfeitUseCase = forfeitusecase.ForfeitUseCase
type ForfeitClock = forfeitusecase.ForfeitClock
type ForfeitRepository = forfeitusecase.ForfeitRepository
type GameRevision = forfeitusecase.GameRevision
type SeriesRevision = forfeitusecase.SeriesRevision

func ForfeitNewUseCase(repository ForfeitRepository, clock ForfeitClock) *ForfeitUseCase {
	return forfeitusecase.ForfeitNewUseCase(repository, clock)
}
