package admin

import (
	"github.com/google/uuid"

	executionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
)

type WaveAction = executionusecase.WaveAction
type WaveCommand = executionusecase.WaveCommand
type SwissPairingEvidenceView = executionusecase.SwissPairingEvidenceView
type SwissPairingView = executionusecase.SwissPairingView
type SwissByeView = executionusecase.SwissByeView
type SwissRoundView = executionusecase.SwissRoundView
type WaveView = executionusecase.WaveView
type PairingPort = executionusecase.PairingPort
type WavePort = executionusecase.WavePort

const (
	WaveActionOpenReadyWindow = executionusecase.WaveActionOpenReadyWindow
	WaveActionStart           = executionusecase.WaveActionStart
	WaveActionPause           = executionusecase.WaveActionPause
	WaveActionResume          = executionusecase.WaveActionResume
	WaveActionComplete        = executionusecase.WaveActionComplete
	WaveActionCancel          = executionusecase.WaveActionCancel
)

func validWaveCommand(command WaveCommand) bool {
	return executionusecase.ValidWaveCommand(command)
}

func validSwissRoundView(view SwissRoundView, tournamentID uuid.UUID, roundNumber int) bool {
	return executionusecase.ValidSwissRoundView(view, tournamentID, roundNumber)
}

func validSwissStandings(view SwissRoundView, roster map[uuid.UUID]struct{}) bool {
	return executionusecase.ValidSwissStandings(view, roster)
}

func validWaveView(view WaveView, tournamentID, waveID uuid.UUID) bool {
	return executionusecase.ValidWaveView(view, tournamentID, waveID)
}
