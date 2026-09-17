package admin

import (
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/result"
)

type NoShowCommand = resultusecase.NoShowCommand
type GameExpectation = resultusecase.GameExpectation
type ForfeitCommand = resultusecase.ForfeitCommand
type NoShowPort = resultusecase.NoShowPort
type ForfeitPort = resultusecase.ForfeitPort

type OperatorResultTransactionManager = resultusecase.OperatorResultTransactionManager
type OperatorResultAction = resultusecase.OperatorResultAction
type OperatorResultAuthority = resultusecase.OperatorResultAuthority
type OperatorResultCommandRecord = resultusecase.OperatorResultCommandRecord
type OperatorResultWorkflowRepository = resultusecase.OperatorResultWorkflowRepository
type AdminPostseasonWorkflow = resultusecase.PostseasonWorkflow
type OperatorResultWorkflowDependencies = resultusecase.OperatorResultWorkflowDependencies
type OperatorResultWorkflow = resultusecase.OperatorResultWorkflow

const (
	OperatorResultActionNoShow  = resultusecase.OperatorResultActionNoShow
	OperatorResultActionForfeit = resultusecase.OperatorResultActionForfeit
)

func NewOperatorResultWorkflow(deps OperatorResultWorkflowDependencies) *OperatorResultWorkflow {
	return resultusecase.NewOperatorResultWorkflow(deps)
}

func validNoShowCommand(command NoShowCommand) bool {
	return resultusecase.ValidNoShowCommand(command)
}

func validForfeitCommand(command ForfeitCommand) bool {
	return resultusecase.ValidForfeitCommand(command)
}

func operatorResultDigest(action OperatorResultAction, command any) ([32]byte, error) {
	return resultusecase.OperatorResultDigest(action, command)
}
