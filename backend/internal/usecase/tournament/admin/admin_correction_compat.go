package admin

import (
	"crypto/sha256"

	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/correction"
)

type ProjectionRevisionExpectation = correctionusecase.ProjectionRevisionExpectation
type CorrectionProjectionIntent = correctionusecase.CorrectionProjectionIntent
type CorrectionUnlockIntent = correctionusecase.CorrectionUnlockIntent
type CorrectionPatch = correctionusecase.CorrectionPatch
type CorrectionCommand = correctionusecase.CorrectionCommand
type ProjectionSupersessionView = correctionusecase.ProjectionSupersessionView
type CorrectionEvidence = correctionusecase.CorrectionEvidence
type CorrectionPort = correctionusecase.CorrectionPort

type CorrectionWorkflowAuthority = correctionusecase.CorrectionWorkflowAuthority
type CorrectionCommandRecord = correctionusecase.CorrectionCommandRecord
type CorrectionMutation = correctionusecase.CorrectionMutation
type CorrectionTransactionManager = correctionusecase.CorrectionTransactionManager
type CorrectionWorkflowRepository = correctionusecase.CorrectionWorkflowRepository
type CorrectionWorkflowDependencies = correctionusecase.CorrectionWorkflowDependencies
type CorrectionWorkflow = correctionusecase.CorrectionWorkflow

func NewCorrectionWorkflow(deps CorrectionWorkflowDependencies) *CorrectionWorkflow {
	return correctionusecase.NewCorrectionWorkflow(deps)
}

func validCorrectionCommand(command CorrectionCommand) bool {
	return correctionusecase.ValidCorrectionCommand(command)
}

func validCorrectionEvidence(evidence CorrectionEvidence, command CorrectionCommand) bool {
	return correctionusecase.ValidCorrectionEvidence(evidence, command)
}

func validCorrectionPatch(patch CorrectionPatch) bool {
	return correctionusecase.ValidCorrectionPatch(patch)
}

func correctionRequestDigest(command CorrectionCommand) ([sha256.Size]byte, error) {
	return correctionusecase.CorrectionRequestDigest(command)
}
