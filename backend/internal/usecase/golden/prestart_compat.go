package golden

import (
	"github.com/google/uuid"

	goldenprestart "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/prestart"
)

type PrestartClock = goldenprestart.PrestartClock
type PrestartRepository = goldenprestart.PrestartRepository
type RetainedGoldenPrestartCommit = goldenprestart.RetainedGoldenPrestartCommit
type RetainedGoldenPrestartPauseCommand = goldenprestart.RetainedGoldenPrestartPauseCommand
type RetainedGoldenPrestartResumeCommand = goldenprestart.RetainedGoldenPrestartResumeCommand
type RetainedGoldenPrestartPauseUseCase = goldenprestart.RetainedGoldenPrestartPauseUseCase
type GoldenPrestartPauseReason = goldenprestart.GoldenPrestartPauseReason
type RetainedGoldenPrestartState = goldenprestart.RetainedGoldenPrestartState
type GoldenPrestartOperatorAuthorizationExpectation = goldenprestart.GoldenPrestartOperatorAuthorizationExpectation
type GoldenPrestartOperatorAuthorization = goldenprestart.GoldenPrestartOperatorAuthorization
type RetainedGoldenPrestartExpectation = goldenprestart.RetainedGoldenPrestartExpectation
type RetainedGoldenPrestartRecord = goldenprestart.RetainedGoldenPrestartRecord
type RetainedGoldenPrestartAuthority = goldenprestart.RetainedGoldenPrestartAuthority

var (
	ErrInvalidGoldenPrestartPause         = goldenprestart.ErrInvalidGoldenPrestartPause
	ErrGoldenPrestartAuthorityConflict    = goldenprestart.ErrGoldenPrestartAuthorityConflict
	ErrGoldenPrestartConflict             = goldenprestart.ErrGoldenPrestartConflict
	ErrGoldenPrestartCommandReuse         = goldenprestart.ErrGoldenPrestartCommandReuse
	ErrGoldenPrestartAlreadyStarted       = goldenprestart.ErrGoldenPrestartAlreadyStarted
	ErrGoldenPrestartSessionStateConflict = goldenprestart.ErrGoldenPrestartSessionStateConflict
)

const (
	GoldenPrestartPauseOperatorManual = goldenprestart.GoldenPrestartPauseOperatorManual
	RetainedGoldenPrestartPaused      = goldenprestart.RetainedGoldenPrestartPaused
	RetainedGoldenPrestartReady       = goldenprestart.RetainedGoldenPrestartReady
)

func NewRetainedGoldenPrestartPauseUseCase(
	repository PrestartRepository,
	clock PrestartClock,
) *RetainedGoldenPrestartPauseUseCase {
	return goldenprestart.NewRetainedGoldenPrestartPauseUseCase(repository, clock)
}

func NewGoldenPrestartOperatorAuthorization(
	tournamentID uuid.UUID,
	actorID uuid.UUID,
	revisionID uuid.UUID,
	revision int64,
) (GoldenPrestartOperatorAuthorization, error) {
	return goldenprestart.NewGoldenPrestartOperatorAuthorization(tournamentID, actorID, revisionID, revision)
}
