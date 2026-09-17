package admin

import observabilityusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/observability"

type Operation = observabilityusecase.Operation
type OperationEvent = observabilityusecase.OperationEvent
type OperationObserver = observabilityusecase.OperationObserver
type OperationClock = observabilityusecase.OperationClock

const (
	OperationOutcomeSuccess  = observabilityusecase.OperationOutcomeSuccess
	OperationOutcomeRetry    = observabilityusecase.OperationOutcomeRetry
	OperationOutcomeRejected = observabilityusecase.OperationOutcomeRejected
	OperationOutcomeFailure  = observabilityusecase.OperationOutcomeFailure

	OperationRosterReplace    = observabilityusecase.OperationRosterReplace
	OperationPreflightRun     = observabilityusecase.OperationPreflightRun
	OperationRosterLock       = observabilityusecase.OperationRosterLock
	OperationRosterUnlock     = observabilityusecase.OperationRosterUnlock
	OperationPairingConfigure = observabilityusecase.OperationPairingConfigure
	OperationTournamentAction = observabilityusecase.OperationTournamentAction
	OperationWaveControl      = observabilityusecase.OperationWaveControl
	OperationNoShowResolve    = observabilityusecase.OperationNoShowResolve
	OperationReserveAssign    = observabilityusecase.OperationReserveAssign
	OperationForfeitRecord    = observabilityusecase.OperationForfeitRecord
	OperationGameReplay       = observabilityusecase.OperationGameReplay
	OperationResultCorrect    = observabilityusecase.OperationResultCorrect
)
