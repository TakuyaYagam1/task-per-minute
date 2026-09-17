package correction

import (
	"time"

	stageusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction/stage"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

var (
	ErrInvalidStage                  = stageusecase.ErrInvalidStage
	ErrStageConflict                 = stageusecase.ErrStageConflict
	ErrInvalidMaterializedProjection = stageusecase.ErrInvalidMaterializedProjection
)

type StageMode = stageusecase.StageMode
type StageTransition = stageusecase.StageTransition
type StagePauseState = stageusecase.StagePauseState
type StagePauseExpectation = stageusecase.StagePauseExpectation
type StageLayout = stageusecase.StageLayout
type StageSnapshot = stageusecase.StageSnapshot
type StageSwissAuthority = stageusecase.StageSwissAuthority
type StageGroupSupersessionIntent = stageusecase.StageGroupSupersessionIntent
type StageCommand = stageusecase.StageCommand
type StageGroupSupersession = stageusecase.StageGroupSupersession
type StageResult = stageusecase.StageResult
type StageCommit = stageusecase.StageCommit
type StageUseCase = stageusecase.StageUseCase

const (
	StageModePlayoff = stageusecase.StageModePlayoff
	StageModeGolden  = stageusecase.StageModeGolden

	StageUnchanged           = stageusecase.StageUnchanged
	StagePlayoffToGolden     = stageusecase.StagePlayoffToGolden
	StageGoldenToPlayoff     = stageusecase.StageGoldenToPlayoff
	StageGoldenGroupsChanged = stageusecase.StageGoldenGroupsChanged

	StagePauseStatePaused = stageusecase.StagePauseStatePaused
)

type MaterializedProjectionState = stageusecase.MaterializedProjectionState
type MaterializedProjectionMember = stageusecase.MaterializedProjectionMember
type MaterializedProjectionArtifact = stageusecase.MaterializedProjectionArtifact
type MaterializedProjections = stageusecase.MaterializedProjections

func NewStageUseCase(
	transactions TransactionManager,
	repository StageRepository,
	clock Clock,
) *StageUseCase {
	return stageusecase.NewStageUseCase(transactions, repository, clock)
}

func PlanStageRollback(
	command StageCommand,
	snapshot StageSnapshot,
	changedAt time.Time,
) (StageResult, error) {
	return stageusecase.PlanStageRollback(command, snapshot, changedAt)
}

func PlanServerOwnedStageRollback(
	correction Plan,
	snapshot StageSnapshot,
	changedAt time.Time,
) (StageResult, error) {
	return stageusecase.PlanServerOwnedStageRollback(correction, snapshot, changedAt)
}

func ValidateServerOwnedStageRollback(
	correction Plan,
	snapshot StageSnapshot,
	changedAt time.Time,
	result StageResult,
) error {
	return stageusecase.ValidateServerOwnedStageRollback(correction, snapshot, changedAt, result)
}

func ApplyServerOwnedSwissSuccessor(
	correction Plan,
	entries []resultprojection.CanonicalSwissPointLedgerEntry,
) ([]resultprojection.CanonicalSwissPointLedgerEntry, error) {
	return stageusecase.ApplyServerOwnedSwissSuccessor(correction, entries)
}

func BuildMaterializedProjections(
	state MaterializedProjectionState,
) (MaterializedProjections, error) {
	return stageusecase.BuildMaterializedProjections(state)
}
