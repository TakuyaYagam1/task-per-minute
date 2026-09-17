package admin

import (
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
)

type PairingParticipant = pairingusecase.PairingParticipant
type PairingMode = pairingusecase.PairingMode
type ParticipantPair = pairingusecase.ParticipantPair
type PairingCommand = pairingusecase.PairingCommand
type SwissStandingView = pairingusecase.SwissStandingView
type PairingAuthority = pairingusecase.PairingAuthority
type PairingPlan = pairingusecase.PairingPlan
type ManualByeMismatchError = pairingusecase.ManualByeMismatchError

const (
	PairingModeAutomatic = pairingusecase.PairingModeAutomatic
	PairingModeManual    = pairingusecase.PairingModeManual
)

var ErrManualByeMismatch = pairingusecase.ErrManualByeMismatch

func validPairingCommand(command PairingCommand) bool {
	return pairingusecase.ValidPairingCommand(command)
}
