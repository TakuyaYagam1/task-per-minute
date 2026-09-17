package pause

import (
	"time"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	presenceusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/presence"
)

var (
	ErrInvalidPausedPresence      = presenceusecase.ErrInvalidPausedPresence
	ErrPausedPresenceConflict     = presenceusecase.ErrPausedPresenceConflict
	ErrPausedPresenceCommandReuse = presenceusecase.ErrPausedPresenceCommandReuse
	ErrPausedPresenceSuppression  = presenceusecase.ErrPausedPresenceSuppression
	ErrPausedPresenceState        = presenceusecase.ErrPausedPresenceState
	ErrPausedPresenceOverflow     = presenceusecase.ErrPausedPresenceOverflow
)

type PausedPresenceExpectation = presenceusecase.PausedPresenceExpectation
type PausedPresenceCommand = presenceusecase.PausedPresenceCommand
type PausedPresenceAuthority = presenceusecase.PausedPresenceAuthority
type PausedPresenceRecord = presenceusecase.PausedPresenceRecord

func validatePausedPresenceCommand(command PausedPresenceCommand) error {
	return presenceusecase.ValidatePausedPresenceCommand(command)
}

func validatePausedPresenceAuthority(authority PausedPresenceAuthority) error {
	return presenceusecase.ValidatePausedPresenceAuthority(authority)
}

func matchPausedPresenceCommand(authority PausedPresenceAuthority, command PausedPresenceCommand) error {
	return presenceusecase.MatchPausedPresenceCommand(authority, command)
}

func buildPausedPresenceRecord(authority PausedPresenceAuthority, command PausedPresenceCommand, changedAt time.Time) (PausedPresenceRecord, error) {
	return presenceusecase.BuildPausedPresenceRecord(authority, command, changedAt)
}

func reconcilePausedPresence(record PausedPresenceRecord, command PausedPresenceCommand) (*PausedPresenceRecord, error) {
	return presenceusecase.ReconcilePausedPresence(record, command)
}

func pausedPresenceExpectation(authority PausedPresenceAuthority, command PausedPresenceCommand) PausedPresenceExpectation {
	return presenceusecase.PausedPresenceExpectationFrom(authority, command)
}

func pausedPresenceRecordsEqual(first, second PausedPresenceRecord) bool {
	return presenceusecase.PausedPresenceRecordsEqual(first, second)
}

func samePausePresenceIdentity(first, second pausedomain.PausePresence) bool {
	return presenceusecase.SamePausePresenceIdentity(first, second)
}
