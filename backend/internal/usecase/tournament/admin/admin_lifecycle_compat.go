package admin

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	lifecycleworkflow "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/lifecycle"
)

type TournamentAction = lifecycleworkflow.TournamentAction
type TournamentActionCommand = lifecycleworkflow.TournamentActionCommand
type LifecyclePort = lifecycleworkflow.LifecyclePort
type LifecycleTransactionManager = lifecycleworkflow.LifecycleTransactionManager
type AdminLifecycleClock = lifecycleworkflow.AdminLifecycleClock
type LifecycleTransitioner = lifecycleworkflow.LifecycleTransitioner
type LifecyclePauser = lifecycleworkflow.LifecyclePauser
type LifecycleCanceller = lifecycleworkflow.LifecycleCanceller
type LifecycleProgression = lifecycleworkflow.LifecycleProgression
type LifecycleAuthority = lifecycleworkflow.LifecycleAuthority
type LifecycleExecutionSnapshot = lifecycleworkflow.LifecycleExecutionSnapshot
type LifecycleResumeInput = lifecycleworkflow.LifecycleResumeInput
type LifecycleResumeResult = lifecycleworkflow.LifecycleResumeResult
type LifecyclePauseCancellationInput = lifecycleworkflow.LifecyclePauseCancellationInput
type LifecycleCommandRecord = lifecycleworkflow.LifecycleCommandRecord
type LifecycleWorkflowRepository = lifecycleworkflow.LifecycleWorkflowRepository
type LifecycleWorkflowDependencies = lifecycleworkflow.LifecycleWorkflowDependencies
type LifecycleWorkflow = lifecycleworkflow.LifecycleWorkflow

const (
	TournamentActionOpenRegistration = lifecycleworkflow.TournamentActionOpenRegistration
	TournamentActionStartSwiss       = lifecycleworkflow.TournamentActionStartSwiss
	TournamentActionStartGolden      = lifecycleworkflow.TournamentActionStartGolden
	TournamentActionStartPlayoffs    = lifecycleworkflow.TournamentActionStartPlayoffs
	TournamentActionPause            = lifecycleworkflow.TournamentActionPause
	TournamentActionResume           = lifecycleworkflow.TournamentActionResume
	TournamentActionComplete         = lifecycleworkflow.TournamentActionComplete
	TournamentActionCancel           = lifecycleworkflow.TournamentActionCancel
)

func NewLifecycleWorkflow(deps LifecycleWorkflowDependencies) *LifecycleWorkflow {
	return lifecycleworkflow.NewLifecycleWorkflow(deps)
}

func validTournamentActionCommand(command TournamentActionCommand) bool {
	return lifecycleworkflow.ValidTournamentActionCommand(command)
}

func validTournamentView(view usecase.TournamentView, tournamentID uuid.UUID) bool {
	return lifecycleworkflow.ValidTournamentView(view, tournamentID)
}

func lifecycleActionState(action TournamentAction) (domain.TournamentState, bool) {
	return lifecycleworkflow.LifecycleActionState(action)
}

func validateLifecycleExecutionSnapshot(
	snapshot LifecycleExecutionSnapshot,
	authority LifecycleAuthority,
) error {
	return lifecycleworkflow.ValidateLifecycleExecutionSnapshot(snapshot, authority)
}
