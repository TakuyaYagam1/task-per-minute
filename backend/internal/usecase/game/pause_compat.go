package game

import (
	pauseusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
)

var (
	ErrInvalidNormalPauseGraph    = pauseusecase.ErrInvalidNormalPauseGraph
	ErrNormalPauseGraphConflict   = pauseusecase.ErrNormalPauseGraphConflict
	ErrNormalPauseCommandReuse    = pauseusecase.ErrNormalPauseCommandReuse
	ErrNormalPauseGraphIncomplete = pauseusecase.ErrNormalPauseGraphIncomplete
	ErrNormalPauseGoldenActive    = pauseusecase.ErrNormalPauseGoldenActive
	ErrNormalPauseDeadline        = pauseusecase.ErrNormalPauseDeadline
	ErrNormalPauseOverflow        = pauseusecase.ErrNormalPauseOverflow

	ErrInvalidPausedPresence      = pauseusecase.ErrInvalidPausedPresence
	ErrPausedPresenceConflict     = pauseusecase.ErrPausedPresenceConflict
	ErrPausedPresenceCommandReuse = pauseusecase.ErrPausedPresenceCommandReuse
	ErrPausedPresenceSuppression  = pauseusecase.ErrPausedPresenceSuppression
	ErrPausedPresenceState        = pauseusecase.ErrPausedPresenceState
	ErrPausedPresenceOverflow     = pauseusecase.ErrPausedPresenceOverflow

	ErrInvalidPauseResume      = pauseusecase.ErrInvalidPauseResume
	ErrPauseResumeConflict     = pauseusecase.ErrPauseResumeConflict
	ErrPauseResumeCommandReuse = pauseusecase.ErrPauseResumeCommandReuse
	ErrPauseResumeIncomplete   = pauseusecase.ErrPauseResumeIncomplete
	ErrPauseResumePresence     = pauseusecase.ErrPauseResumePresence
	ErrPauseResumeOverflow     = pauseusecase.ErrPauseResumeOverflow

	ErrInvalidPauseResumePresence      = pauseusecase.ErrInvalidPauseResumePresence
	ErrPauseResumePresenceConflict     = pauseusecase.ErrPauseResumePresenceConflict
	ErrPauseResumePresenceCommandReuse = pauseusecase.ErrPauseResumePresenceCommandReuse
	ErrPauseResumePresenceIncomplete   = pauseusecase.ErrPauseResumePresenceIncomplete
	ErrPauseResumePresenceIneligible   = pauseusecase.ErrPauseResumePresenceIneligible
	ErrPauseResumePresenceOverflow     = pauseusecase.ErrPauseResumePresenceOverflow
)

const (
	PauseReasonOperator       = pauseusecase.PauseReasonOperator
	PauseReasonDisconnect     = pauseusecase.PauseReasonDisconnect
	PauseReasonPlatform       = pauseusecase.PauseReasonPlatform
	PauseReasonExecutionEpoch = pauseusecase.PauseReasonExecutionEpoch

	PauseStateActive    = pauseusecase.PauseStateActive
	PauseStateResumed   = pauseusecase.PauseStateResumed
	PauseStateCancelled = pauseusecase.PauseStateCancelled

	PauseDeadlineReadyWindow = pauseusecase.PauseDeadlineReadyWindow
	PauseDeadlineGame        = pauseusecase.PauseDeadlineGame
	PauseDeadlineDraft       = pauseusecase.PauseDeadlineDraft

	PauseResumeDecisionScopeSeries      = pauseusecase.PauseResumeDecisionScopeSeries
	PauseResumeDecisionScopeGameAttempt = pauseusecase.PauseResumeDecisionScopeGameAttempt

	PauseResumeParticipantConnected    = pauseusecase.PauseResumeParticipantConnected
	PauseResumeParticipantContinuation = pauseusecase.PauseResumeParticipantContinuation
	PauseResumeParticipantFresh        = pauseusecase.PauseResumeParticipantFresh

	PauseResumeActionResume     = pauseusecase.PauseResumeActionResume
	PauseResumeActionWaitFirst  = pauseusecase.PauseResumeActionWaitFirst
	PauseResumeActionWaitSecond = pauseusecase.PauseResumeActionWaitSecond
	PauseResumeActionWaitBoth   = pauseusecase.PauseResumeActionWaitBoth
)

type NormalPauseAuthority = pauseusecase.NormalPauseAuthority
type NormalPauseCommand = pauseusecase.NormalPauseCommand
type NormalPauseGraphUseCase = pauseusecase.NormalPauseGraphUseCase
type NormalPauseRecord = pauseusecase.NormalPauseRecord
type NormalPauseRepository = pauseusecase.NormalPauseRepository

type PauseChildRevision = pauseusecase.PauseChildRevision
type PauseClock = pauseusecase.PauseClock
type PauseDeadlineKind = pauseusecase.PauseDeadlineKind
type PauseFrozenDeadline = pauseusecase.PauseFrozenDeadline
type PauseFrozenDeadlineRevision = pauseusecase.PauseFrozenDeadlineRevision
type PauseGame = pauseusecase.PauseGame
type PauseGraph = pauseusecase.PauseGraph
type PauseGraphRevisions = pauseusecase.PauseGraphRevisions
type PausePresenceRevision = pauseusecase.PausePresenceRevision
type PauseReason = pauseusecase.PauseReason
type PauseReconnectCounterRevision = pauseusecase.PauseReconnectCounterRevision
type PauseResumeAuthority = pauseusecase.PauseResumeAuthority
type PauseResumeCommand = pauseusecase.PauseResumeCommand
type PauseResumeDecisionAuthority = pauseusecase.PauseResumeDecisionAuthority
type PauseResumeDecisionExpectation = pauseusecase.PauseResumeDecisionExpectation
type PauseResumeDecisionRecord = pauseusecase.PauseResumeDecisionRecord
type PauseResumeDecisionScopeKind = pauseusecase.PauseResumeDecisionScopeKind
type PauseResumeExpectation = pauseusecase.PauseResumeExpectation
type PauseResumeIntervalInput = pauseusecase.PauseResumeIntervalInput
type PauseResumeParticipantDisposition = pauseusecase.PauseResumeParticipantDisposition
type PauseResumeParticipantResolution = pauseusecase.PauseResumeParticipantResolution
type PauseResumePresenceAction = pauseusecase.PauseResumePresenceAction
type PauseResumePresenceAuthority = pauseusecase.PauseResumePresenceAuthority
type PauseResumePresenceCommand = pauseusecase.PauseResumePresenceCommand
type PauseResumePresenceExpectation = pauseusecase.PauseResumePresenceExpectation
type PauseResumePresenceRecord = pauseusecase.PauseResumePresenceRecord
type PauseResumePresenceRepository = pauseusecase.PauseResumePresenceRepository
type PauseResumePresenceUseCase = pauseusecase.PauseResumePresenceUseCase
type PauseResumeRecord = pauseusecase.PauseResumeRecord
type PauseResumeRepository = pauseusecase.PauseResumeRepository
type PauseResumeUseCase = pauseusecase.PauseResumeUseCase
type PauseSeries = pauseusecase.PauseSeries
type PauseState = pauseusecase.PauseState
type PauseWave = pauseusecase.PauseWave
type PausedPresenceAuthority = pauseusecase.PausedPresenceAuthority
type PausedPresenceCommand = pauseusecase.PausedPresenceCommand
type PausedPresenceExpectation = pauseusecase.PausedPresenceExpectation
type PausedPresenceRecord = pauseusecase.PausedPresenceRecord
type PausedPresenceRepository = pauseusecase.PausedPresenceRepository
type PausedPresenceUseCase = pauseusecase.PausedPresenceUseCase
type TournamentRecord = pauseusecase.TournamentRecord
type TransactionManager = pauseusecase.TransactionManager

func NewNormalPauseGraphUseCase(transactions TransactionManager, repository NormalPauseRepository, clock PauseClock) *NormalPauseGraphUseCase {
	return pauseusecase.NewNormalPauseGraphUseCase(transactions, repository, clock)
}

func NewPauseResumeUseCase(transactions TransactionManager, repository PauseResumeRepository, clock PauseClock) *PauseResumeUseCase {
	return pauseusecase.NewPauseResumeUseCase(transactions, repository, clock)
}

func NewPauseResumePresenceUseCase(transactions TransactionManager, repository PauseResumePresenceRepository, clock PauseClock) *PauseResumePresenceUseCase {
	return pauseusecase.NewPauseResumePresenceUseCase(transactions, repository, clock)
}

func NewPausedPresenceUseCase(transactions TransactionManager, repository PausedPresenceRepository, clock PauseClock) *PausedPresenceUseCase {
	return pauseusecase.NewPausedPresenceUseCase(transactions, repository, clock)
}

func PauseGraphRevisionsFrom(graph PauseGraph) PauseGraphRevisions {
	return pauseusecase.PauseGraphRevisionsFrom(graph)
}

func PauseResumeExpectationFrom(authority PauseResumeAuthority) PauseResumeExpectation {
	return pauseusecase.PauseResumeExpectationFrom(authority)
}

func PauseResumeDecisionExpectationFrom(authority PauseResumeDecisionAuthority) PauseResumeDecisionExpectation {
	return pauseusecase.PauseResumeDecisionExpectationFrom(authority)
}

func PauseResumePresenceExpectationFrom(authority PauseResumePresenceAuthority) PauseResumePresenceExpectation {
	return pauseusecase.PauseResumePresenceExpectationFrom(authority)
}
