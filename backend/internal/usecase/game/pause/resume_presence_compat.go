package pause

import resumepresence "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/resumepresence"

var (
	ErrInvalidPauseResumePresence      = resumepresence.ErrInvalidPauseResumePresence
	ErrPauseResumePresenceConflict     = resumepresence.ErrPauseResumePresenceConflict
	ErrPauseResumePresenceCommandReuse = resumepresence.ErrPauseResumePresenceCommandReuse
	ErrPauseResumePresenceIncomplete   = resumepresence.ErrPauseResumePresenceIncomplete
	ErrPauseResumePresenceIneligible   = resumepresence.ErrPauseResumePresenceIneligible
	ErrPauseResumePresenceOverflow     = resumepresence.ErrPauseResumePresenceOverflow
)

type PauseResumeDecisionScopeKind = resumepresence.PauseResumeDecisionScopeKind
type PauseResumeDecisionAuthority = resumepresence.PauseResumeDecisionAuthority
type PauseResumeDecisionExpectation = resumepresence.PauseResumeDecisionExpectation
type PauseResumeDecisionRecord = resumepresence.PauseResumeDecisionRecord
type PauseResumePresenceAuthority = resumepresence.PauseResumePresenceAuthority
type PauseResumePresenceExpectation = resumepresence.PauseResumePresenceExpectation
type PauseResumeIntervalInput = resumepresence.PauseResumeIntervalInput
type PauseResumePresenceCommand = resumepresence.PauseResumePresenceCommand
type PauseResumePresenceAction = resumepresence.PauseResumePresenceAction
type PauseResumeParticipantDisposition = resumepresence.PauseResumeParticipantDisposition
type PauseResumeParticipantResolution = resumepresence.PauseResumeParticipantResolution
type PauseResumePresenceRecord = resumepresence.PauseResumePresenceRecord
type PauseResumePresenceUseCase = resumepresence.PauseResumePresenceUseCase
type PauseResumePresenceRepository = resumepresence.PauseResumePresenceRepository

const (
	PauseResumeDecisionScopeSeries      = resumepresence.PauseResumeDecisionScopeSeries
	PauseResumeDecisionScopeGameAttempt = resumepresence.PauseResumeDecisionScopeGameAttempt
	PauseResumeActionResume             = resumepresence.PauseResumeActionResume
	PauseResumeActionWaitFirst          = resumepresence.PauseResumeActionWaitFirst
	PauseResumeActionWaitSecond         = resumepresence.PauseResumeActionWaitSecond
	PauseResumeActionWaitBoth           = resumepresence.PauseResumeActionWaitBoth
	PauseResumeParticipantConnected     = resumepresence.PauseResumeParticipantConnected
	PauseResumeParticipantContinuation  = resumepresence.PauseResumeParticipantContinuation
	PauseResumeParticipantFresh         = resumepresence.PauseResumeParticipantFresh
)

func NewPauseResumePresenceUseCase(transactions TransactionManager, repository PauseResumePresenceRepository, clock PauseClock) *PauseResumePresenceUseCase {
	return resumepresence.NewPauseResumePresenceUseCase(transactions, repository, clock)
}

func PauseResumeDecisionExpectationFrom(authority PauseResumeDecisionAuthority) PauseResumeDecisionExpectation {
	return resumepresence.PauseResumeDecisionExpectationFrom(authority)
}

func PauseResumePresenceExpectationFrom(authority PauseResumePresenceAuthority) PauseResumePresenceExpectation {
	return resumepresence.PauseResumePresenceExpectationFrom(authority)
}
