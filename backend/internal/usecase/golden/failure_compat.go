package golden

import (
	goldenfailure "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/failure"
)

type FailureClock = goldenfailure.FailureClock
type FailureRepository = goldenfailure.FailureRepository
type GoldenFailureRoute = goldenfailure.GoldenFailureRoute
type GoldenFailureClassificationKind = goldenfailure.GoldenFailureClassificationKind
type GoldenFailureEdge = goldenfailure.GoldenFailureEdge
type GoldenFailureClassification = goldenfailure.GoldenFailureClassification
type GoldenFailureActiveExecution = goldenfailure.GoldenFailureActiveExecution
type GoldenDiscardedOrderEntry = goldenfailure.GoldenDiscardedOrderEntry
type GoldenFailureReplacement = goldenfailure.GoldenFailureReplacement
type GoldenGroupOperationalState = goldenfailure.GoldenGroupOperationalState
type GoldenGroupTechnicalPause = goldenfailure.GoldenGroupTechnicalPause
type GoldenFailureExpectation = goldenfailure.GoldenFailureExpectation
type GoldenFailureAuthority = goldenfailure.GoldenFailureAuthority
type GoldenFailureRecord = goldenfailure.GoldenFailureRecord
type GoldenFailureCommit = goldenfailure.GoldenFailureCommit
type GoldenFailureReplayCommand = goldenfailure.GoldenFailureReplayCommand
type GoldenFailureReplayUseCase = goldenfailure.GoldenFailureReplayUseCase
type GoldenReserveExhaustionCommand = goldenfailure.GoldenReserveExhaustionCommand
type GoldenReserveExhaustionUseCase = goldenfailure.GoldenReserveExhaustionUseCase

var (
	ErrInvalidGoldenFailure           = goldenfailure.ErrInvalidGoldenFailure
	ErrGoldenFailureAuthorityConflict = goldenfailure.ErrGoldenFailureAuthorityConflict
	ErrGoldenFailureConflict          = goldenfailure.ErrGoldenFailureConflict
	ErrGoldenFailureCommandReuse      = goldenfailure.ErrGoldenFailureCommandReuse
	ErrGoldenFailureRouteConflict     = goldenfailure.ErrGoldenFailureRouteConflict
)

const (
	GoldenFailureRouteReplay    = goldenfailure.GoldenFailureRouteReplay
	GoldenFailureRouteExhausted = goldenfailure.GoldenFailureRouteExhausted

	GoldenFailureClassificationReplay    = goldenfailure.GoldenFailureClassificationReplay
	GoldenFailureClassificationExhausted = goldenfailure.GoldenFailureClassificationExhausted

	GoldenGroupStateTechnicalPause = goldenfailure.GoldenGroupStateTechnicalPause
)

func NewGoldenFailureActiveExecution(
	execution GoldenWaveExecution,
) (GoldenFailureActiveExecution, error) {
	return goldenfailure.NewGoldenFailureActiveExecution(execution)
}

func ClassifyGoldenFailure(
	plan ExactPlan,
	active GoldenFailureActiveExecution,
) (GoldenFailureClassification, error) {
	return goldenfailure.ClassifyGoldenFailure(plan, active)
}

func NewGoldenFailureReplayUseCase(
	repository FailureRepository,
	clock FailureClock,
) *GoldenFailureReplayUseCase {
	return goldenfailure.NewGoldenFailureReplayUseCase(repository, clock)
}

func NewGoldenReserveExhaustionUseCase(
	repository FailureRepository,
	clock FailureClock,
) *GoldenReserveExhaustionUseCase {
	return goldenfailure.NewGoldenReserveExhaustionUseCase(repository, clock)
}
