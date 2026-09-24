package pause

import (
	"time"

	resumeusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/resume"
)

var (
	ErrInvalidPauseResume      = resumeusecase.ErrInvalidPauseResume
	ErrPauseResumeConflict     = resumeusecase.ErrPauseResumeConflict
	ErrPauseResumeCommandReuse = resumeusecase.ErrPauseResumeCommandReuse
	ErrPauseResumeIncomplete   = resumeusecase.ErrPauseResumeIncomplete
	ErrPauseResumePresence     = resumeusecase.ErrPauseResumePresence
	ErrPauseResumeOverflow     = resumeusecase.ErrPauseResumeOverflow
)

type PauseResumeExpectation = resumeusecase.PauseResumeExpectation
type PauseResumeCommand = resumeusecase.PauseResumeCommand
type PauseResumeAuthority = resumeusecase.PauseResumeAuthority
type PauseResumeRecord = resumeusecase.PauseResumeRecord

func PauseResumeExpectationFrom(authority PauseResumeAuthority) PauseResumeExpectation {
	return resumeusecase.PauseResumeExpectationFrom(authority)
}

func ValidatePauseResumeRecord(record PauseResumeRecord) error {
	return resumeusecase.ValidatePauseResumeRecord(record)
}

func validatePauseResumeCommand(command PauseResumeCommand) error {
	return resumeusecase.ValidatePauseResumeCommand(command)
}

func validatePauseResumeAuthority(authority PauseResumeAuthority) error {
	return resumeusecase.ValidatePauseResumeAuthority(authority)
}

func reconcilePauseResume(record PauseResumeRecord, command PauseResumeCommand) (*PauseResumeRecord, error) {
	return resumeusecase.ReconcilePauseResume(record, command)
}

func buildPauseResumeRecord(authority PauseResumeAuthority, command PauseResumeCommand, resumedAt time.Time) (PauseResumeRecord, error) {
	return resumeusecase.BuildPauseResumeRecord(authority, command, resumedAt)
}

func pauseResumeExpectationEqual(first, second PauseResumeExpectation) bool {
	return resumeusecase.PauseResumeExpectationEqual(first, second)
}

func clonePauseResumeExpectation(value PauseResumeExpectation) PauseResumeExpectation {
	return resumeusecase.ClonePauseResumeExpectation(value)
}
