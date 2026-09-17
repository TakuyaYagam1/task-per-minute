package golden

import goldencontinuation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/continuation"

type ContinuationClock = goldencontinuation.ContinuationClock
type ContinuationRepository = goldencontinuation.ContinuationRepository
type GoldenContinuationCommand = goldencontinuation.GoldenContinuationCommand
type GoldenContinuationAuthority = goldencontinuation.GoldenContinuationAuthority
type GoldenContinuationRecord = goldencontinuation.GoldenContinuationRecord
type GoldenContinuationUseCase = goldencontinuation.GoldenContinuationUseCase

var (
	ErrInvalidGoldenContinuation           = goldencontinuation.ErrInvalidGoldenContinuation
	ErrGoldenContinuationAuthorityConflict = goldencontinuation.ErrGoldenContinuationAuthorityConflict
	ErrGoldenContinuationConflict          = goldencontinuation.ErrGoldenContinuationConflict
	ErrGoldenContinuationCommandReuse      = goldencontinuation.ErrGoldenContinuationCommandReuse
	ErrGoldenContinuationAlreadyCommitted  = goldencontinuation.ErrGoldenContinuationAlreadyCommitted
	ErrGoldenContinuationFallbackRequired  = goldencontinuation.ErrGoldenContinuationFallbackRequired
	ErrGoldenContinuationReservesExhausted = goldencontinuation.ErrGoldenContinuationReservesExhausted
)

func NewGoldenContinuationUseCase(
	repository ContinuationRepository,
	clock ContinuationClock,
) *GoldenContinuationUseCase {
	return goldencontinuation.NewGoldenContinuationUseCase(repository, clock)
}
