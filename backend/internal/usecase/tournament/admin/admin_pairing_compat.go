package admin

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
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

func validPairingCommandHeader(command PairingCommand) bool {
	return pairingusecase.ValidPairingCommandHeader(command)
}

func validManualPairingCommand(command PairingCommand) bool {
	return pairingusecase.ValidManualPairingCommand(command)
}

func validParticipantPair(pair ParticipantPair) bool {
	return pairingusecase.ValidParticipantPair(pair)
}

func validManualBye(byeParticipantID *uuid.UUID, participants map[uuid.UUID]struct{}) bool {
	return pairingusecase.ValidManualBye(byeParticipantID, participants)
}

func validCategories(categories []domain.Category) bool {
	return pairingusecase.ValidCategories(categories)
}

func validSwissStanding(standing SwissStandingView, total int) bool {
	return pairingusecase.ValidSwissStanding(standing, total)
}
