package admin

import (
	"crypto/sha256"

	replayusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
)

type ReserveCommand = replayusecase.ReserveCommand
type ReplayCommand = replayusecase.ReplayCommand
type ReservePort = replayusecase.ReservePort
type ReplayPort = replayusecase.ReplayPort
type OperatorReserveAuthority = replayusecase.OperatorReserveAuthority
type ReplayReplacementAuthority = replayusecase.ReplayReplacementAuthority
type ReplayWorkflowRepository = replayusecase.ReplayWorkflowRepository
type ReplayWorkflowDependencies = replayusecase.ReplayWorkflowDependencies
type ReplayWorkflow = replayusecase.ReplayWorkflow

func NewReplayWorkflow(deps ReplayWorkflowDependencies) *ReplayWorkflow {
	return replayusecase.NewReplayWorkflow(deps)
}

func validReserveCommand(command ReserveCommand) bool {
	return replayusecase.ValidReserveCommand(command)
}

func validReplayCommand(command ReplayCommand) bool {
	return replayusecase.ValidReplayCommand(command)
}

func replayWorkflowDigest(action string, command any) ([sha256.Size]byte, error) {
	return replayusecase.ReplayWorkflowDigest(action, command)
}
