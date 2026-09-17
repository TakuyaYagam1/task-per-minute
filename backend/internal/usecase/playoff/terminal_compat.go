package playoff

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	terminalusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff/terminal"
)

var (
	ErrInvalidFinal                = terminalusecase.ErrInvalidFinal
	ErrInvalidSemifinalAdvancement = terminalusecase.ErrInvalidSemifinalAdvancement
	ErrInvalidTerminalStage        = terminalusecase.ErrInvalidTerminalStage
)

type FinalCommand = terminalusecase.FinalCommand
type FinalProgressionCommand = terminalusecase.FinalProgressionCommand
type Final = terminalusecase.Final

type SemifinalAdvancementResult = terminalusecase.SemifinalAdvancementResult
type SemifinalAdvancementAuthority = terminalusecase.SemifinalAdvancementAuthority
type SemifinalAdvancement = terminalusecase.SemifinalAdvancement

type TerminalSeriesCommand = terminalusecase.TerminalSeriesCommand
type TerminalDraftCommand = terminalusecase.TerminalDraftCommand
type FinalStageIDs = terminalusecase.FinalStageIDs
type FinalPublicationIDs = terminalusecase.FinalPublicationIDs
type FinalGameBinding = terminalusecase.FinalGameBinding
type SemifinalStageAuthority = terminalusecase.SemifinalStageAuthority
type FinalDraftPlan = terminalusecase.FinalDraftPlan
type FinalDraftAuthority = terminalusecase.FinalDraftAuthority
type FinalInitialPlan = terminalusecase.FinalInitialPlan
type FinalSettlementAuthority = terminalusecase.FinalSettlementAuthority
type FinalContinuationPlan = terminalusecase.FinalContinuationPlan
type TerminalReceipt = terminalusecase.TerminalReceipt
type TerminalRepository = terminalusecase.TerminalRepository
type TerminalCoordinatorDependencies = terminalusecase.TerminalCoordinatorDependencies
type FinalDraftAssignmentPlanner = terminalusecase.FinalDraftAssignmentPlanner
type FinalBindingRehydrator = terminalusecase.FinalBindingRehydrator
type ExactDraftCommittedPlanReader = terminalusecase.ExactDraftCommittedPlanReader
type ExactDraftPlanWorkflow = terminalusecase.ExactDraftPlanWorkflow
type ExactDraftPlanAuthorityReader = terminalusecase.ExactDraftPlanAuthorityReader
type FinalDraftAssignmentService = terminalusecase.FinalDraftAssignmentService
type TerminalCoordinator = terminalusecase.TerminalCoordinator

func NewFinal(command FinalCommand) (Final, error) {
	return terminalusecase.NewFinal(command)
}

func ProgressFinal(current Final, command FinalProgressionCommand) (Final, bool, error) {
	return terminalusecase.ProgressFinal(current, command)
}

func AdvanceSemifinalResults(
	current SemifinalAdvancement,
	bracket SemifinalBracket,
	series []domain.Series,
) (SemifinalAdvancement, bool, error) {
	return terminalusecase.AdvanceSemifinalResults(current, bracket, series)
}

func AdvanceSemifinalEvidence(
	current SemifinalAdvancement,
	authority SemifinalAdvancementAuthority,
	series []domain.Series,
) (SemifinalAdvancement, bool, error) {
	return terminalusecase.AdvanceSemifinalEvidence(current, authority, series)
}

func FinalStageIdentity(stageCommandID uuid.UUID) (FinalStageIDs, error) {
	return terminalusecase.FinalStageIdentity(stageCommandID)
}

func FinalPublicationIdentity(
	gameResultRevisionID domain.OfficialResultRevisionID,
) (FinalPublicationIDs, error) {
	return terminalusecase.FinalPublicationIdentity(gameResultRevisionID)
}

func NewFinalDraftAssignmentService(
	workflow ExactDraftPlanWorkflow,
	authority ExactDraftPlanAuthorityReader,
	committed ExactDraftCommittedPlanReader,
) *FinalDraftAssignmentService {
	return terminalusecase.NewFinalDraftAssignmentService(workflow, authority, committed)
}

func NewTerminalCoordinator(deps TerminalCoordinatorDependencies) *TerminalCoordinator {
	return terminalusecase.NewTerminalCoordinator(deps)
}
