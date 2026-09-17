package resultprojection

import (
	revisionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

const (
	OfficialResultStatusSolved     = revisionusecase.OfficialResultStatusSolved
	OfficialResultStatusCompleted  = revisionusecase.OfficialResultStatusCompleted
	OfficialResultStatusNoShow     = revisionusecase.OfficialResultStatusNoShow
	OfficialResultStatusVoid       = revisionusecase.OfficialResultStatusVoid
	OfficialResultStatusCancelled  = revisionusecase.OfficialResultStatusCancelled
	OfficialResultStatusSuperseded = revisionusecase.OfficialResultStatusSuperseded

	OfficialResultCauseNoShow = revisionusecase.OfficialResultCauseNoShow

	TerminalResultSourcePlayed          = revisionusecase.TerminalResultSourcePlayed
	TerminalResultSourceNormalNoShow    = revisionusecase.TerminalResultSourceNormalNoShow
	TerminalResultSourcePreStartForfeit = revisionusecase.TerminalResultSourcePreStartForfeit

	MaxRecordedProjectionDecisionBytes = revisionusecase.MaxRecordedProjectionDecisionBytes
)

var (
	ErrInvalidRevisionDAG              = revisionusecase.ErrInvalidRevisionDAG
	ErrInvalidOfficialResultProjection = revisionusecase.ErrInvalidOfficialResultProjection
	ErrInvalidProjectionRebuild        = revisionusecase.ErrInvalidProjectionRebuild
	ErrProjectionRebuildChanged        = revisionusecase.ErrProjectionRebuildChanged
)

type OfficialResultStatus = revisionusecase.OfficialResultStatus
type OfficialResultCause = revisionusecase.OfficialResultCause
type TerminalResultSource = revisionusecase.TerminalResultSource
type PublicOfficialResult = revisionusecase.PublicOfficialResult
type OperatorOfficialResult = revisionusecase.OperatorOfficialResult
type RecordedNoGameResult = revisionusecase.RecordedNoGameResult
type RecordedNoGameAttempt = revisionusecase.RecordedNoGameAttempt
type OfficialResultProjectionInput = revisionusecase.OfficialResultProjectionInput
type OfficialResultProjectionPlan = revisionusecase.OfficialResultProjectionPlan
type RevisionDAGInput = revisionusecase.RevisionDAGInput
type RevisionDAGSnapshot = revisionusecase.RevisionDAGSnapshot
type RevisionDAG = revisionusecase.RevisionDAG
type RecordedProjectionDecision = revisionusecase.RecordedProjectionDecision
type ProjectionRebuildInput = revisionusecase.ProjectionRebuildInput
type ProjectionRebuild = revisionusecase.ProjectionRebuild

func ProjectOfficialResult(input OfficialResultProjectionInput) (OfficialResultProjectionPlan, error) {
	return revisionusecase.ProjectOfficialResult(input)
}

func RestoreSQLNoGameResult(recorded RecordedNoGameResult) (RecordedNoGameResult, error) {
	return revisionusecase.RestoreSQLNoGameResult(recorded)
}

func BuildRevisionDAG(input RevisionDAGInput) (RevisionDAG, error) {
	return revisionusecase.BuildRevisionDAG(input)
}

func RebuildOfficialProjections(input ProjectionRebuildInput) (ProjectionRebuild, error) {
	return revisionusecase.RebuildOfficialProjections(input)
}
